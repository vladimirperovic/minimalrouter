package apply

import (
	"context"
	"errors"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestAmbiguousOutcomeReportsRecoveryRequired(t *testing.T) {
	initial := config.DefaultConfig()
	candidate := initial
	candidate.System.Hostname = "unknown-runtime-outcome"
	client := &scenarioApplyClient{steps: []scenarioApplyStep{
		{err: errors.New("response lost")},
		{err: errors.New("helper still unavailable")},
	}}
	engine := NewEngineWithClient(initial, nil, client)

	tx, err := engine.ProcessTransaction("tx-unknown-outcome", candidate)
	if err == nil {
		t.Fatal("an unresolved privileged outcome must fail")
	}
	if tx.CurrentState != StateRecoveryRequired {
		t.Fatalf("expected recovery-required state, got %s", tx.CurrentState)
	}
	if engine.GetCurrentConfig().System.Hostname != initial.System.Hostname {
		t.Fatal("an unresolved outcome must not become canonical")
	}
}

func TestHelperRecoveryRequiredResponseIsNotReportedAsRolledBack(t *testing.T) {
	initial := config.DefaultConfig()
	candidate := initial
	candidate.System.Hostname = "rollback-not-verified"
	client := &scenarioApplyClient{steps: []scenarioApplyStep{{
		response: ApplyResponse{
			Success:          false,
			Verified:         false,
			RecoveryRequired: true,
			Error:            "apply failed and rollback could not be verified",
		},
	}}}
	engine := NewEngineWithClient(initial, nil, client)

	tx, err := engine.ProcessTransaction("tx-helper-recovery", candidate)
	if err == nil {
		t.Fatal("recovery-required helper response must fail")
	}
	if tx.CurrentState != StateRecoveryRequired {
		t.Fatalf("expected recovery-required state, got %s", tx.CurrentState)
	}
}

type storeFailureClient struct {
	store           *config.FileStore
	calls           int
	rollbackSuccess bool
}

func (c *storeFailureClient) Apply(_ context.Context, req ApplyRequest) (*ApplyResponse, error) {
	c.calls++
	if c.calls == 1 {
		if err := c.store.Close(); err != nil {
			return nil, err
		}
		return &ApplyResponse{ID: req.ID, Success: true, Verified: true}, nil
	}
	if c.rollbackSuccess {
		return &ApplyResponse{ID: req.ID, Success: true, Verified: true}, nil
	}
	return &ApplyResponse{
		ID:               req.ID,
		Success:          false,
		Verified:         false,
		RecoveryRequired: true,
		Error:            "rollback helper could not restore runtime",
	}, nil
}

func TestConfigStoreFailureWithVerifiedRollbackReportsRolledBack(t *testing.T) {
	store := newScenarioStore(t)
	initial, err := store.GetLatestConfig()
	if err != nil {
		t.Fatalf("read initial config: %v", err)
	}
	candidate := initial
	candidate.System.Hostname = "store-write-fails"
	client := &storeFailureClient{store: store, rollbackSuccess: true}
	engine := NewEngineWithClient(initial, store, client)

	tx, err := engine.ProcessTransaction("tx-store-failure-rollback-ok", candidate)
	if err == nil {
		t.Fatal("closed configuration store must fail commit")
	}
	if tx.CurrentState != StateRolledBack {
		t.Fatalf("verified rollback should report rolled back, got %s", tx.CurrentState)
	}
	if client.calls != 2 {
		t.Fatalf("expected apply and rollback calls, got %d", client.calls)
	}
}

func TestConfigStoreFailureWithUnverifiedRollbackRequiresRecovery(t *testing.T) {
	store := newScenarioStore(t)
	initial, err := store.GetLatestConfig()
	if err != nil {
		t.Fatalf("read initial config: %v", err)
	}
	candidate := initial
	candidate.System.Hostname = "store-write-and-rollback-fail"
	client := &storeFailureClient{store: store, rollbackSuccess: false}
	engine := NewEngineWithClient(initial, store, client)

	tx, err := engine.ProcessTransaction("tx-store-failure-recovery", candidate)
	if err == nil {
		t.Fatal("closed configuration store must fail commit")
	}
	if tx.CurrentState != StateRecoveryRequired {
		t.Fatalf("unverified rollback should require recovery, got %s", tx.CurrentState)
	}
	if client.calls != 2 {
		t.Fatalf("expected apply and rollback calls, got %d", client.calls)
	}
}
