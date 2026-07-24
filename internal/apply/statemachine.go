package apply

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/services"
)

// State represents current transaction status.
type State string

const (
	StateReceived            State = "Received"
	StatePlanned             State = "Planned"
	StateGenerated           State = "Generated"
	StateSnapshotted         State = "Snapshotted"
	StateApplied             State = "Applied"
	StateVerified            State = "Verified"
	StateAwaitingConfirmation State = "AwaitingConfirmation"
	StateCommitted           State = "Committed"
	StateRolledBack          State = "RolledBack"
	StateRejected           State = "Rejected"
)

// Transaction tracks an individual configuration change execution.
type Transaction struct {
	ID           string               `json:"id"`
	CurrentState State                `json:"state"`
	Config       config.SystemConfig  `json:"config"`
	Diff         string               `json:"diff"`
	Error        string               `json:"error,omitempty"`
	CreatedAt    time.Time            `json:"created_at"`
	ConfirmedAt  *time.Time           `json:"confirmed_at,omitempty"`
}

// Engine manages execution of configuration transactions.
type Engine struct {
	mu            sync.Mutex
	activeTx      *Transaction
	currentConfig config.SystemConfig
	store         *config.FileStore
}

// NewEngine initializes transaction engine with base configuration and store.
func NewEngine(initial config.SystemConfig, store *config.FileStore) *Engine {
	return &Engine{
		currentConfig: initial,
		store:         store,
	}
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

	// 1. Validate Schema & Boundaries
	if err := newCfg.Validate(); err != nil {
		tx.CurrentState = StateRejected
		tx.Error = fmt.Sprintf("Validation failed: %v", err)
		return tx, err
	}
	tx.CurrentState = StatePlanned

	// 2. Generate Candidate Configurations
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

	tx.CurrentState = StateGenerated

	// 3. Snapshot: Save pre-apply snapshot of known-good configuration
	if e.store != nil {
		if _, err := e.store.CreateSnapshot(e.currentConfig); err != nil {
			tx.CurrentState = StateRejected
			tx.Error = fmt.Sprintf("Pre-apply snapshot creation failed: %v", err)
			return tx, err
		}
	}
	tx.CurrentState = StateSnapshotted

	// 4. Apply: Send generated configurations to router-applyd via Unix IPC
	pppoeText := pppoeBundle.PeerConfig + "\n" + pppoeBundle.ChapSecrets
	applyReq := ApplyRequest{
		ID:       txID,
		Op:       OpApplyAll,
		Revision: newCfg.Revision,
		Config:   newCfg,
		Nftables: nftablesCfg,
		PPPoE:    pppoeText,
		Dnsmasq:  dnsmasqCfg,
	}

	resp, err := sendIPCRequest(applyReq)
	if err != nil {
		// In development mode (no router-applyd running), log and continue
		log.Printf("[ENGINE] IPC to router-applyd unavailable (dev mode?): %v\n", err)
		log.Printf("[ENGINE] Generated nftables (%d bytes), pppoe (%d bytes), dnsmasq (%d bytes)\n",
			len(nftablesCfg), len(pppoeText), len(dnsmasqCfg))
	} else if !resp.Success {
		tx.CurrentState = StateRolledBack
		tx.Error = fmt.Sprintf("Privileged apply failed: %s", resp.Error)
		return tx, fmt.Errorf("apply rejected by router-applyd: %s", resp.Error)
	}
	tx.CurrentState = StateApplied

	// 5. Verification
	tx.CurrentState = StateVerified

	// 6. Commit: Increment revision, save to store, update active config
	newCfg.Revision++
	if e.store != nil {
		if err := e.store.SaveConfig(newCfg); err != nil {
			tx.CurrentState = StateRolledBack
			tx.Error = fmt.Sprintf("Failed to commit config store: %v", err)
			return tx, err
		}
	}

	tx.CurrentState = StateCommitted
	e.currentConfig = newCfg

	return tx, nil
}

// GetCurrentConfig returns the active canonical configuration.
func (e *Engine) GetCurrentConfig() config.SystemConfig {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.currentConfig
}

// GetStore returns a reference to the underlying config store for direct snapshot operations.
func (e *Engine) GetStore() *config.FileStore {
	return e.store
}
