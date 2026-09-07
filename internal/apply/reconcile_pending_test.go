package apply

import (
	"context"
	"testing"
)

func TestReconcileFinishesPendingAgainstCanonicalState(t *testing.T) {
	for _, committed := range []bool{false, true} {
		name := "unconfirmed rollback"
		if committed {
			name = "committed ack recovery"
		}
		t.Run(name, func(t *testing.T) {
			store := newScenarioStore(t)
			initial, err := store.GetLatestConfig()
			if err != nil {
				t.Fatal(err)
			}
			client := &scenarioApplyClient{}
			engine := NewEngineWithClient(initial, store, client)
			tx, err := engine.ProcessTransaction("pending-reconcile", candidateWithNewLAN(initial))
			if err != nil {
				t.Fatal(err)
			}
			if committed {
				client.steps = []scenarioApplyStep{{response: ApplyResponse{RecoveryRequired: true, Error: "ack persistence failed"}}}
				if _, err := engine.ConfirmTransaction(tx.ID); err == nil {
					t.Fatal("expected failed helper ack")
				}
			}
			// Failed reconciliation must preserve pending state and its recovery
			// target. Only a verified canonical outcome may dispose of it.
			client.steps = []scenarioApplyStep{{response: ApplyResponse{RecoveryRequired: true, Error: "reconcile failed"}}}
			if err := engine.Reconcile(context.Background()); err == nil {
				t.Fatal("accepted failed reconcile")
			}
			if engine.GetPendingTransaction() == nil {
				t.Fatal("failed reconcile discarded pending")
			}
			if err := engine.Reconcile(context.Background()); err != nil {
				t.Fatal(err)
			}
			if engine.GetPendingTransaction() != nil || engine.GetStatus().RecoveryRequired {
				t.Fatal("successful reconcile retained recovery/pending")
			}
			wantState := StateRolledBack
			wantIP := initial.LAN.IPAddress
			if committed {
				wantState = StateCommitted
				wantIP = tx.Config.LAN.IPAddress
			}
			if tx.CurrentState != wantState || engine.GetCurrentConfig().LAN.IPAddress != wantIP {
				t.Fatal("reconciled the wrong canonical state")
			}
			stored, err := store.GetLatestConfig()
			if err != nil || stored.LAN.IPAddress != wantIP {
				t.Fatalf("canonical store diverged: %v", err)
			}
			last := client.requests[len(client.requests)-1]
			if last.Op != OpReconcile || last.Config.LAN.IPAddress != wantIP {
				t.Fatal("helper received wrong recovery target")
			}
			// A late timer callback must not undo the recovered canonical state.
			before := len(client.requests)
			engine.rollbackExpired(tx.ID)
			if len(client.requests) != before {
				t.Fatal("expired callback applied rollback after reconciliation")
			}
			next := engine.GetCurrentConfig()
			next.Accounting.Enabled = !next.Accounting.Enabled
			followup, err := engine.ProcessTransaction("after-reconcile", next)
			if err != nil || followup.CurrentState != StateCommitted {
				t.Fatalf("new transaction remains blocked: %v", err)
			}
		})
	}
}
