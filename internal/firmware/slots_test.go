package firmware

import (
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSlotManagerStagesActivatesAndRollsBackSignedReleases(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manager := SlotManager{Root: root, TrustedKey: publicKey}

	stageRelease := func(version, content string) {
		t.Helper()
		source := t.TempDir()
		if err := os.WriteFile(filepath.Join(source, "minimalrouter.tar.gz"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		manifest, err := SignFirmware(source, privateKey)
		if err != nil {
			t.Fatal(err)
		}
		manifest.Version = version
		manifest.BuildDate = "2026-07-30T00:00:00Z"
		manifest.GitCommit = "0123456789abcdef"
		if err := SignManifest(manifest, privateKey); err != nil {
			t.Fatal(err)
		}
		if err := manager.Stage(source, manifest); err != nil {
			t.Fatal(err)
		}
	}

	stageRelease("v1.0.0", "one")
	if err := manager.Activate("v1.0.0"); err != nil {
		t.Fatal(err)
	}
	stageRelease("v1.1.0", "two")
	if err := manager.Activate("v1.1.0"); err != nil {
		t.Fatal(err)
	}
	state, err := manager.State()
	if err != nil {
		t.Fatal(err)
	}
	if state.Current != "v1.1.0" || state.Previous != "v1.0.0" {
		t.Fatalf("unexpected state after activation: %+v", state)
	}
	assertLink(t, root, "current", "v1.1.0")
	assertLink(t, root, "previous", "v1.0.0")

	if err := manager.Rollback(); err != nil {
		t.Fatal(err)
	}
	state, err = manager.State()
	if err != nil {
		t.Fatal(err)
	}
	if state.Current != "v1.0.0" || state.Previous != "v1.1.0" {
		t.Fatalf("unexpected state after rollback: %+v", state)
	}
	assertLink(t, root, "current", "v1.0.0")
	assertLink(t, root, "previous", "v1.1.0")
}

func TestSlotManagerRejectsTamperedRelease(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	path := filepath.Join(source, "bundle")
	if err := os.WriteFile(path, []byte("signed"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := SignFirmware(source, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Version = "v1.0.0"
	if err := SignManifest(manifest, privateKey); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := SlotManager{Root: t.TempDir(), TrustedKey: publicKey}
	if err := manager.Stage(source, manifest); err == nil {
		t.Fatal("tampered release was staged")
	}
}

func TestStageWithCorruptStateLeavesNoBlockingSlot(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "state.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "payload"), []byte("signed"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := SignFirmware(source, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Version = "v1.2.3"
	if err := SignManifest(manifest, privateKey); err != nil {
		t.Fatal(err)
	}
	manager := SlotManager{Root: root, TrustedKey: publicKey}
	if err := manager.Stage(source, manifest); err == nil {
		t.Fatal("corrupt state was accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "slots", manifest.Version)); !os.IsNotExist(err) {
		t.Fatalf("failed staging left a blocking slot: %v", err)
	}
}

func TestStagedSlotIsReadableAndExecutableByUnprivilegedServices(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "bin", "routerd-amd64"), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := SignFirmware(source, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Version = "1.2.3"
	if err := SignManifest(manifest, privateKey); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "update")
	manager := SlotManager{Root: root, TrustedKey: publicKey}
	if err := manager.Stage(source, manifest); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, filepath.Join(root, "slots"), filepath.Join(root, "slots", "1.2.3"), filepath.Join(root, "slots", "1.2.3", "bin")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o055 != 0o055 {
			t.Fatalf("%s is not traversable/readable by service users: %o", path, info.Mode().Perm())
		}
	}
	info, err := os.Stat(filepath.Join(root, "slots", "1.2.3", "bin", "routerd-amd64"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("staged routerd is not executable: %o", info.Mode().Perm())
	}
}

func TestEmptyStateIsReportableBeforeFirstUpdate(t *testing.T) {
	state, err := (SlotManager{Root: filepath.Join(t.TempDir(), "missing")}).State()
	if err != nil {
		t.Fatal(err)
	}
	if state != (SlotState{}) {
		t.Fatalf("unexpected initial state: %+v", state)
	}
}

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
	createTestSlots(t, root, "v1.0.0", "v1.1.0")
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
	createTestSlots(t, root, "v1.0.0")
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
	createTestSlots(t, root, "v1.0.0", "v1.1.0")
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

func createTestSlots(t *testing.T, root string, versions ...string) {
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

func assertLink(t *testing.T, root, name, version string) {
	t.Helper()
	target, err := os.Readlink(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join("slots", version) {
		t.Fatalf("%s points to %q, want %q", name, target, filepath.Join("slots", version))
	}
}
