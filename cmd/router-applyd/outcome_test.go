package main

import (
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
)

func TestJournalPersistenceFailureNeverClaimsUnknownRuntimeRolledBack(t *testing.T) {
	tests := []struct {
		name           string
		previous       apply.ApplyResponse
		wantRolledBack bool
		wantRecovery   bool
	}{
		{name: "successful apply", previous: apply.ApplyResponse{Success: true, Verified: true}, wantRecovery: true},
		{name: "side-effect-free rejection", previous: apply.ApplyResponse{Success: false}, wantRecovery: true},
		{name: "verified rollback", previous: apply.ApplyResponse{Success: false, RolledBack: true}, wantRolledBack: true},
		{name: "already recovery required", previous: apply.ApplyResponse{Success: false, RecoveryRequired: true}, wantRecovery: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := journalPersistenceFailure("tx-journal", test.previous)
			if got.RolledBack != test.wantRolledBack || got.RecoveryRequired != test.wantRecovery {
				t.Fatalf("outcome rolled_back=%v recovery_required=%v", got.RolledBack, got.RecoveryRequired)
			}
			if got.Success || got.Verified {
				t.Fatal("journal failure was reported as verified success")
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("journal failure produced invalid response: %v", err)
			}
		})
	}
}
