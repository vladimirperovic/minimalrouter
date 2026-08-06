package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/firmware"
)

func allLayoutFilesForTest(t *testing.T) []runtimeLayoutFile {
	t.Helper()
	bootstrap, err := bootstrapRuntimeFiles()
	if err != nil {
		t.Fatal(err)
	}
	files := append([]runtimeLayoutFile(nil), runtimeLayoutFiles...)
	return append(files, bootstrap...)
}

func writeLayoutFixture(t *testing.T, updateRoot, version, systemRoot string, mismatch bool) {
	t.Helper()
	slotRoot := filepath.Join(updateRoot, "slots", version)
	for i, item := range allLayoutFilesForTest(t) {
		candidate := []byte("layout-" + item.slotPath + "\n")
		if err := os.MkdirAll(filepath.Dir(filepath.Join(slotRoot, item.slotPath)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(slotRoot, item.slotPath), candidate, 0o644); err != nil {
			t.Fatal(err)
		}
		installed := candidate
		if mismatch && i == 0 {
			installed = []byte("old-layout\n")
		}
		installedPath := rootedPath(systemRoot, item.systemPath)
		if err := os.MkdirAll(filepath.Dir(installedPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(installedPath, installed, item.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(installedPath, item.mode); err != nil {
			t.Fatal(err)
		}
	}
	leaseDir := rootedPath(systemRoot, "/var/lib/minimalrouter-dhcp")
	if err := os.MkdirAll(leaseDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(leaseDir, 0o750); err != nil {
		t.Fatal(err)
	}
}

func seedCurrentSlot(t *testing.T, root, version string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "slots", version), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("slots", version), filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeLayoutMismatchBlocksActivationBeforeServiceRestart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "updates")
	systemRoot := filepath.Join(t.TempDir(), "system")
	seedCurrentSlot(t, root, "1.0.0")
	writeLayoutFixture(t, root, "1.1.0", systemRoot, true)

	oldServiceCommand := serviceCommand
	defer func() { serviceCommand = oldServiceCommand }()
	called := false
	serviceCommand = func(args ...string) error {
		called = true
		return nil
	}

	err := activateAndRestart(firmware.SlotManager{Root: root}, "1.1.0", systemRoot)
	if err == nil {
		t.Fatal("runtime-layout mismatch was accepted")
	}
	if called {
		t.Fatal("services changed before runtime-layout compatibility was proven")
	}
	state, stateErr := (firmware.SlotManager{Root: root}).State()
	if stateErr != nil || state.Current != "1.0.0" {
		t.Fatalf("current slot changed after rejected layout: state=%+v err=%v", state, stateErr)
	}
}

func TestBootstrapBinaryMismatchBlocksABActivation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "updates")
	systemRoot := filepath.Join(t.TempDir(), "system")
	seedCurrentSlot(t, root, "1.0.0")
	writeLayoutFixture(t, root, "1.1.0", systemRoot, false)

	bootstrap, err := bootstrapRuntimeFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(bootstrap) == 0 {
		t.Fatal("bootstrap compatibility file list is empty")
	}
	candidatePath := filepath.Join(root, "slots", "1.1.0", bootstrap[0].slotPath)
	if err := os.WriteFile(candidatePath, []byte("new-bootstrap-binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldServiceCommand := serviceCommand
	defer func() { serviceCommand = oldServiceCommand }()
	called := false
	serviceCommand = func(args ...string) error {
		called = true
		return nil
	}

	if err := activateAndRestart(firmware.SlotManager{Root: root}, "1.1.0", systemRoot); err == nil {
		t.Fatal("A/B activation accepted a bootstrap-stable binary change")
	}
	if called {
		t.Fatal("service restart occurred before bootstrap compatibility failure")
	}
}

func TestActivationRestartsBothDaemonsFromSameSlot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "updates")
	systemRoot := filepath.Join(t.TempDir(), "system")
	seedCurrentSlot(t, root, "1.0.0")
	writeLayoutFixture(t, root, "1.1.0", systemRoot, false)

	oldServiceCommand := serviceCommand
	defer func() { serviceCommand = oldServiceCommand }()
	var calls [][]string
	serviceCommand = func(args ...string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}

	if err := activateAndRestart(firmware.SlotManager{Root: root}, "1.1.0", systemRoot); err != nil {
		t.Fatalf("activateAndRestart: %v", err)
	}
	want := [][]string{
		{"routerd", "stop"},
		{"router-applyd", "restart"},
		{"routerd", "start"},
		{"router-applyd", "status"},
		{"routerd", "status"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("service sequence=%v want=%v", calls, want)
	}
	state, err := (firmware.SlotManager{Root: root}).State()
	if err != nil {
		t.Fatal(err)
	}
	if state.Current != "1.1.0" || state.Previous != "1.0.0" {
		t.Fatalf("unexpected slot state after activation: %+v", state)
	}
}

func TestFailedNewDaemonRestartAutomaticallyRollsBack(t *testing.T) {
	root := filepath.Join(t.TempDir(), "updates")
	systemRoot := filepath.Join(t.TempDir(), "system")
	seedCurrentSlot(t, root, "1.0.0")
	writeLayoutFixture(t, root, "1.1.0", systemRoot, false)

	oldServiceCommand := serviceCommand
	defer func() { serviceCommand = oldServiceCommand }()
	failedOnce := false
	serviceCommand = func(args ...string) error {
		if len(args) == 2 && args[0] == "router-applyd" && args[1] == "restart" && !failedOnce {
			failedOnce = true
			return errors.New("simulated new helper startup failure")
		}
		return nil
	}

	if err := activateAndRestart(firmware.SlotManager{Root: root}, "1.1.0", systemRoot); err == nil {
		t.Fatal("failed new slot restart was reported as success")
	}
	state, err := (firmware.SlotManager{Root: root}).State()
	if err != nil {
		t.Fatal(err)
	}
	if state.Current != "1.0.0" {
		t.Fatalf("automatic rollback did not restore old current slot: %+v", state)
	}
}

func TestActivationRequiresRollbackBaseline(t *testing.T) {
	root := filepath.Join(t.TempDir(), "updates")
	systemRoot := filepath.Join(t.TempDir(), "system")
	writeLayoutFixture(t, root, "1.1.0", systemRoot, false)
	if err := activateAndRestart(firmware.SlotManager{Root: root}, "1.1.0", systemRoot); err == nil {
		t.Fatal("first A/B activation without rollback baseline was accepted")
	}
}
