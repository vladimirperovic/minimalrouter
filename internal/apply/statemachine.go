package apply

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/faultinject"
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
	StateRecoveryRequired     State = "RecoveryRequired"
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
	maximumRollbackAttempts = 5
	privilegedApplyTimeout  = 2 * time.Minute
	privilegedApplyAttempts = 2
)

// ReconcileBudget is the wall-clock budget a caller should give Reconcile. It
// must stay above privilegedApplyTimeout * privilegedApplyAttempts so a
// transport retry is never cancelled halfway through, and routerd.initd's
// start_post wait must in turn stay above this value.
const ReconcileBudget = privilegedApplyTimeout*privilegedApplyAttempts + 30*time.Second

type pendingChange struct {
	tx                 *Transaction
	previous           config.SystemConfig
	timer              *time.Timer
	rollbackAttempts   int
	commitAttempts     int
	canonicalCommitted bool
}

// EngineStatus is an immutable management-plane snapshot that remains
// readable while a privileged mutation is in progress.
type EngineStatus struct {
	Applying            bool   `json:"apply_in_progress"`
	RecoveryRequired    bool   `json:"recovery_required"`
	RecoveryReason      string `json:"recovery_reason,omitempty"`
	ActiveTransactionID string `json:"transaction_id,omitempty"`
	ActiveState         State  `json:"transaction_state,omitempty"`
}

// Engine manages execution of configuration transactions.
type Engine struct {
	operationMu      sync.Mutex
	mu               sync.RWMutex
	activeTx         *Transaction
	applying         bool
	currentConfig    config.SystemConfig
	store            *config.FileStore
	client           Client
	pending          *pendingChange
	recoveryRequired bool
	recoveryReason   string
}

// NewEngine initializes transaction engine with base configuration and store.
func NewEngine(initial config.SystemConfig, store *config.FileStore) *Engine {
	return NewEngineWithClient(initial, store, NewUnixClient(DefaultSocketPath))
}

func NewEngineWithClient(initial config.SystemConfig, store *config.FileStore, client Client) *Engine {
	return &Engine{currentConfig: initial, store: store, client: client}
}

// applyPrivileged retries only ambiguous transport failures. The helper
// deduplicates by transaction ID, so replaying the exact request safely recovers
// a response that was lost after a completed privileged operation.
func (e *Engine) applyPrivileged(ctx context.Context, req ApplyRequest) (*ApplyResponse, error) {
	switch req.Op {
	case OpApplyAll, OpConfirm:
		faultinject.Run(faultinject.PrePrivilegedApply)
	case OpCommitConfirmed:
		faultinject.Run(faultinject.PreCanonicalAck)
	case OpReconcile:
		faultinject.Run(faultinject.DuringFinalReconcile)
	}
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
			if validationErr := resp.Validate(); validationErr != nil {
				return nil, fmt.Errorf("router-applyd returned an invalid outcome: %w", validationErr)
			}
			return resp, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (e *Engine) requireRecovery(reason string) {
	e.recoveryRequired = true
	e.recoveryReason = reason
}

// ProcessTransaction executes the full state machine pipeline with snapshot and rollback.
func (e *Engine) ProcessTransaction(txID string, newCfg config.SystemConfig) (*Transaction, error) {
	return e.processTransaction(txID, newCfg, false, nil)
}

// ProcessInitialSetup permits the one-time selection of real WAN/LAN interface
// names. The supplied commit function must atomically persist both the verified
// network configuration and the administrator credential.
func (e *Engine) ProcessInitialSetup(txID string, newCfg config.SystemConfig, commit func(config.SystemConfig) error) (*Transaction, error) {
	if commit == nil {
		return nil, fmt.Errorf("initial setup commit function is required")
	}
	return e.processTransaction(txID, newCfg, true, commit)
}

func sameIPv4Network(a, b string) bool {
	_, aNet, aErr := net.ParseCIDR(a)
	_, bNet, bErr := net.ParseCIDR(b)
	if aErr != nil || bErr != nil || aNet.IP.To4() == nil || bNet.IP.To4() == nil {
		return false
	}
	aOnes, aBits := aNet.Mask.Size()
	bOnes, bBits := bNet.Mask.Size()
	return aBits == 32 && bBits == 32 && aOnes == bOnes && aNet.IP.Equal(bNet.IP)
}

func (e *Engine) processTransaction(txID string, newCfg config.SystemConfig, allowInterfaceChange bool, commit func(config.SystemConfig) error) (*Transaction, error) {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()

	tx := &Transaction{ID: txID, CurrentState: StateReceived, Config: newCfg, CreatedAt: time.Now()}
	e.activeTx = tx
	if e.recoveryRequired {
		tx.CurrentState = StateRecoveryRequired
		tx.Error = "configuration changes are blocked until canonical reconciliation succeeds"
		if e.recoveryReason != "" {
			tx.Error += ": " + e.recoveryReason
		}
		return tx, fmt.Errorf("%s", tx.Error)
	}
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
	if !allowInterfaceChange && newCfg.LAN.Interface != e.currentConfig.LAN.Interface {
		tx.CurrentState = StateRejected
		tx.Error = "live LAN interface changes are unsupported; use the local recovery console"
		return tx, fmt.Errorf("%s", tx.Error)
	}
	// A cross-subnet change cannot safely migrate already leased DHCP clients:
	// they retain the old gateway/DNS until renewal while the new anti-spoof
	// policy rejects their old source network. Until a durable dual-policy lease
	// migration exists, require the local recovery console for subnet changes.
	if !allowInterfaceChange && !sameIPv4Network(e.currentConfig.LAN.CIDR, newCfg.LAN.CIDR) {
		tx.CurrentState = StateRejected
		tx.Error = "live LAN subnet changes are unsupported; use the local recovery console"
		return tx, fmt.Errorf("%s", tx.Error)
	}
	if newCfg.System.HTTPSPort != e.currentConfig.System.HTTPSPort {
		tx.CurrentState = StateRejected
		tx.Error = "live management-port changes are unsupported"
		return tx, fmt.Errorf("%s", tx.Error)
	}
	if newCfg.System.ManagementAccess == "wireguard_only" && e.currentConfig.System.ManagementAccess != "wireguard_only" && !e.currentConfig.WireGuard.Enabled {
		tx.CurrentState = StateRejected
		tx.Error = "enable and verify WireGuard in a separate transaction before restricting management access"
		return tx, fmt.Errorf("%s", tx.Error)
	}
	// Judge the change, not the whole stored state: a configuration written by
	// an older release can already violate a newer rule, and that must not make
	// every later edit impossible. Scenario and transition safety below still
	// see the complete candidate.
	if err := newCfg.ValidateChangesFrom(&e.currentConfig); err != nil {
		tx.CurrentState = StateRejected
		tx.Error = fmt.Sprintf("Validation failed: %v", err)
		return tx, err
	}
	if err := newCfg.ValidateScenarioSafety(); err != nil {
		tx.CurrentState = StateRejected
		tx.Error = fmt.Sprintf("Scenario safety validation failed: %v", err)
		return tx, err
	}
	if !allowInterfaceChange {
		if err := validateTransitionSafety(e.currentConfig, newCfg); err != nil {
			tx.CurrentState = StateRejected
			tx.Error = "Transition safety validation failed: " + err.Error()
			return tx, err
		}
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

	commitConfig := commit
	if commitConfig == nil && e.store != nil {
		commitConfig = e.store.SaveConfig
	}
	applyReq := ApplyRequest{
		ID: txID, Op: OpApplyAll, Revision: newCfg.Revision, Config: newCfg,
		Nftables: nftablesCfg, PPPoEPeer: pppoeBundle.PeerConfig, PPPoESecret: pppoeBundle.ChapSecrets,
		Dnsmasq: dnsmasqCfg, Hostapd: hostapdCfg, WireGuard: wireGuardCfg,
		RequireConfirmation: !allowInterfaceChange && requiresConfirmation(e.currentConfig, newCfg),
	}
	// Canonical persistence always precedes helper last-good persistence when a
	// commit callback exists, including first-run setup. This removes the setup
	// power-loss window where root could boot a newer network than SQLite/anon.
	applyReq.DeferLastGood = !applyReq.RequireConfirmation && commitConfig != nil
	ctx, cancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)
	defer cancel()
	e.applying = true
	e.mu.Unlock()
	resp, err := e.applyPrivileged(ctx, applyReq)
	e.mu.Lock()
	e.applying = false
	if err != nil {
		tx.CurrentState = StateRecoveryRequired
		tx.Error = fmt.Sprintf("privileged apply outcome remained unknown after retry; recovery or reboot reconciliation is required: %v", err)
		e.requireRecovery(tx.Error)
		return tx, fmt.Errorf("%s", tx.Error)
	}
	if !resp.Success {
		switch {
		case resp.RecoveryRequired:
			tx.CurrentState = StateRecoveryRequired
		case resp.RolledBack:
			tx.CurrentState = StateRolledBack
		default:
			tx.CurrentState = StateRejected
		}
		tx.Error = fmt.Sprintf("privileged apply failed: %s", resp.Error)
		if tx.CurrentState == StateRecoveryRequired {
			e.requireRecovery(tx.Error)
		}
		return tx, fmt.Errorf("apply rejected by router-applyd: %s", resp.Error)
	}
	tx.CurrentState = StateApplied
	if !resp.Verified {
		tx.CurrentState = StateRecoveryRequired
		tx.Error = "router-applyd did not verify the active configuration; recovery is required"
		e.requireRecovery(tx.Error)
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
	if commitConfig != nil {
		faultinject.Run(faultinject.PreSQLiteCommit)
		if err := commitConfig(newCfg); err != nil {
			tx.Error = fmt.Sprintf("failed to commit config store: %v", err)
			tx.CurrentState = StateRecoveryRequired
			rollbackReq, buildErr := buildApplyRequest(txID+"-rollback", e.currentConfig)
			if buildErr == nil {
				rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)
				e.applying = true
				e.mu.Unlock()
				rollbackResp, rollbackErr := e.applyPrivileged(rollbackCtx, rollbackReq)
				e.mu.Lock()
				e.applying = false
				rollbackCancel()
				if rollbackErr == nil && rollbackResp.Success && rollbackResp.Verified && !rollbackResp.RecoveryRequired {
					tx.CurrentState = StateRolledBack
					tx.Error += "; previous configuration was verified restored"
				} else {
					tx.Error += "; rollback could not be verified and recovery is required"
				}
			} else {
				tx.Error += "; rollback request could not be generated and recovery is required"
			}
			if tx.CurrentState == StateRecoveryRequired {
				e.requireRecovery(tx.Error)
			}
			return tx, err
		}
		faultinject.Run(faultinject.PostSQLiteCommit)
		// The durable store now holds newCfg: the in-memory canon must follow it
		// immediately, before the helper's last-good acknowledgement below. If
		// that acknowledgement fails, RecoveryRequired still triggers a Reconcile,
		// but Reconcile builds its request from e.currentConfig — it must rebuild
		// from the config that is actually committed and running, not revert to
		// the pre-transaction config (see F1 in the 2026-09-05 audit).
		e.currentConfig = newCfg
		if applyReq.DeferLastGood {
			ackID := txID + "-commit-canonical-0"
			ackReq := ApplyRequest{ID: ackID, Op: OpCommitConfirmed, Revision: newCfg.Revision, Config: newCfg, SkipWANVerify: true}
			ackCtx, ackCancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)
			e.applying = true
			e.mu.Unlock()
			ackResp, ackErr := e.applyPrivileged(ackCtx, ackReq)
			e.mu.Lock()
			e.applying = false
			ackCancel()
			if ackErr != nil || !ackResp.Success || !ackResp.Verified {
				tx.CurrentState = StateRecoveryRequired
				if ackErr != nil {
					tx.Error = fmt.Sprintf("canonical configuration was committed but helper last-good acknowledgement is unknown: %v", ackErr)
				} else {
					tx.Error = "canonical configuration was committed but the helper last-good acknowledgement failed: " + ackResp.Error
				}
				e.requireRecovery(tx.Error)
				return tx, fmt.Errorf("helper commit acknowledgement failed")
			}
		}
	}
	tx.CurrentState = StateCommitted
	e.currentConfig = newCfg
	return tx, nil
}

func requiresConfirmation(current, candidate config.SystemConfig) bool {
	wireGuardManagementChanged := (current.System.ManagementAccess == "wireguard_only" || candidate.System.ManagementAccess == "wireguard_only") && !reflect.DeepEqual(current.WireGuard, candidate.WireGuard)
	wifiChanged := !reflect.DeepEqual(current.WiFi, candidate.WiFi) && (current.WiFi.Enabled || candidate.WiFi.Enabled)
	wgClientChanged := !reflect.DeepEqual(current.WGClient, candidate.WGClient)
	trustedNetworksChanged := !reflect.DeepEqual(current.TrustedNetworks, candidate.TrustedNetworks)
	return current.LAN.IPAddress != candidate.LAN.IPAddress || current.LAN.CIDR != candidate.LAN.CIDR ||
		current.System.ManagementAccess != candidate.System.ManagementAccess || wifiChanged || wireGuardManagementChanged || wgClientChanged || trustedNetworksChanged
}

func (e *Engine) GetPendingTransaction() *Transaction {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.pending == nil || e.pending.tx == nil {
		return nil
	}
	copy := *e.pending.tx
	return &copy
}

func verifyWGClientStatusForConfirmation(status *TunnelStatus, keepalive int) error {
	if status == nil || status.LastHandshake <= 0 {
		return fmt.Errorf("WireGuard client tunnel has no completed handshake")
	}
	if keepalive <= 0 {
		return nil
	}
	maxAge := 3 * time.Minute
	if age := time.Duration(keepalive) * 3 * time.Second; age > maxAge {
		maxAge = age
	}
	age := time.Since(time.Unix(status.LastHandshake, 0))
	if age > maxAge {
		return fmt.Errorf("WireGuard client handshake is stale (last %s ago)", age.Round(time.Second))
	}
	return nil
}

func (e *Engine) ConfirmTransaction(txID string) (*Transaction, error) {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending == nil || e.pending.tx.ID != txID {
		return nil, fmt.Errorf("transaction is not awaiting confirmation")
	}
	pending := e.pending
	if !pending.canonicalCommitted {
		if !reflect.DeepEqual(pending.previous.WGClient, pending.tx.Config.WGClient) && pending.tx.Config.WGClient.Enabled {
			statusReq := ApplyRequest{ID: txID + "-wg1-status", Op: OpWGTunnelStatus, Config: pending.tx.Config, TunnelInterface: pending.tx.Config.WGClient.Interface}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			e.applying = true
			e.mu.Unlock()
			statusResp, statusErr := e.applyPrivileged(ctx, statusReq)
			e.mu.Lock()
			e.applying = false
			cancel()
			if statusErr != nil || statusResp == nil || !statusResp.Success || statusResp.TunnelStatus == nil {
				pending.tx.Error = "WireGuard client confirmation failed: tunnel status is unavailable"
				return pending.tx, fmt.Errorf("%s", pending.tx.Error)
			}
			if err := verifyWGClientStatusForConfirmation(statusResp.TunnelStatus, pending.tx.Config.WGClient.PersistentKeepalive); err != nil {
				pending.tx.Error = "WireGuard client confirmation failed: " + err.Error()
				return pending.tx, err
			}
		}
		if e.store != nil {
			faultinject.Run(faultinject.PreSQLiteCommit)
			if err := e.store.SaveConfig(pending.tx.Config); err != nil {
				pending.tx.CurrentState = StateRecoveryRequired
				pending.tx.Error = "management-path confirmation succeeded but canonical configuration could not be committed; retry confirmation or allow verified rollback"
				return pending.tx, fmt.Errorf("failed to commit confirmed configuration: %w", err)
			}
			faultinject.Run(faultinject.PostSQLiteCommit)
		}
		pending.canonicalCommitted = true
		e.currentConfig = pending.tx.Config
		if pending.timer != nil {
			pending.timer.Stop()
		}
	}

	pending.commitAttempts++
	commitID := fmt.Sprintf("%s-commit-confirmed-%d", txID, pending.commitAttempts)
	commitReq := ApplyRequest{ID: commitID, Op: OpCommitConfirmed, Revision: pending.tx.Config.Revision, Config: pending.tx.Config, SkipWANVerify: true}
	commitCtx, commitCancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)
	e.applying = true
	e.mu.Unlock()
	commitResp, commitErr := e.applyPrivileged(commitCtx, commitReq)
	e.mu.Lock()
	e.applying = false
	commitCancel()
	if commitErr != nil || !commitResp.Success || !commitResp.Verified {
		pending.tx.CurrentState = StateRecoveryRequired
		if commitErr != nil {
			pending.tx.Error = fmt.Sprintf("canonical configuration was committed but helper last-good acknowledgement is unknown: %v", commitErr)
		} else {
			pending.tx.Error = "canonical configuration was committed but helper last-good acknowledgement failed: " + commitResp.Error
		}
		e.requireRecovery(pending.tx.Error)
		return pending.tx, fmt.Errorf("confirmed helper commit failed")
	}

	lanChanged := pending.previous.LAN.IPAddress != pending.tx.Config.LAN.IPAddress || pending.previous.LAN.CIDR != pending.tx.Config.LAN.CIDR
	if lanChanged {
		finalizeReq, buildErr := buildApplyRequest(txID+"-finalize-lan", pending.tx.Config)
		if buildErr != nil {
			pending.tx.CurrentState = StateRecoveryRequired
			pending.tx.Error = "canonical LAN configuration was committed but final federation request could not be generated: " + buildErr.Error()
			e.requireRecovery(pending.tx.Error)
			return pending.tx, buildErr
		}
		finalizeReq.Op = OpReconcile
		finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)
		e.applying = true
		e.mu.Unlock()
		finalizeResp, finalizeErr := e.applyPrivileged(finalizeCtx, finalizeReq)
		e.mu.Lock()
		e.applying = false
		finalizeCancel()
		if finalizeErr != nil || finalizeResp == nil || !finalizeResp.Success || !finalizeResp.Verified {
			pending.tx.CurrentState = StateRecoveryRequired
			if finalizeErr != nil {
				pending.tx.Error = "canonical LAN configuration was committed but final runtime reconciliation is unknown: " + finalizeErr.Error()
			} else if finalizeResp == nil {
				pending.tx.Error = "canonical LAN configuration was committed but final runtime reconciliation returned no result"
			} else {
				pending.tx.Error = "canonical LAN configuration was committed but final runtime reconciliation failed: " + finalizeResp.Error
			}
			e.requireRecovery(pending.tx.Error)
			return pending.tx, fmt.Errorf("LAN runtime finalization failed")
		}
	}

	now := time.Now()
	pending.tx.ConfirmedAt = &now
	pending.tx.CurrentState = StateCommitted
	e.currentConfig = pending.tx.Config
	e.pending = nil
	e.activeTx = pending.tx
	e.recoveryRequired = false
	e.recoveryReason = ""
	return pending.tx, nil
}

func (e *Engine) rollbackExpired(txID string) {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending == nil || e.pending.tx.ID != txID {
		return
	}
	pending := e.pending
	if pending.canonicalCommitted {
		return
	}
	pending.rollbackAttempts++
	if pending.rollbackAttempts > maximumRollbackAttempts {
		pending.tx.CurrentState = StateRecoveryRequired
		pending.tx.Error = "confirmation deadline expired and automatic rollback attempts were exhausted"
		e.requireRecovery(pending.tx.Error)
		e.pending = nil
		return
	}
	rollbackID := fmt.Sprintf("%s-timeout-rollback-%d", txID, pending.rollbackAttempts)
	req, err := buildApplyRequest(rollbackID, pending.previous)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)
		e.applying = true
		e.mu.Unlock()
		resp, applyErr := e.applyPrivileged(ctx, req)
		e.mu.Lock()
		e.applying = false
		cancel()
		if applyErr == nil && resp.Success && resp.Verified {
			pending.tx.CurrentState = StateRolledBack
			pending.tx.Error = "confirmation deadline expired; previous configuration restored"
			if pending.timer != nil {
				pending.timer.Stop()
			}
			e.pending = nil
			e.recoveryRequired = false
			e.recoveryReason = ""
			return
		}
		pending.tx.CurrentState = StateRecoveryRequired
		pending.tx.Error = "confirmation deadline expired and privileged rollback failed; retry scheduled while candidate access remains available"
	} else {
		pending.tx.CurrentState = StateRecoveryRequired
		pending.tx.Error = "confirmation deadline expired and rollback generation failed; retry scheduled while candidate access remains available"
	}
	delay := rollbackRetryDelay * time.Duration(1<<(pending.rollbackAttempts-1))
	pending.timer = time.AfterFunc(delay, func() { e.rollbackExpired(txID) })
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
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	req, err := buildApplyRequest(fmt.Sprintf("boot-reconcile-%d", time.Now().UnixNano()), e.currentConfig)
	if err != nil {
		return err
	}
	req.Op = OpReconcile
	e.applying = true
	e.mu.Unlock()
	resp, err := e.applyPrivileged(ctx, req)
	e.mu.Lock()
	e.applying = false
	if err != nil {
		reason := fmt.Sprintf("boot reconciliation failed: %v", err)
		e.requireRecovery(reason)
		return fmt.Errorf("%s", reason)
	}
	if !resp.Success || !resp.Verified {
		reason := fmt.Sprintf("boot reconciliation was not verified: %s", resp.Error)
		e.requireRecovery(reason)
		return fmt.Errorf("%s", reason)
	}
	e.recoveryRequired = false
	e.recoveryReason = ""
	return nil
}

// GetCurrentConfig returns a detached deep copy of the canonical in-memory
// configuration. Callers may read and modify the result freely; the canonical
// engine state can only change through a processed transaction.
func (e *Engine) GetCurrentConfig() config.SystemConfig {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.currentConfig.DeepCopy()
}

func (e *Engine) GetStatus() EngineStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	status := EngineStatus{Applying: e.applying, RecoveryRequired: e.recoveryRequired, RecoveryReason: e.recoveryReason}
	if e.activeTx != nil {
		status.ActiveTransactionID = e.activeTx.ID
		status.ActiveState = e.activeTx.CurrentState
	}
	return status
}

func (e *Engine) GetStore() *config.FileStore { return e.store }
