package main

import (
	"errors"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
)

func TestJournalPersistenceFailureAlwaysRequiresRecovery(t *testing.T) {
	tests := []struct {
		name     string
		previous apply.ApplyResponse
	}{
		{name: "successful apply", previous: apply.ApplyResponse{Success: true, Verified: true}},
		{name: "side-effect-free rejection", previous: apply.ApplyResponse{Success: false}},
		{name: "verified rollback", previous: apply.ApplyResponse{Success: false, RolledBack: true}},
		{name: "already recovery required", previous: apply.ApplyResponse{Success: false, RecoveryRequired: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := journalPersistenceFailure("tx-journal", test.previous)
			if got.RolledBack || !got.RecoveryRequired {
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

func TestPersistTransactionOutcomeCachesRecoveryWhenDiskJournalFails(t *testing.T) {
	record := validTransactionRecordForTest()
	stored, response := persistTransactionOutcome(record, func(transactionRecord) error {
		return errors.New("disk full")
	})
	if !response.RecoveryRequired || response.RolledBack || response.Success {
		t.Fatalf("journal failure response=%+v", response)
	}
	if !stored.Response.RecoveryRequired {
		t.Fatal("in-memory record did not retain recovery-required result")
	}
	replayed, handled := replayTransactionResponse(stored.ID, stored.ConfigHash, &stored, nil)
	if !handled || replayed == nil || !replayed.RecoveryRequired {
		t.Fatalf("in-memory recovery result was not replayed: handled=%v response=%+v", handled, replayed)
	}
}
