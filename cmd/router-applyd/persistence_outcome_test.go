package main

import (
	"errors"
	"os"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestReplayTransactionResponseFailsClosedOnUnreadableJournal(t *testing.T) {
	response, handled := replayTransactionResponse("tx-corrupt", "hash", nil, errors.New("corrupt JSON"))
	if !handled || response == nil || !response.RecoveryRequired {
		t.Fatalf("unreadable journal did not require recovery: handled=%v response=%+v", handled, response)
	}
}

func TestReplayTransactionResponseAllowsMissingJournal(t *testing.T) {
	response, handled := replayTransactionResponse("tx-new", "hash", nil, os.ErrNotExist)
	if handled || response != nil {
		t.Fatalf("missing journal should allow a new request: handled=%v response=%+v", handled, response)
	}
}

func TestReplayTransactionResponseReturnsPersistedResult(t *testing.T) {
	stored := &transactionRecord{
		ID: "tx-existing", ConfigHash: "same",
		Response: apply.ApplyResponse{ID: "tx-existing", Success: true, Verified: true},
	}
	response, handled := replayTransactionResponse("tx-existing", "same", stored, nil)
	if !handled || response == nil || !response.Success || !response.Verified {
		t.Fatalf("persisted response was not replayed: handled=%v response=%+v", handled, response)
	}
}

func TestNormalizeLastGoodRejectsCorruptionButAllowsFreshInstall(t *testing.T) {
	if previous, err := normalizeLastGood(nil, os.ErrNotExist); err != nil || previous != nil {
		t.Fatalf("fresh install was rejected: previous=%+v err=%v", previous, err)
	}
	if _, err := normalizeLastGood(nil, errors.New("invalid last-good JSON")); err == nil {
		t.Fatal("corrupt last-good state was accepted")
	}
	cfg := config.DefaultConfig()
	if previous, err := normalizeLastGood(&cfg, nil); err != nil || previous == nil {
		t.Fatalf("valid last-good state was rejected: %v", err)
	}
}

func TestPendingLoadFailureDistinguishesMissingFromCorrupt(t *testing.T) {
	missing := pendingLoadFailure("tx-missing", os.ErrNotExist)
	if missing.RecoveryRequired || missing.RolledBack || missing.Success {
		t.Fatalf("missing pending state should be an ordinary rejection: %+v", missing)
	}
	corrupt := pendingLoadFailure("tx-corrupt", errors.New("corrupt pending JSON"))
	if !corrupt.RecoveryRequired || corrupt.RolledBack || corrupt.Success {
		t.Fatalf("corrupt pending state must require recovery: %+v", corrupt)
	}
}
