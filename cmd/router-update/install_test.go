package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/firmware"
)

func installerFixture(t *testing.T, key ed25519.PrivateKey, version string) string {
	t.Helper()
	root := t.TempDir()
	paths := []string{"web/dist/index.html", "web/dist/assets/app.js.gz", "install.sh", "install-core.sh"}
	for _, item := range allLayoutFilesForTest(t) {
		paths = append(paths, item.slotPath)
	}
	for _, role := range []string{"routerd", "router-applyd", "router-update", "router-recovery", "router-setup"} {
		paths = append(paths, "bin/"+role+"-"+runtime.GOARCH)
	}
	for _, path := range paths {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("fixture:"+path), firmware.ApplianceFileMode(path)); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(full, firmware.ApplianceFileMode(path)); err != nil {
			t.Fatal(err)
		}
	}
	if key != nil {
		if err := os.WriteFile(filepath.Join(root, "firmware-signing.pub"), []byte(hex.EncodeToString(key.Public().(ed25519.PublicKey))), 0644); err != nil {
			t.Fatal(err)
		}
		m, err := firmware.SignFirmware(root, key)
		if err != nil {
			t.Fatal(err)
		}
		m.Version = version
		if err := firmware.SignManifest(m, key); err != nil {
			t.Fatal(err)
		}
		if err := firmware.SaveManifest(m, filepath.Join(root, "release-manifest.json")); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFullInstallPreflightCannotRaceANewerActivation(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	m := firmware.SlotManager{Root: t.TempDir(), TrustedKey: pub}
	oldSource := installerFixture(t, key, "1.2.0")
	inv, minimum, err := inspectInstallation(m, oldSource, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	newSource := installerFixture(t, key, "1.3.0")
	newManifest, err := firmware.LoadManifest(filepath.Join(newSource, "release-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Stage(newSource, newManifest); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate("1.3.0"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(filepath.Join(m.Root, "slots"))
	if err != nil {
		t.Fatal(err)
	}
	for _, staleMinimum := range []string{minimum, ""} {
		if err := m.BeginInstallation(staleMinimum); err == nil {
			t.Fatalf("begin admitted stale floor %q", staleMinimum)
		}
		if _, err := os.Stat(filepath.Join(m.Root, "installation.json")); !os.IsNotExist(err) {
			t.Fatal("rejected begin wrote install intent")
		}
		if err := m.InstallBaseline(oldSource, inv, staleMinimum); err == nil {
			t.Fatalf("baseline commit admitted stale floor %q", staleMinimum)
		}
	}
	after, err := os.ReadDir(filepath.Join(m.Root, "slots"))
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatal("rejected installer copied a baseline")
	}
	state, err := m.State()
	if err != nil || state.Current != "1.3.0" || state.MinimumVersion != "1.3.0" {
		t.Fatalf("race lowered active/floor state: %+v %v", state, err)
	}
}

func TestInstallerTrustPreflightIsReadOnlyAndRejectsWrongKey(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	_, other, _ := ed25519.GenerateKey(nil)
	system := t.TempDir()
	keyPath := rootedPath(system, defaultPublicKey)
	if err := os.MkdirAll(filepath.Dir(keyPath), 0755); err != nil {
		t.Fatal(err)
	}
	original := []byte(hex.EncodeToString(pub))
	if err := os.WriteFile(keyPath, original, 0644); err != nil {
		t.Fatal(err)
	}
	m := firmware.SlotManager{Root: filepath.Join(system, "updates")}
	if _, _, err := inspectInstallation(m, installerFixture(t, other, "1.2.0"), system); err == nil {
		t.Fatal("foreign key accepted")
	}
	got, _ := os.ReadFile(keyPath)
	if string(got) != string(original) {
		t.Fatal("trust key was changed during preflight")
	}
	if _, err := os.Stat(m.Root); !os.IsNotExist(err) {
		t.Fatal("read-only preflight mutated update state")
	}
}

func TestInstallerVersionAndUnsignedPreservation(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	system := t.TempDir()
	m := firmware.SlotManager{Root: filepath.Join(system, "updates"), TrustedKey: pub}
	source := installerFixture(t, key, "1.2.0")
	inv, floor, err := inspectInstallation(m, source, system)
	if err != nil {
		t.Fatal(err)
	}
	if floor != "1.2.0" {
		t.Fatal(floor)
	}
	if err := m.BeginInstallation(floor); err != nil {
		t.Fatal(err)
	}
	if err := m.InstallBaseline(source, inv, floor); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"1.1.0", "1.2.0", "1.3.0"} {
		_, _, err := inspectInstallation(m, installerFixture(t, key, version), system)
		if (err != nil) != (version == "1.1.0") {
			t.Fatalf("version %s preflight: %v", version, err)
		}
	}
	_, floor, err = inspectInstallation(m, installerFixture(t, nil, ""), system)
	if err != nil || floor != "1.2.0" {
		t.Fatalf("dev install erased floor: %s %v", floor, err)
	}
}

func TestInstallerRejectsUnsignedExtraOrTamperedCompressedAsset(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(nil)
	for _, path := range []string{"install-extra.sh", "web/dist/assets/app.js.gz"} {
		source := installerFixture(t, key, "1.2.0")
		if err := os.WriteFile(filepath.Join(source, path), []byte("tampered"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := inspectInstallation(firmware.SlotManager{Root: filepath.Join(t.TempDir(), "updates")}, source, t.TempDir()); err == nil {
			t.Fatalf("accepted tamper: %s", path)
		}
	}
}

func TestFullInstallPreflightRequiresStartupAndInstallerFiles(t *testing.T) {
	for _, path := range []string{"firstboot", "firstboot-ready", "init.d/minimalrouter-firstboot", "install-core.sh", "bin/router-setup-" + runtime.GOARCH} {
		t.Run(path, func(t *testing.T) {
			source := installerFixture(t, nil, "")
			if err := os.Remove(filepath.Join(source, path)); err != nil {
				t.Fatal(err)
			}
			m := firmware.SlotManager{Root: filepath.Join(t.TempDir(), "updates")}
			if _, _, err := inspectInstallation(m, source, t.TempDir()); err == nil {
				t.Fatal("incomplete full install passed preflight")
			}
			if _, err := os.Stat(m.Root); !os.IsNotExist(err) {
				t.Fatal("incomplete preflight mutated update state")
			}
		})
	}
}

func TestInstallCommitRequiresMatchingDurableIntegration(t *testing.T) {
	root := t.TempDir()
	system := t.TempDir()
	writeLayoutFixture(t, root, "1.2.0", system, false)
	source := filepath.Join(root, "slots", "1.2.0")
	if err := syncInstalledIntegration(source, system); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rootedPath(system, runtimeLayoutFiles[0].systemPath), []byte("different generation"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := syncInstalledIntegration(source, system); err == nil {
		t.Fatal("mismatched integration could clear the install fence")
	}
}
