package main

import (
	"errors"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
)

func TestUnresolvedPreviousTransactionBlocksNewMutationButAllowsReconcile(t *testing.T) {
	record := validTransactionRecordForTest()
	record.Response = apply.ApplyResponse{}
	record.CompletedAt = time.Time{}

	blocked, handled := replayTransactionResponseWithOverride("tx-new", "new-hash", &record, nil, false)
	if !handled || blocked == nil || !blocked.RecoveryRequired {
		t.Fatalf("new mutation was not blocked: handled=%v response=%+v", handled, blocked)
	}
	allowed, handled := replayTransactionResponseWithOverride("boot-reconcile-new", "canonical-hash", &record, nil, true)
	if handled || allowed != nil {
		t.Fatalf("canonical reconcile was blocked: handled=%v response=%+v", handled, allowed)
	}
}

func TestCorruptJournalBlocksMutationButAllowsCanonicalReconcile(t *testing.T) {
	blocked, handled := replayTransactionResponseWithOverride("tx-new", "hash", nil, errors.New("corrupt journal"), false)
	if !handled || blocked == nil || !blocked.RecoveryRequired {
		t.Fatalf("corrupt journal did not block mutation: handled=%v response=%+v", handled, blocked)
	}
	allowed, handled := replayTransactionResponseWithOverride("boot-reconcile-new", "hash", nil, errors.New("corrupt journal"), true)
	if handled || allowed != nil {
		t.Fatalf("canonical reconcile could not replace corrupt journal: handled=%v response=%+v", handled, allowed)
	}
}

func TestCanonicalReconcileDoesNotRequireWANAvailability(t *testing.T) {
	if requireWANVerification(apply.OpReconcile) {
		t.Fatal("boot reconciliation must keep the LAN management plane available during an ISP outage")
	}
	for _, op := range []apply.OperationType{apply.OpApplyAll, apply.OpConfirm, apply.OpCommitConfirmed} {
		if !requireWANVerification(op) {
			t.Fatalf("operation %q unexpectedly skipped WAN verification", op)
		}
	}
}
