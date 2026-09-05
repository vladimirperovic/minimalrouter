package apply

import (
	"context"
	"os"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// failCommitAckClient applies successfully but never acknowledges the
// canonical last-good commit, reproducing a helper that dies, restarts, or
// loses its reply between the SQLite commit and the acknowledgement.
type failCommitAckClient struct {
	requests []ApplyRequest
}

func (c *failCommitAckClient) Apply(_ context.Context, req ApplyRequest) (*ApplyResponse, error) {
	c.requests = append(c.requests, req)
	if req.Op == OpCommitConfirmed {
		return &ApplyResponse{ID: req.ID, Success: false, Error: "helper acknowledgement failed"}, nil
	}
	return &ApplyResponse{ID: req.ID, Success: true, Verified: true}, nil
}

// After a durable commit, the in-memory canon must equal the persisted canon
// even when the helper acknowledgement fails. Otherwise the recovery reconcile
// that this failure triggers rebuilds its request from the pre-transaction
// configuration and reverts a change that is already committed and running.
func TestFailedLastGoodAckKeepsCanonicalConfigInSyncWithStore(t *testing.T) {
	// os.MkdirTemp rather than t.TempDir: the store keeps the SQLite file open
	// for the life of the test, and t.TempDir's cleanup treats a still-open
	// file as a failure on platforms that refuse to unlink it.
	tempDir, err := os.MkdirTemp("", "canonical-divergence-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	store, err := config.NewStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	initialCfg := config.DefaultConfig()
	client := &failCommitAckClient{}
	engine := NewEngineWithClient(initialCfg, store, client)

	newCfg := initialCfg
	newCfg.System.Hostname = "audit-candidate"

	tx, err := engine.ProcessTransaction("tx-ack-failure", newCfg)
	if err == nil {
		t.Fatal("a failed last-good acknowledgement must be reported as an error")
	}
	if tx.CurrentState != StateRecoveryRequired {
		t.Fatalf("state = %s, want %s", tx.CurrentState, StateRecoveryRequired)
	}

	persisted, err := store.GetLatestConfig()
	if err != nil {
		t.Fatal(err)
	}
	current := engine.GetCurrentConfig()
	if current.Revision != persisted.Revision {
		t.Fatalf("in-memory revision %d diverged from persisted revision %d", current.Revision, persisted.Revision)
	}
	if current.System.Hostname != persisted.System.Hostname {
		t.Fatalf("in-memory hostname %q diverged from persisted %q", current.System.Hostname, persisted.System.Hostname)
	}

	// The recovery reconcile must now re-drive the committed configuration,
	// not the one it replaced.
	before := len(client.requests)
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	reconcileReq := client.requests[before]
	if reconcileReq.Op != OpReconcile {
		t.Fatalf("op = %s, want %s", reconcileReq.Op, OpReconcile)
	}
	if reconcileReq.Config.Revision != persisted.Revision || reconcileReq.Config.System.Hostname != persisted.System.Hostname {
		t.Fatalf("reconcile used revision %d / hostname %q, want the committed revision %d / %q",
			reconcileReq.Config.Revision, reconcileReq.Config.System.Hostname, persisted.Revision, persisted.System.Hostname)
	}
}
