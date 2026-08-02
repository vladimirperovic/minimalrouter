package firmware

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInterruptedActivationBeforeCurrentSwitchRestoresOldState(t *testing.T) {
	manager, old, next := setupJournalTest(t)
	if err := manager.beginOperation(slotOperation{Version: operationJournalVersion, Kind: "activate", Old: old, Next: next}); err != nil {
		t.Fatal(err)
	}
	if err := manager.swapLink("previous", old.Current); err != nil {
		t.Fatal(err)
	}
	if err := manager.recoverOperation(); err != nil {
		t.Fatal(err)
	}
	state, err := manager.State()
	if err != nil {
		t.Fatal(err)
	}
	if state != old {
		t.Fatalf("recovery did not restore old state: got %+v want %+v", state, old)
	}
	assertLink(t, manager.Root, "current", old.Current)
	if _, err := os.Lstat(filepath.Join(manager.Root, "previous")); !os.IsNotExist(err) {
		t.Fatalf("previous pointer survived aborted activation: %v", err)
	}
	assertJournalRemoved(t, manager.Root)
}

func TestInterruptedActivationAfterCurrentSwitchCompletesNewState(t *testing.T) {
	manager, old, next := setupJournalTest(t)
	if err := manager.beginOperation(slotOperation{Version: operationJournalVersion, Kind: "activate", Old: old, Next: next}); err != nil {
		t.Fatal(err)
	}
	if err := manager.swapLink("previous", old.Current); err != nil {
		t.Fatal(err)
	}
	if err := manager.swapLink("current", next.Current); err != nil {
		t.Fatal(err)
	}
	projected, err := manager.State()
	if err != nil {
		t.Fatal(err)
	}
	if projected != next {
		t.Fatalf("status did not project interrupted activation: got %+v want %+v", projected, next)
	}
	if err := manager.recoverOperation(); err != nil {
		t.Fatal(err)
	}
	state, err := manager.State()
	if err != nil {
		t.Fatal(err)
	}
	if state != next {
		t.Fatalf("recovery did not complete activation: got %+v want %+v", state, next)
	}
	assertLink(t, manager.Root, "current", next.Current)
	assertLink(t, manager.Root, "previous", next.Previous)
	assertJournalRemoved(t, manager.Root)
}

func TestInterruptedRollbackAfterCurrentSwitchCompletesRollback(t *testing.T) {
	root := t.TempDir()
	manager := SlotManager{Root: root}
	createJournalTestSlots(t, root, "v1.0.0", "v1.1.0")
	old := SlotState{Current: "v1.1.0", Previous: "v1.0.0"}
	next := SlotState{Current: "v1.0.0", Previous: "v1.1.0"}
	if err := manager.saveState(old); err != nil {
		t.Fatal(err)
	}
	if err := manager.swapLink("current", old.Current); err != nil {
		t.Fatal(err)
	}
	if err := manager.swapLink("previous", old.Previous); err != nil {
		t.Fatal(err)
	}
	if err := manager.beginOperation(slotOperation{Version: operationJournalVersion, Kind: "rollback", Old: old, Next: next}); err != nil {
		t.Fatal(err)
	}
	if err := manager.swapLink("current", next.Current); err != nil {
		t.Fatal(err)
	}
	if err := manager.recoverOperation(); err != nil {
		t.Fatal(err)
	}
	state, err := manager.State()
	if err != nil {
		t.Fatal(err)
	}
	if state != next {
		t.Fatalf("recovery did not complete rollback: got %+v want %+v", state, next)
	}
	assertLink(t, root, "current", next.Current)
	assertLink(t, root, "previous", next.Previous)
	assertJournalRemoved(t, root)
}

func TestCorruptOperationJournalIsRejected(t *testing.T) {
	root := t.TempDir()
	manager := SlotManager{Root: root}
	createJournalTestSlots(t, root, "v1.0.0")
	if err := manager.swapLink("current", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	bad := slotOperation{
		Version: operationJournalVersion,
		Kind:    "activate",
		Old:     SlotState{Current: "v1.0.0"},
		Next:    SlotState{Current: "v9.9.9", Previous: "v1.0.0"},
	}
	data, err := json.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, operationJournalName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.State(); err == nil {
		t.Fatal("state accepted an operation journal that references a missing slot")
	}
}

func TestOperationJournalIsPrivate(t *testing.T) {
	manager, old, next := setupJournalTest(t)
	if err := manager.beginOperation(slotOperation{Version: operationJournalVersion, Kind: "activate", Old: old, Next: next}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(manager.Root, operationJournalName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("operation journal permissions are %o, want 600", info.Mode().Perm())
	}
}

func setupJournalTest(t *testing.T) (SlotManager, SlotState, SlotState) {
	t.Helper()
	root := t.TempDir()
	manager := SlotManager{Root: root}
	createJournalTestSlots(t, root, "v1.0.0", "v1.1.0")
	old := SlotState{Current: "v1.0.0", Pending: "v1.1.0"}
	next := SlotState{Current: "v1.1.0", Previous: "v1.0.0"}
	if err := manager.saveState(old); err != nil {
		t.Fatal(err)
	}
	if err := manager.swapLink("current", old.Current); err != nil {
		t.Fatal(err)
	}
	return manager, old, next
}

func createJournalTestSlots(t *testing.T, root string, versions ...string) {
	t.Helper()
	for _, version := range versions {
		if err := os.MkdirAll(filepath.Join(root, "slots", version), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func assertJournalRemoved(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, operationJournalName)); !os.IsNotExist(err) {
		t.Fatalf("operation journal was not removed: %v", err)
	}
}
