package firmware

import (
	"crypto/ed25519"
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
