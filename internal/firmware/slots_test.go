package firmware

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func signedApplianceFixture(t *testing.T, privateKey ed25519.PrivateKey, version string) (string, *FirmwareManifest) {
	t.Helper()
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("unsupported test architecture %s", runtime.GOARCH)
	}
	source := t.TempDir()
	writeExecutableFixture(t, source, completeManifestForArchForTest(runtime.GOARCH))
	manifest, err := SignFirmware(source, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Version = version
	manifest.BuildDate = "2026-08-15T00:00:00Z"
	manifest.GitCommit = "0123456789abcdef"
	if err := SignManifest(manifest, privateKey); err != nil {
		t.Fatal(err)
	}
	return source, manifest
}

func TestSlotManagerStagesActivatesAndRollsBackSignedReleases(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manager := SlotManager{Root: root, TrustedKey: publicKey}

	stageRelease := func(version string) {
		t.Helper()
		source, manifest := signedApplianceFixture(t, privateKey, version)
		if err := manager.Stage(source, manifest); err != nil {
			t.Fatal(err)
		}
	}

	stageRelease("v1.0.0")
	if err := manager.Activate("v1.0.0"); err != nil {
		t.Fatal(err)
	}
	stageRelease("v1.1.0")
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
	source, manifest := signedApplianceFixture(t, privateKey, "v1.0.0")
	path := filepath.Join(source, "web", "dist", "index.html")
	if err := os.WriteFile(path, []byte("tampered"), 0o644); err != nil {
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
	source, manifest := signedApplianceFixture(t, privateKey, "v1.2.3")
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
	source, manifest := signedApplianceFixture(t, privateKey, "1.2.3")
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
	info, err := os.Stat(filepath.Join(root, "slots", "1.2.3", "bin", "routerd-"+runtime.GOARCH))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("staged routerd is not executable: %o", info.Mode().Perm())
	}
}

func TestStageRejectsSignedDowngradeButRollbackStillWorks(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manager := SlotManager{Root: root, TrustedKey: publicKey}

	source100, manifest100 := signedApplianceFixture(t, privateKey, "v1.0.0")
	if err := manager.Stage(source100, manifest100); err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate("v1.0.0"); err != nil {
		t.Fatal(err)
	}
	source110, manifest110 := signedApplianceFixture(t, privateKey, "v1.1.0")
	if err := manager.Stage(source110, manifest110); err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate("v1.1.0"); err != nil {
		t.Fatal(err)
	}

	source090, manifest090 := signedApplianceFixture(t, privateKey, "v0.9.0")
	if err := manager.Stage(source090, manifest090); err == nil {
		t.Fatal("signed downgrade was accepted by normal staging")
	}
	if err := manager.Rollback(); err != nil {
		t.Fatalf("explicit rollback was blocked by anti-downgrade policy: %v", err)
	}
	state, err := manager.State()
	if err != nil {
		t.Fatal(err)
	}
	if state.Current != "v1.0.0" {
		t.Fatalf("rollback restored %q, want v1.0.0", state.Current)
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
