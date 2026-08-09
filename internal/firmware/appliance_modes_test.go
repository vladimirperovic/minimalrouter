package firmware

import (
	"os"
	"path/filepath"
	"testing"
)

func writeExecutableFixture(t *testing.T, root string, manifest *FirmwareManifest) {
	t.Helper()
	for path := range manifest.Files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if filepath.Dir(path) == "bin" || path == "slot-exec" || path == "install.sh" ||
			path == "init.d/routerd" || path == "init.d/router-applyd" || path == "init.d/pppoe-wan" ||
			path == "ip-up.d-minimalrouter-qos" {
			mode = 0o755
		}
		if err := os.WriteFile(full, []byte(path), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(full, mode); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidateApplianceArchitectureRejectsMismatchedPayload(t *testing.T) {
	manifest := completeAMD64ManifestForTest()
	if err := ValidateApplianceArchitecture(manifest, "amd64"); err != nil {
		t.Fatalf("matching AMD64 payload rejected: %v", err)
	}
	if err := ValidateApplianceArchitecture(manifest, "arm64"); err == nil {
		t.Fatal("AMD64 payload was accepted for ARM64 activation")
	}
	if err := ValidateApplianceArchitecture(manifest, "riscv64"); err == nil {
		t.Fatal("unsupported runtime architecture was accepted")
	}
}

func TestValidateApplianceFileModesAcceptsExecutableDaemons(t *testing.T) {
	root := t.TempDir()
	manifest := completeAMD64ManifestForTest()
	writeExecutableFixture(t, root, manifest)
	if err := ValidateApplianceFileModes(root, manifest); err != nil {
		t.Fatalf("valid executable layout rejected: %v", err)
	}
}

func TestValidateApplianceFileModesRejectsNonExecutableApplyd(t *testing.T) {
	root := t.TempDir()
	manifest := completeAMD64ManifestForTest()
	writeExecutableFixture(t, root, manifest)
	path := filepath.Join(root, "bin/router-applyd-amd64")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateApplianceFileModes(root, manifest); err == nil {
		t.Fatal("signed payload with non-executable helper was accepted")
	}
}

func TestValidateApplianceFileModesRejectsRootOnlyExecutableDaemon(t *testing.T) {
	root := t.TempDir()
	manifest := completeAMD64ManifestForTest()
	writeExecutableFixture(t, root, manifest)
	path := filepath.Join(root, "bin/routerd-amd64")
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ValidateApplianceFileModes(root, manifest); err == nil {
		t.Fatal("signed payload with root-only executable daemon was accepted")
	}
}

func TestValidateApplianceFileModesRejectsRootOnlyWebIndex(t *testing.T) {
	root := t.TempDir()
	manifest := completeAMD64ManifestForTest()
	writeExecutableFixture(t, root, manifest)
	path := filepath.Join(root, "web/dist/index.html")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateApplianceFileModes(root, manifest); err == nil {
		t.Fatal("signed payload with root-only web index was accepted")
	}
}

func TestValidateApplianceFileModesRejectsRootOnlyNestedWebAsset(t *testing.T) {
	root := t.TempDir()
	manifest := completeAMD64ManifestForTest()
	manifest.Files["web/dist/assets/app.js"] = "fixture-hash"
	writeExecutableFixture(t, root, manifest)
	path := filepath.Join(root, "web/dist/assets/app.js")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateApplianceFileModes(root, manifest); err == nil {
		t.Fatal("signed payload with root-only nested web asset was accepted")
	}
}

func TestValidateApplianceFileModesRejectsUnsafeManifestPathBeforeModeInspection(t *testing.T) {
	root := t.TempDir()
	manifest := completeAMD64ManifestForTest()
	manifest.Files["web/dist/../../outside"] = "fixture-hash"
	writeExecutableFixture(t, root, completeAMD64ManifestForTest())
	if err := ValidateApplianceFileModes(root, manifest); err == nil {
		t.Fatal("unsafe manifest path was accepted by mode preflight")
	}
}
