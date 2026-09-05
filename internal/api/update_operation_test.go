package api

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testOperationStore(t *testing.T) *updateOperationStore {
	t.Helper()
	dir, err := os.MkdirTemp("", "update-operation-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return newUpdateOperationStore(filepath.Join(dir, "update-operation.json"))
}

func sampleOperation(id string, now time.Time) UpdateOperation {
	return UpdateOperation{
		ID: id, State: UpdateQueued, FromVersion: "0.1.7", TargetVersion: "0.1.8",
		CandidateID: "candidate", Source: "published_release", StartedAt: now,
	}
}

func TestOnlyOneUpdateRunsAtATime(t *testing.T) {
	store := testOperationStore(t)
	now := time.Now()
	if _, err := store.Begin(sampleOperation("upd-1", now)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Begin(sampleOperation("upd-2", now)); !errors.Is(err, errUpdateInProgress) {
		t.Fatalf("second update err = %v, want errUpdateInProgress", err)
	}
	if err := store.Finish("upd-1", UpdateSucceeded, "", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Begin(sampleOperation("upd-2", now)); err != nil {
		t.Fatalf("a new update must be allowed once the previous one ended: %v", err)
	}
}

// A lost response must not install twice: retrying with the same key returns
// the operation that is already running rather than starting another.
func TestRetryWithTheSameIdempotencyKeyReturnsTheSameOperation(t *testing.T) {
	store := testOperationStore(t)
	now := time.Now()
	first := sampleOperation("upd-1", now)
	first.IdempotencyKey = "key-a"
	accepted, err := store.Begin(first)
	if err != nil {
		t.Fatal(err)
	}

	retry := sampleOperation("upd-2", now)
	retry.IdempotencyKey = "key-a"
	again, err := store.Begin(retry)
	if err != nil {
		t.Fatalf("a retry under the same key must not be refused: %v", err)
	}
	if again.ID != accepted.ID {
		t.Fatalf("retry started a second operation %s, want %s", again.ID, accepted.ID)
	}

	// The same holds after the work finished: the caller gets the outcome, not
	// a fresh install.
	if err := store.Finish(accepted.ID, UpdateSucceeded, "", "", now); err != nil {
		t.Fatal(err)
	}
	third, err := store.Begin(retry)
	if err != nil {
		t.Fatal(err)
	}
	if third.ID != accepted.ID || third.State != UpdateSucceeded {
		t.Fatalf("completed operation replay = %+v, want the original outcome", third)
	}
}

func TestOperationSurvivesProcessRestart(t *testing.T) {
	store := testOperationStore(t)
	now := time.Now()
	if _, err := store.Begin(sampleOperation("upd-1", now)); err != nil {
		t.Fatal(err)
	}
	if err := store.Advance("upd-1", UpdateDownloading, now); err != nil {
		t.Fatal(err)
	}

	// A new process reads the same file.
	restarted := newUpdateOperationStore(store.path)
	operation, err := restarted.Current()
	if err != nil {
		t.Fatal(err)
	}
	if operation == nil || operation.State != UpdateDownloading {
		t.Fatalf("operation after restart = %+v, want the persisted downloading state", operation)
	}
}

func TestInterruptedDownloadFailsSafelyAndInterruptedActivationDoesNot(t *testing.T) {
	now := time.Now()

	before := testOperationStore(t)
	if _, err := before.Begin(sampleOperation("upd-1", now)); err != nil {
		t.Fatal(err)
	}
	if err := before.Advance("upd-1", UpdateDownloading, now); err != nil {
		t.Fatal(err)
	}
	recovered, err := before.RecoverInterrupted(now)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != UpdateFailed {
		t.Fatalf("interrupted download = %s, want failed: nothing was installed", recovered.State)
	}

	after := testOperationStore(t)
	if _, err := after.Begin(sampleOperation("upd-2", now)); err != nil {
		t.Fatal(err)
	}
	if err := after.Advance("upd-2", UpdateActivating, now); err != nil {
		t.Fatal(err)
	}
	recovered, err = after.RecoverInterrupted(now)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != UpdateRecoveryRequired {
		t.Fatalf("interrupted activation = %s, want recovery_required: the slot pointer may already have moved", recovered.State)
	}
	if recovered.ErrorCode != "interrupted_during_activation" {
		t.Fatalf("error code = %q", recovered.ErrorCode)
	}
}

func TestTerminalOperationIsNotDisturbedByRecovery(t *testing.T) {
	store := testOperationStore(t)
	now := time.Now()
	if _, err := store.Begin(sampleOperation("upd-1", now)); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish("upd-1", UpdateSucceeded, "", "", now); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.RecoverInterrupted(now)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != nil {
		t.Fatalf("a finished operation must not be rewritten by recovery, got %+v", recovered)
	}
}

func TestUnreadableOperationRecordDoesNotSilentlyUnlockUpdates(t *testing.T) {
	store := testOperationStore(t)
	if err := os.MkdirAll(filepath.Dir(store.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Current(); err == nil {
		t.Fatal("a corrupt operation record must be reported, not treated as 'no update running'")
	}
}
