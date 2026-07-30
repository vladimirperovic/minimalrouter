package apply

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/services"
)

// State represents current transaction status.
type State string

const (
	StateReceived             State = "Received"
	StatePlanned              State = "Planned"
	StateGenerated            State = "Generated"
	StateSnapshotted          State = "Snapshotted"
	StateApplied              State = "Applied"
	StateVerified             State = "Verified"
	StateAwaitingConfirmation State = "AwaitingConfirmation"
	StateCommitted            State = "Committed"
	StateRolledBack           State = "RolledBack"
	StateRejected             State = "Rejected"
)

// Transaction tracks an individual configuration change execution.
type Transaction struct {
	ID                   string              `json:"id"`
	CurrentState         State               `json:"state"`
	Config               config.SystemConfig `json:"config"`
	Diff                 string              `json:"diff"`
	Error                string              `json:"error,omitempty"`
	CreatedAt            time.Time           `json:"created_at"`
	ConfirmedAt          *time.Time          `json:"confirmed_at,omitempty"`
	ConfirmationDeadline *time.Time          `json:"confirmation_deadline,omitempty"`
}

const (
	confirmationTimeout     = 90 * time.Second
	rollbackRetryDelay      = 10 * time.Second
	privilegedApplyTimeout  = 2 * time.Minute
	privilegedApplyAttempts = 2
)

type pendingChange struct {
	tx               *Transaction
	previous         config.SystemConfig
	timer            *time.Timer
	rollbackAttempts int
}

// Engine manages execution of configuration transactions.
type Engine struct {
	mu            sync.Mutex
	activeTx      *Transaction
	currentConfig config.SystemConfig
	store         *config.FileStore
	client        Client
	pending       *pendingChange
}

// NewEngine initializes transaction engine with base configuration and store.
func NewEngine(initial config.SystemConfig, store *config.FileStore) *Engine {
	return NewEngineWithClient(initial, store, NewUnixClient(DefaultSocketPath))
}

func NewEngineWithClient(initial config.SystemConfig, store *config.FileStore, client Client) *Engine {
	return &Engine{
		currentConfig: initial,
		store:         store,
		client:        client,
	}
}

// applyPrivileged retries only ambiguous transport failures. The helper
// deduplicates by transaction ID, so replaying the exact request safely recovers
// a response that was lost after a completed privileged operation.
func (e *Engine) applyPrivileged(ctx context.Context, req ApplyRequest) (*ApplyResponse, error) {
	var lastErr error
	for attempt := 0; attempt < privilegedApplyAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, err
		}
		resp, err := e.client.Apply(ctx, req)
		if err == nil {
			if resp == nil {
				lastErr = fmt.Errorf("router-applyd returned an empty response")
				continue
			}
			return resp, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// ProcessTransaction executes the full state machine pipeline with snapshot and rollback.
func (e *Engine) ProcessTransaction(txID string, newCfg config.SystemConfig) (*Transaction, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	tx := &Transaction{
		ID:           txID,
		CurrentState: StateReceived,
		Config:       newCfg,
		CreatedAt:    time.Now(),
	}
	e.activeTx = tx
	if e.pending != nil {
		tx.CurrentState = StateRejected
		tx.Error = "another configuration is awaiting confirmation"
		return tx, fmt.Errorf("%s", tx.Error)
	}

	if newCfg.Revision != e.currentConfig.Revision {
		tx.CurrentState = StateRejected
		tx.Error = fmt.Sprintf("stale revision: expected %d, received %d", e.currentConfig.Revision, newCfg.Revision)
		return tx, fmt.Errorf("%s", tx.Error)
	}
	if newCfg.LAN.Interface != e.currentConfig.LAN.Interface {
		tx.CurrentState = StateRejected
		tx.Error = "live LAN interface changes are unsupported; use the local recovery console"
		return tx, fmt.Errorf("%s", tx.Error)
	}
	if newCfg.System.HTTPSPort != e.currentConfig.System.HTTPSPort {
		tx.CurrentState = StateRejected
		tx.Error = "live management-port changes are unsupported"
		return tx, fmt.Errorf("%s", tx.Error)
	}
	if newCfg.System.ManagementAccess == "wireguard_only" &&
		e.currentConfig.System.ManagementAccess != "wireguard_only" &&
		!e.currentConfig.WireGuard.Enabled {
		tx.CurrentState = StateRejected
		tx.Error = "enable and verify WireGuard in a separate transaction before restricting management access"
		return tx, fmt.Errorf("%s", tx.Error)
	}
	if err := newCfg.Validate(); err != nil {
		tx.CurrentState = StateRejected
		tx.Error = fmt.Sprintf("Validation failed: %v", err)
		return tx, err
	}
	newCfg.Revision = e.currentConfig.Revision + 1
	newCfg.UpdatedAt = time.Now()
	tx.Config = newCfg
	tx.CurrentState = StatePlanned

	nftablesCfg, err := services.GenerateNftables(&newCfg)
	if err != nil {
		tx.CurrentState = StateRejected
		tx.Error = fmt.Sprintf("nftables generator failed: %v", err)
		return tx, err
	}
	pppoeBundle, err := services.GeneratePPPoE(&newCfg)
	if err != nil {
		tx.CurrentState = StateRejected
		tx.Error = fmt.Sprintf("pppd generator failed: %v", err)
		return tx, err
	}
	dnsmasqCfg, err := services.GenerateDnsmasq(&newCfg)
	if err != nil {
		tx.CurrentState = StateRejected
		tx.Error = fmt.Sprintf("dnsmasq generator failed: %v", err)
		return tx, err
	}
	hostapdCfg, err := services.GenerateHostapd(&newCfg)
	if err != nil {
		tx.CurrentState = StateRejected
		tx.Error = fmt.Sprintf("hostapd generator failed: %v", err)
		return tx, err
	}
	wireGuardCfg, err := services.GenerateWireGuard(&newCfg.WireGuard)
	if err != nil {
		tx.CurrentState = StateRejected
		tx.Error = fmt.Sprintf("WireGuard generator failed: %v", err)
		return tx, err
	}
	tx.CurrentState = StateGenerated

	if e.store != nil {
		if _, err := e.store.CreateSnapshot(e.currentConfig); err != nil {
			tx.CurrentState = StateRejected
			tx.Error = fmt.Sprintf("Pre-apply snapshot creation failed: %v", err)
			return tx, err
		}
	}
	tx.CurrentState = StateSnapshotted

	applyReq := ApplyRequest{
		ID:                  txID,
		Op:                  OpApplyAll,
		Revision:            newCfg.Revision,
		Config:              newCfg,
		Nftables:            nftablesCfg,
		PPPoEPeer:           pppoeBundle.PeerConfig,
		PPPoESecret:         pppoeBundle.ChapSecrets,
		Dnsmasq:             dnsmasqCfg,
		Hostapd:             hostapdCfg,
		WireGuard:           wireGuardCfg,
		RequireConfirmation: requiresConfirmation(e.currentConfig, newCfg),
	}
	ctx, cancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)
	defer cancel()
	resp, err := e.applyPrivileged(ctx, applyReq)
	if err != nil {
		tx.CurrentState = StateRolledBack
		tx.Error = fmt.Sprintf("privileged apply outcome remained unknown after retry; reboot reconciliation will restore the canonical configuration: %v", err)
		return tx, fmt.Errorf("%s", tx.Error)
	}
	if !resp.Success {
		tx.CurrentState = StateRolledBack
		tx.Error = fmt.Sprintf("privileged apply failed: %s", resp.Error)
		return tx, fmt.Errorf("apply rejected by router-applyd: %s", resp.Error)
	}
	tx.CurrentState = StateApplied
	if !resp.Verified {
		tx.CurrentState = StateRolledBack
		tx.Error = "router-applyd did not verify the active configuration"
		return tx, fmt.Errorf("%s", tx.Error)
	}
	tx.CurrentState = StateVerified

	tx.Config = newCfg
	if applyReq.RequireConfirmation {
		deadline := time.Now().Add(confirmationTimeout)
		tx.CurrentState = StateAwaitingConfirmation
		tx.ConfirmationDeadline = &deadline
		pending := &pendingChange{tx: tx, previous: e.currentConfig}
		pending.timer = time.AfterFunc(confirmationTimeout, func() { e.rollbackExpired(tx.ID) })
		e.pending = pending
		return tx, nil
	}
	if e.store != nil {
		if err := e.store.SaveConfig(newCfg); err != nil {
			tx.CurrentState = StateRolledBack
			tx.Error = fmt.Sprintf("Failed to commit config store: %v", err)
			rollbackReq, buildErr := buildApplyRequest(txID+"-rollback", e.currentConfig)
			if buildErr == nil {
				rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)
				_, _ = e.applyPrivileged(rollbackCtx, rollbackReq)
				rollbackCancel()
			}
			return tx, err
		}
	}
	tx.CurrentState = StateCommitted
	e.currentConfig = newCfg
	return tx, nil
}

func requiresConfirmation(current, candidate config.SystemConfig) bool {
	wireGuardManagementChanged :=
		(current.System.ManagementAccess == "wireguard_only" || candidate.System.ManagementAccess == "wireguard_only") &&
			!reflect.DeepEqual(current.WireGuard, candidate.WireGuard)
	return current.LAN.IPAddress != candidate.LAN.IPAddress ||
		current.LAN.CIDR != candidate.LAN.CIDR ||
		current.System.ManagementAccess != candidate.System.ManagementAccess ||
		current.WiFi.Enabled != candidate.WiFi.Enabled ||
		current.WiFi.Interface != candidate.WiFi.Interface ||
		wireGuardManagementChanged
}

func (e *Engine) GetPendingTransaction() *Transaction {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending == nil || e.pending.tx == nil {
		return nil
	}
	copy := *e.pending.tx
	return &copy
}

func (e *Engine) ConfirmTransaction(txID string) (*Transaction, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending == nil || e.pending.tx.ID != txID {
		return nil, fmt.Errorf("transaction is not awaiting confirmation")
	}
	pending := e.pending
	req := ApplyRequest{ID: txID + "-confirm", Op: OpConfirm, Revision: pending.tx.Config.Revision, Config: pending.tx.Config}
	ctx, cancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)
	resp, err := e.applyPrivileged(ctx, req)
	cancel()
	if err != nil || !resp.Success || !resp.Verified {
		return pending.tx, fmt.Errorf("privileged confirmation failed")
	}
	if e.store != nil {
		if err := e.store.SaveConfig(pending.tx.Config); err != nil {
			return pending.tx, fmt.Errorf("failed to commit confirmed configuration: %w", err)
		}
	}
	now := time.Now()
	pending.tx.ConfirmedAt = &now
	pending.tx.CurrentState = StateCommitted
	pending.timer.Stop()
	e.currentConfig = pending.tx.Config
	e.pending = nil
	e.activeTx = pending.tx
	return pending.tx, nil
}

func (e *Engine) rollbackExpired(txID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending == nil || e.pending.tx.ID != txID {
		return
	}
	pending := e.pending
	pending.rollbackAttempts++
	rollbackID := fmt.Sprintf("%s-timeout-rollback-%d", txID, pending.rollbackAttempts)
	req, err := buildApplyRequest(rollbackID, pending.previous)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)
		resp, applyErr := e.applyPrivileged(ctx, req)
		cancel()
		if applyErr == nil && resp.Success && resp.Verified {
			pending.tx.CurrentState = StateRolledBack
			pending.tx.Error = "confirmation deadline expired; previous configuration restored"
			if pending.timer != nil {
				pending.timer.Stop()
			}
			e.pending = nil
			return
		}
		pending.tx.Error = "confirmation deadline expired and privileged rollback failed; retry scheduled while candidate access remains available"
	} else {
		pending.tx.Error = "confirmation deadline expired and rollback generation failed; retry scheduled while candidate access remains available"
	}
	pending.timer = time.AfterFunc(rollbackRetryDelay, func() { e.rollbackExpired(txID) })
}

func buildApplyRequest(txID string, cfg config.SystemConfig) (ApplyRequest, error) {
	nftablesCfg, err := services.GenerateNftables(&cfg)
	if err != nil {
		return ApplyRequest{}, err
	}
	pppoeBundle, err := services.GeneratePPPoE(&cfg)
	if err != nil {
		return ApplyRequest{}, err
	}
	dnsmasqCfg, err := services.GenerateDnsmasq(&cfg)
	if err != nil {
		return ApplyRequest{}, err
	}
	hostapdCfg, err := services.GenerateHostapd(&cfg)
	if err != nil {
		return ApplyRequest{}, err
	}
	wireGuardCfg, err := services.GenerateWireGuard(&cfg.WireGuard)
	if err != nil {
		return ApplyRequest{}, err
	}
	return ApplyRequest{ID: txID, Op: OpApplyAll, Revision: cfg.Revision, Config: cfg, Nftables: nftablesCfg, PPPoEPeer: pppoeBundle.PeerConfig, PPPoESecret: pppoeBundle.ChapSecrets, Dnsmasq: dnsmasqCfg, Hostapd: hostapdCfg, WireGuard: wireGuardCfg}, nil
}

func (e *Engine) Reconcile(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	req, err := buildApplyRequest(fmt.Sprintf("boot-reconcile-%d", time.Now().UnixNano()), e.currentConfig)
	if err != nil {
		return err
	}
	resp, err := e.applyPrivileged(ctx, req)
	if err != nil {
		return fmt.Errorf("boot reconciliation failed: %w", err)
	}
	if !resp.Success || !resp.Verified {
		return fmt.Errorf("boot reconciliation was not verified: %s", resp.Error)
	}
	return nil
}

func (e *Engine) GetCurrentConfig() config.SystemConfig {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.currentConfig
}

func (e *Engine) GetStore() *config.FileStore { return e.store }
