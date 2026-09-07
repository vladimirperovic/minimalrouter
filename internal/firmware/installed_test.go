package firmware

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallRetryCannotEraseHigherIntentFloor(t *testing.T) {
	m := SlotManager{Root: t.TempDir()}
	if err := m.BeginInstallation("2.0.0"); err != nil {
		t.Fatal(err)
	}
	_, key, _ := ed25519.GenerateKey(nil)
	source, _ := signedApplianceFixture(t, key, "1.0.0")
	inventory, err := InspectLocalPayload(source)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(m.Root, installationJournal))
	if err != nil {
		t.Fatal(err)
	}
	for _, floor := range []string{"", "1.0.0"} {
		if err := m.BeginInstallation(floor); err == nil {
			t.Fatal("retry lowered interrupted-install floor")
		}
		if err := m.InstallBaseline(source, inventory, floor); err == nil {
			t.Fatal("commit lowered interrupted-install floor")
		}
	}
	after, err := os.ReadFile(filepath.Join(m.Root, installationJournal))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected retry rewrote the installation journal")
	}
	state, err := m.State()
	if err != nil || state.MinimumVersion != "2.0.0" || !state.InstallationPending {
		t.Fatalf("lost install intent: %+v %v", state, err)
	}
}

func TestUnsignedLegacyBaselineStaysUnknown(t *testing.T) {
	m := SlotManager{Root: t.TempDir()}
	_, key, _ := ed25519.GenerateKey(nil)
	source, _ := signedApplianceFixture(t, key, "1.0.0")
	inventory, err := InspectLocalPayload(source)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := m.BeginInstallation(""); err != nil {
			t.Fatal(err)
		}
		if err := m.InstallBaseline(source, inventory, ""); err != nil {
			t.Fatal(err)
		}
		state, err := m.State()
		if err != nil {
			t.Fatal(err)
		}
		if state.MinimumVersion != "" || state.InstallationPending {
			t.Fatalf("development installation invented trust state: %+v", state)
		}
		if _, err := state.UpgradeFloor(); err == nil {
			t.Fatal("unsigned development baseline became web-updatable")
		}
	}
}

func TestInstallOperationRecoveryRetainsIntentFloor(t *testing.T) {
	m, old, next := setupJournalTest(t)
	if err := m.BeginInstallation("2.0.0"); err != nil {
		t.Fatal(err)
	}
	next.Previous = ""
	next.MinimumVersion = "2.0.0"
	if err := m.beginOperation(slotOperation{Version: operationJournalVersion, Kind: "install", Old: old, Next: next}); err != nil {
		t.Fatal(err)
	}
	state, err := m.State()
	if err != nil || state.MinimumVersion != "2.0.0" || !state.InstallationPending {
		t.Fatalf("projected recovery erased install intent: %+v %v", state, err)
	}
	if err := m.BeginInstallation("1.0.0"); err == nil {
		t.Fatal("recovery/retry lowered intent floor")
	}
	state, err = m.State()
	if err != nil || state.MinimumVersion != "2.0.0" || !state.InstallationPending {
		t.Fatalf("recovery erased install intent: %+v %v", state, err)
	}
}

func TestLegacyNormalRollbackPersistsCurrentFloor(t *testing.T) {
	m := SlotManager{Root: t.TempDir()}
	for _, version := range []string{"1.0.0", "1.1.0"} {
		if err := os.MkdirAll(filepath.Join(m.Root, "slots", version), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.saveState(SlotState{Current: "1.1.0", Previous: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Rollback(); err != nil {
		t.Fatal(err)
	}
	state, err := m.State()
	if err != nil || state.MinimumVersion != "1.1.0" {
		t.Fatalf("legacy rollback lost floor: %+v %v", state, err)
	}
}

func TestInstalledFloorAndRollbackRemainMonotonic(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	m := SlotManager{Root: t.TempDir(), TrustedKey: pub}
	source, _ := signedApplianceFixture(t, key, "1.2.0")
	inventory, err := InspectLocalPayload(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.BeginInstallation("1.2.0"); err != nil {
		t.Fatal(err)
	}
	if err := m.InstallBaseline(source, inventory, "1.2.0"); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"1.1.0", "1.2.0"} {
		src, manifest := signedApplianceFixture(t, key, version)
		if err := m.Stage(src, manifest); err == nil {
			t.Fatalf("baseline admitted %s", version)
		}
	}
	src, manifest := signedApplianceFixture(t, key, "1.3.0")
	if err := m.Stage(src, manifest); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate("1.3.0"); err != nil {
		t.Fatal(err)
	}
	if err := m.Rollback(); err != nil {
		t.Fatal(err)
	}
	state, err := m.State()
	if err != nil || state.MinimumVersion != "1.3.0" {
		t.Fatalf("rollback lowered floor: %+v %v", state, err)
	}
	if err := m.Stage(src, manifest); err == nil {
		t.Fatal("rollback allowed replay through normal stage")
	}
}

func TestLegacyUnknownBaselineRefusesSignedStaging(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	m := SlotManager{Root: t.TempDir(), TrustedKey: pub}
	version := "0.0.0+bootstrap.123456"
	if err := os.MkdirAll(filepath.Join(m.Root, "slots", version), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("slots/"+version, filepath.Join(m.Root, "current")); err != nil {
		t.Fatal(err)
	}
	src, manifest := signedApplianceFixture(t, key, "0.1.5")
	if err := m.Stage(src, manifest); err == nil {
		t.Fatal("synthetic directory was accepted as an installed version")
	}
}

func TestInterruptedFullInstallBlocksUpdatesAndDropsCrossGenerationRollback(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	m := SlotManager{Root: t.TempDir(), TrustedKey: pub}
	src, manifest := signedApplianceFixture(t, key, "1.0.0")
	if err := m.Stage(src, manifest); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate("1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := m.BeginInstallation("2.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate("1.0.0"); err == nil {
		t.Fatal("activation admitted during incomplete installation")
	}
	if err := m.Stage(src, manifest); err == nil {
		t.Fatal("staging admitted during incomplete installation")
	}
	if err := m.Rollback(); err == nil {
		t.Fatal("rollback admitted during incomplete installation")
	}
	inventory, err := InspectLocalPayload(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.InstallBaseline(src, inventory, "2.0.0"); err != nil {
		t.Fatal(err)
	}
	state, err := m.State()
	if err != nil || state.Previous != "" || state.MinimumVersion != "2.0.0" {
		t.Fatalf("bad reinstall state: %+v %v", state, err)
	}
	if err := m.Rollback(); err == nil {
		t.Fatal("full install retained cross-generation rollback")
	}
}

func TestNormalActivationRejectsRetainedOlderSlot(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	m := SlotManager{Root: t.TempDir(), TrustedKey: pub}
	for _, version := range []string{"1.0.0", "1.1.0"} {
		src, manifest := signedApplianceFixture(t, key, version)
		if err := m.Stage(src, manifest); err != nil {
			t.Fatal(err)
		}
		if err := m.Activate(version); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Activate("1.0.0"); err == nil {
		t.Fatal("normal activation performed a downgrade")
	}
	state, _ := m.State()
	if state.Current != "1.1.0" {
		t.Fatal("rejected activation changed current")
	}
	if err := m.Rollback(); err != nil {
		t.Fatalf("explicit rollback was lost: %v", err)
	}
}

func TestRuntimeModesRejectOwnerOnlyDaemonAndUnreadableCompressedAsset(t *testing.T) {
	for _, path := range []string{"bin/routerd-amd64", "web/dist/assets/app.js.gz"} {
		t.Run(path, func(t *testing.T) {
			m := completeAMD64ManifestForTest()
			m.Files["web/dist/assets/app.js.gz"] = "00"
			root := t.TempDir()
			writeExecutableFixture(t, root, m)
			if err := os.Chmod(filepath.Join(root, path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := ValidateApplianceFileModes(root, m); err == nil {
				t.Fatal("owner-only runtime file accepted")
			}
		})
	}
}
