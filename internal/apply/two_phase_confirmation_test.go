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
	case OpApplyAll, OpConfirm:
		if stored.LAN.CIDR != c.initial.LAN.CIDR {
			testingError := "canonical store changed before runtime confirmation completed"
			return &ApplyResponse{ID: req.ID, Success: false, Error: testingError}, nil
		}
	case OpCommitConfirmed:
		if stored.LAN.CIDR != c.candidate.LAN.CIDR {
			return &ApplyResponse{ID: req.ID, Success: false, Error: "helper commit ran before canonical store commit"}, nil
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
	want := []OperationType{OpApplyAll, OpConfirm, OpCommitConfirmed}
	if len(client.requests) != len(want) {
		t.Fatalf("request count=%d, want %d", len(client.requests), len(want))
	}
	for i, op := range want {
		if client.requests[i].Op != op {
			t.Fatalf("request %d op=%s, want %s", i, client.requests[i].Op, op)
		}
	}
	if client.requests[2].ID != tx.ID+"-commit-confirmed-1" {
		t.Fatalf("first confirmed commit ID=%q", client.requests[2].ID)
	}
}

func TestHelperCommitFailureRetriesCommitWithoutRepeatingRuntimeConfirmation(t *testing.T) {
	store := newScenarioStore(t)
	initial, err := store.GetLatestConfig()
	if err != nil {
		t.Fatalf("read initial config: %v", err)
	}
	client := &scenarioApplyClient{steps: []scenarioApplyStep{
		successfulScenarioStep(),
		successfulScenarioStep(),
		{response: ApplyResponse{Success: false, RecoveryRequired: true, Error: "last-good storage unavailable"}},
		successfulScenarioStep(),
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
	want := []OperationType{OpApplyAll, OpConfirm, OpCommitConfirmed, OpCommitConfirmed}
	if len(client.requests) != len(want) {
		t.Fatalf("request count=%d, want %d", len(client.requests), len(want))
	}
	for i, op := range want {
		if client.requests[i].Op != op {
			t.Fatalf("request %d op=%s, want %s", i, client.requests[i].Op, op)
		}
	}
	if client.requests[2].ID == client.requests[3].ID {
		t.Fatalf("explicit confirmed-commit retry reused cached transaction ID %q", client.requests[2].ID)
	}
	if client.requests[2].ID != tx.ID+"-commit-confirmed-1" || client.requests[3].ID != tx.ID+"-commit-confirmed-2" {
		t.Fatalf("unexpected confirmed-commit retry IDs: %q, %q", client.requests[2].ID, client.requests[3].ID)
	}
}
