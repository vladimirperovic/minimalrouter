package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/firmware"
)

func TestActivationRepairsStaleDispatcherLinks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "updates")
	systemRoot := filepath.Join(t.TempDir(), "system")
	seedCurrentSlot(t, root, "1.0.0")
	writeLayoutFixture(t, root, "1.1.0", systemRoot, false)

	for _, item := range runtimeDispatcherLinks {
		path := rootedPath(systemRoot, item.systemPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/legacy/minimalrouter-direct-binary", path); err != nil {
			t.Fatal(err)
		}
	}

	oldServiceCommand := serviceCommand
	defer func() { serviceCommand = oldServiceCommand }()
	serviceCommand = func(args ...string) error {
		for _, item := range runtimeDispatcherLinks {
			target, err := os.Readlink(rootedPath(systemRoot, item.systemPath))
			if err != nil {
				t.Fatalf("dispatcher %s was not a symlink before service restart: %v", item.systemPath, err)
			}
			if target != item.target {
				t.Fatalf("dispatcher %s target=%q want=%q", item.systemPath, target, item.target)
			}
		}
		return nil
	}

	if err := activateAndRestart(firmware.SlotManager{Root: root}, "1.1.0", systemRoot); err != nil {
		t.Fatalf("activateAndRestart: %v", err)
	}
	state, err := (firmware.SlotManager{Root: root}).State()
	if err != nil {
		t.Fatal(err)
	}
	if state.Current != "1.1.0" || state.Previous != "1.0.0" {
		t.Fatalf("unexpected slot state after activation: %+v", state)
	}
}

func TestDispatcherRepairAlsoReplacesLegacyRegularFile(t *testing.T) {
	systemRoot := filepath.Join(t.TempDir(), "system")
	item := runtimeDispatcherLinks[0]
	path := rootedPath(systemRoot, item.systemPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("legacy routerd binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := ensureRuntimeDispatcherLinks(systemRoot); err != nil {
		t.Fatalf("ensureRuntimeDispatcherLinks: %v", err)
	}
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("legacy regular file was not replaced with symlink: %v", err)
	}
	if target != item.target {
		t.Fatalf("dispatcher target=%q want=%q", target, item.target)
	}
}
