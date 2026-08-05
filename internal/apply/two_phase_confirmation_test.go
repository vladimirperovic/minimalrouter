package apply

import (
	"context"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

type confirmationOrderingClient struct {
	store     *config.FileStore
	initial   config.SystemConfig
	candidate config.SystemConfig
	requests  []ApplyRequest
}

func (c *confirmationOrderingClient) Apply(_ context.Context, req ApplyRequest) (*ApplyResponse, error) {
	c.requests = append(c.requests, req)
	stored, err := c.store.GetLatestConfig()
	if err != nil {
		return nil, err
	}
	switch req.Op {
	case OpApplyAll:
		if stored.LAN.CIDR != c.initial.LAN.CIDR {
			return &ApplyResponse{ID: req.ID, Success: false, Error: "canonical store changed before candidate verification completed"}, nil
		}
	case OpCommitConfirmed, OpReconcile:
		if stored.LAN.CIDR != c.candidate.LAN.CIDR {
			return &ApplyResponse{ID: req.ID, Success: false, Error: "helper canonical/finalize operation ran before canonical store commit"}, nil
		}
		if req.Op == OpCommitConfirmed && !req.SkipWANVerify {
			return &ApplyResponse{ID: req.ID, Success: false, Error: "confirmed canonical ack must not depend on WAN availability"}, nil
		}
	}
	return &ApplyResponse{ID: req.ID, Success: true, Verified: true}, nil
}

func TestConfirmationCommitsCanonicalStoreBeforeHelperLastGood(t *testing.T) {
	store := newScenarioStore(t)
	initial, err := store.GetLatestConfig()
	if err != nil {
		t.Fatalf("read initial config: %v", err)
	}
	candidate := candidateWithNewLAN(initial)
	client := &confirmationOrderingClient{store: store, initial: initial, candidate: candidate}
	engine := NewEngineWithClient(initial, store, client)

	tx, err := engine.ProcessTransaction("tx-two-phase-order", candidate)
	if err != nil {
		t.Fatalf("create pending transaction: %v", err)
	}
	confirmed, err := engine.ConfirmTransaction(tx.ID)
	if err != nil {
		t.Fatalf("confirm transaction: %v", err)
	}
	if confirmed.CurrentState != StateCommitted {
		t.Fatalf("confirmation state=%s", confirmed.CurrentState)
	}
	// The API request itself proves the candidate management path. The helper
	// no longer runs a monolithic OpConfirm that couples LAN/Wi-Fi confirmation
	// to unrelated ISP or wg1 health. Canonical commit is acknowledged first;
	// a best-effort reconcile then collapses the provisional dual-LAN address.
	want := []OperationType{OpApplyAll, OpCommitConfirmed, OpReconcile}
	if len(client.requests) != len(want) {
		t.Fatalf("request count=%d, want %d", len(client.requests), len(want))
	}
	for i, op := range want {
		if client.requests[i].Op != op {
			t.Fatalf("request %d op=%s, want %s", i, client.requests[i].Op, op)
		}
	}
	if client.requests[1].ID != tx.ID+"-commit-confirmed-1" {
		t.Fatalf("first confirmed commit ID=%q", client.requests[1].ID)
	}
}

func TestHelperCommitFailureRetriesCommitWithoutRepeatingPathProof(t *testing.T) {
	store := newScenarioStore(t)
	initial, err := store.GetLatestConfig()
	if err != nil {
		t.Fatalf("read initial config: %v", err)
	}
	client := &scenarioApplyClient{steps: []scenarioApplyStep{
		successfulScenarioStep(),
		{response: ApplyResponse{Success: false, RecoveryRequired: true, Error: "last-good storage unavailable"}},
		successfulScenarioStep(),
		successfulScenarioStep(), // post-commit LAN finalize reconcile
	}}
	engine := NewEngineWithClient(initial, store, client)
	tx, err := engine.ProcessTransaction("tx-two-phase-retry", candidateWithNewLAN(initial))
	if err != nil {
		t.Fatalf("create pending transaction: %v", err)
	}
	if _, err := engine.ConfirmTransaction(tx.ID); err == nil {
		t.Fatal("helper commit failure was accepted")
	}
	pending := engine.GetPendingTransaction()
	if pending == nil || pending.CurrentState != StateRecoveryRequired {
		t.Fatalf("helper commit failure lost pending recovery: %+v", pending)
	}
	confirmed, err := engine.ConfirmTransaction(tx.ID)
	if err != nil {
		t.Fatalf("retry helper commit: %v", err)
	}
	if confirmed.CurrentState != StateCommitted {
		t.Fatalf("retry state=%s", confirmed.CurrentState)
	}
	want := []OperationType{OpApplyAll, OpCommitConfirmed, OpCommitConfirmed, OpReconcile}
	if len(client.requests) != len(want) {
		t.Fatalf("request count=%d, want %d", len(client.requests), len(want))
	}
	for i, op := range want {
		if client.requests[i].Op != op {
			t.Fatalf("request %d op=%s, want %s", i, client.requests[i].Op, op)
		}
	}
	if client.requests[1].ID == client.requests[2].ID {
		t.Fatalf("explicit confirmed-commit retry reused cached transaction ID %q", client.requests[1].ID)
	}
	if client.requests[1].ID != tx.ID+"-commit-confirmed-1" || client.requests[2].ID != tx.ID+"-commit-confirmed-2" {
		t.Fatalf("unexpected confirmed-commit retry IDs: %q, %q", client.requests[1].ID, client.requests[2].ID)
	}
}

type routineCommitOrderingClient struct {
	store     *config.FileStore
	initial   config.SystemConfig
	candidate config.SystemConfig
	requests  []ApplyRequest
	setupMode bool
}

func (c *routineCommitOrderingClient) Apply(_ context.Context, req ApplyRequest) (*ApplyResponse, error) {
	c.requests = append(c.requests, req)
	stored, err := c.store.GetLatestConfig()
	if err != nil {
		return nil, err
	}
	switch req.Op {
	case OpApplyAll:
		if c.setupMode {
			if req.DeferLastGood {
				return &ApplyResponse{ID: req.ID, Success: false, Error: "setup must not defer last-good"}, nil
			}
			return &ApplyResponse{ID: req.ID, Success: true, Verified: true}, nil
		}
		if !req.DeferLastGood {
			return &ApplyResponse{ID: req.ID, Success: false, Error: "routine apply must defer last-good until canonical commit"}, nil
		}
		if stored.LAN.CIDR != c.initial.LAN.CIDR {
			return &ApplyResponse{ID: req.ID, Success: false, Error: "canonical store changed before helper apply completed"}, nil
		}
	case OpCommitConfirmed:
		if !req.SkipWANVerify {
			return &ApplyResponse{ID: req.ID, Success: false, Error: "auto commit must not require WAN availability"}, nil
		}
		if stored.LAN.CIDR != c.candidate.LAN.CIDR {
			return &ApplyResponse{ID: req.ID, Success: false, Error: "helper last-good ran before canonical store commit"}, nil
		}
	}
	return &ApplyResponse{ID: req.ID, Success: true, Verified: true}, nil
}

func candidateWithRoutineChange(initial config.SystemConfig) config.SystemConfig {
	candidate := initial.DeepCopy()
	candidate.DHCP.DNSServers = []string{"9.9.9.9", "149.112.112.112"}
	return candidate
}

func TestRoutineSaveCommitsCanonicalStoreBeforeHelperLastGood(t *testing.T) {
	store := newScenarioStore(t)
	initial, err := store.GetLatestConfig()
	if err != nil {
		t.Fatalf("read initial config: %v", err)
	}
	candidate := candidateWithRoutineChange(initial)
	client := &routineCommitOrderingClient{store: store, initial: initial, candidate: candidate}
	engine := NewEngineWithClient(initial, store, client)

	if requiresConfirmation(initial, candidate) {
		t.Fatal("test candidate must not require user confirmation")
	}
	tx, err := engine.ProcessTransaction("tx-routine-two-phase", candidate)
	if err != nil {
		t.Fatalf("process transaction: %v", err)
	}
	if tx.CurrentState != StateCommitted {
		t.Fatalf("state=%s, error=%s", tx.CurrentState, tx.Error)
	}
	want := []OperationType{OpApplyAll, OpCommitConfirmed}
	if len(client.requests) != len(want) {
		t.Fatalf("request count=%d, want %d", len(client.requests), len(want))
	}
	for i, op := range want {
		if client.requests[i].Op != op {
			t.Fatalf("request[%d].Op=%s, want %s", i, client.requests[i].Op, op)
		}
	}
}

func TestInitialSetupDoesNotDeferLastGood(t *testing.T) {
	store := newScenarioStore(t)
	initial, err := store.GetLatestConfig()
	if err != nil {
		t.Fatalf("read initial config: %v", err)
	}
	client := &routineCommitOrderingClient{store: store, initial: initial, setupMode: true}
	engine := NewEngineWithClient(initial, store, client)

	_, err = engine.ProcessInitialSetup("tx-setup", initial, func(config.SystemConfig) error { return nil })
	if err != nil {
		t.Fatalf("initial setup: %v", err)
	}
	if len(client.requests) != 1 || client.requests[0].Op != OpApplyAll {
		t.Fatalf("setup must stay single-phase, got %d requests", len(client.requests))
	}
	if client.requests[0].DeferLastGood {
		t.Fatal("initial setup must not defer last-good: no canonical state exists to protect and a pending marker on a fresh install would fail-closed the next boot")
	}
}
