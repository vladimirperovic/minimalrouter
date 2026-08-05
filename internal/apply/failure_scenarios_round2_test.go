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

func TestConfirmedPathWithCanonicalStoreFailureRemainsRecoverable(t *testing.T) {
	store := newScenarioStore(t)
	initial, err := store.GetLatestConfig()
	if err != nil {
		t.Fatalf("read initial config: %v", err)
	}
	// First helper call applies the provisional candidate. The second helper
	// call is reserved for the verified rollback after the SQLite commit below
	// is deliberately made unavailable.
	client := &scenarioApplyClient{steps: []scenarioApplyStep{
		successfulScenarioStep(),
		successfulScenarioStep(),
	}}
	engine := NewEngineWithClient(initial, store, client)
	tx, err := engine.ProcessTransaction("tx-confirm-store-failure", candidateWithNewLAN(initial))
	if err != nil {
		t.Fatalf("create pending change: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close canonical store: %v", err)
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
	if len(client.requests) != 1 {
		t.Fatalf("helper canonical ack ran despite failed SQLite commit; requests=%d", len(client.requests))
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

func TestConfirmationHelperFailureSurfacesRecoveryRequired(t *testing.T) {
	initial := config.DefaultConfig()
	client := &scenarioApplyClient{steps: []scenarioApplyStep{
		successfulScenarioStep(),
		{response: ApplyResponse{Success: false, RecoveryRequired: true, Error: "pending state is corrupt"}},
	}}
	engine := NewEngineWithClient(initial, nil, client)
	tx, err := engine.ProcessTransaction("tx-confirm-helper-failure", candidateWithNewLAN(initial))
	if err != nil {
		t.Fatalf("create pending transaction: %v", err)
	}
	defer func() {
		if engine.pending != nil && engine.pending.timer != nil {
			engine.pending.timer.Stop()
		}
	}()
	confirmed, confirmErr := engine.ConfirmTransaction(tx.ID)
	if confirmErr == nil {
		t.Fatal("canonical helper acknowledgement failure was accepted")
	}
	if confirmed.CurrentState != StateRecoveryRequired {
		t.Fatalf("confirmation failure state=%s", confirmed.CurrentState)
	}
	if engine.GetPendingTransaction() == nil {
		t.Fatal("confirmation recovery context was discarded")
	}
}
