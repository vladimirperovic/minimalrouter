package main

import (
	"errors"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
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

func TestWANVerificationDependsOnOperationAndWANChange(t *testing.T) {
	previous := config.DefaultConfig()
	previous.WAN.Enabled = true
	candidate := previous.DeepCopy()

	for _, op := range []apply.OperationType{apply.OpReconcile, apply.OpConfirm, apply.OpCommitConfirmed} {
		if verificationPlan(op, &previous, candidate).WAN {
			t.Fatalf("operation %q must not depend on live ISP availability", op)
		}
	}
	if verificationPlan(apply.OpApplyAll, &previous, candidate).WAN {
		t.Fatal("unrelated save unexpectedly requires live PPPoE verification")
	}

	candidate.WAN.Username = "replacement-user"
	if !verificationPlan(apply.OpApplyAll, &previous, candidate).WAN {
		t.Fatal("WAN credential change must require live PPPoE verification")
	}
}
