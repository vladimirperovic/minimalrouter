package apply

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestSideEffectFreeHelperRejectionIsNotReportedAsRollback(t *testing.T) {
	initial := config.DefaultConfig()
	candidate := initial
	candidate.System.Hostname = "rejected-candidate"
	client := &scenarioApplyClient{steps: []scenarioApplyStep{{
		response: ApplyResponse{Success: false, Error: "component preflight failed"},
	}}}
	engine := NewEngineWithClient(initial, nil, client)

	tx, err := engine.ProcessTransaction("tx-preflight-rejection", candidate)
	if err == nil {
		t.Fatal("helper rejection must fail")
	}
	if tx.CurrentState != StateRejected {
		t.Fatalf("side-effect-free rejection was reported as %s", tx.CurrentState)
	}
}

func TestMalformedHelperOutcomeRequiresRecoveryAndBlocksChanges(t *testing.T) {
	initial := config.DefaultConfig()
	candidate := initial
	candidate.System.Hostname = "malformed-outcome"
	client := &scenarioApplyClient{steps: []scenarioApplyStep{{
		response: ApplyResponse{Success: true, Verified: false},
	}}}
	engine := NewEngineWithClient(initial, nil, client)

	tx, err := engine.ProcessTransaction("tx-malformed-outcome", candidate)
	if err == nil || tx.CurrentState != StateRecoveryRequired {
		t.Fatalf("malformed privileged outcome must require recovery, state=%s err=%v", tx.CurrentState, err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("semantic corruption must not be retried as a transport error; requests=%d", len(client.requests))
	}

	second := initial
	second.System.Hostname = "must-remain-blocked"
	blocked, blockErr := engine.ProcessTransaction("tx-after-malformed", second)
	if blockErr == nil || blocked.CurrentState != StateRecoveryRequired {
		t.Fatal("new configuration was not blocked after an unknown runtime outcome")
	}
	if len(client.requests) != 1 {
		t.Fatal("blocked transaction reached the privileged helper")
	}
}

func TestAmbiguousOutcomeBlocksChangesUntilCanonicalReconcile(t *testing.T) {
	initial := config.DefaultConfig()
	client := &scenarioApplyClient{steps: []scenarioApplyStep{
		{err: errors.New("response lost")},
		{err: errors.New("helper unavailable")},
	}}
	engine := NewEngineWithClient(initial, nil, client)
	candidate := initial
	candidate.System.Hostname = "unknown-runtime"

	if tx, err := engine.ProcessTransaction("tx-ambiguous-lock", candidate); err == nil || tx.CurrentState != StateRecoveryRequired {
		t.Fatal("ambiguous outcome did not require recovery")
	}
	blocked := initial
	blocked.System.Hostname = "blocked-before-reconcile"
	if tx, err := engine.ProcessTransaction("tx-blocked-before-reconcile", blocked); err == nil || tx.CurrentState != StateRecoveryRequired {
		t.Fatal("configuration was accepted before canonical reconciliation")
	}
	if len(client.requests) != 2 {
		t.Fatalf("blocked transaction reached helper; requests=%d", len(client.requests))
	}

	client.steps = append(client.steps, successfulScenarioStep())
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("canonical reconciliation failed: %v", err)
	}
	client.steps = append(client.steps, successfulScenarioStep())
	after := initial
	after.System.Hostname = "accepted-after-reconcile"
	if tx, err := engine.ProcessTransaction("tx-after-reconcile", after); err != nil || tx.CurrentState != StateCommitted {
		t.Fatalf("configuration remained blocked after reconciliation: state=%s err=%v", tx.CurrentState, err)
	}
}

func TestFailedTimeoutRollbackSurfacesRecoveryRequired(t *testing.T) {
	initial := config.DefaultConfig()
	client := &scenarioApplyClient{steps: []scenarioApplyStep{
		successfulScenarioStep(),
		{response: ApplyResponse{Success: false, Error: "temporary rollback failure"}},
	}}
	engine := NewEngineWithClient(initial, nil, client)
	tx, err := engine.ProcessTransaction("tx-timeout-state", candidateWithNewLAN(initial))
	if err != nil {
		t.Fatalf("create pending change: %v", err)
	}
	engine.pending.timer.Stop()
	engine.rollbackExpired(tx.ID)
	defer func() {
		if engine.pending != nil && engine.pending.timer != nil {
			engine.pending.timer.Stop()
		}
	}()
	pending := engine.GetPendingTransaction()
	if pending == nil || pending.CurrentState != StateRecoveryRequired {
		t.Fatalf("failed rollback must surface recovery-required state: %+v", pending)
	}
	if !strings.Contains(pending.Error, "retry scheduled") {
		t.Fatalf("retry status missing: %q", pending.Error)
	}
}

type closeStoreOnConfirmClient struct {
	store *config.FileStore
	calls int
}

func (c *closeStoreOnConfirmClient) Apply(_ context.Context, req ApplyRequest) (*ApplyResponse, error) {
	c.calls++
	if c.calls == 2 {
		if err := c.store.Close(); err != nil {
			return nil, err
		}
	}
	return &ApplyResponse{ID: req.ID, Success: true, Verified: true}, nil
}

func TestConfirmedRuntimeWithCanonicalStoreFailureRemainsRecoverable(t *testing.T) {
	store := newScenarioStore(t)
	initial, err := store.GetLatestConfig()
	if err != nil {
		t.Fatalf("read initial config: %v", err)
	}
	client := &closeStoreOnConfirmClient{store: store}
	engine := NewEngineWithClient(initial, store, client)
	tx, err := engine.ProcessTransaction("tx-confirm-store-failure", candidateWithNewLAN(initial))
	if err != nil {
		t.Fatalf("create pending change: %v", err)
	}

	confirmed, confirmErr := engine.ConfirmTransaction(tx.ID)
	if confirmErr == nil {
		t.Fatal("closed canonical store must fail confirmation commit")
	}
	if confirmed.CurrentState != StateRecoveryRequired {
		t.Fatalf("confirmation/store split was reported as %s", confirmed.CurrentState)
	}
	if engine.GetPendingTransaction() == nil {
		t.Fatal("pending recovery context was discarded after store failure")
	}

	engine.pending.timer.Stop()
	engine.rollbackExpired(tx.ID)
	if engine.GetPendingTransaction() != nil {
		t.Fatal("verified rollback did not clear pending recovery state")
	}
	if confirmed.CurrentState != StateRolledBack {
		t.Fatalf("verified rollback state=%s", confirmed.CurrentState)
	}
	if engine.GetCurrentConfig().LAN.CIDR != initial.LAN.CIDR {
		t.Fatal("failed canonical commit changed the in-memory canonical configuration")
	}
}
