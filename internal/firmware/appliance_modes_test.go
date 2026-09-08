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
			path == "ip-up.d-minimalrouter-qos" || path == "firstboot-ready" || path == "firstboot" || path == "install-core.sh" || path == "init.d/minimalrouter-firstboot" {
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

func TestRecoveryWorkerRequiresUnprivilegedExecuteWhileUpdaterStaysPrivate(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		if got := ApplianceFileMode("bin/router-recovery-" + arch); got != 0o755 {
			t.Fatalf("%s recovery mode = %04o; dropped worker needs 0755", arch, got)
		}
		if got := ApplianceFileMode("bin/router-update-" + arch); got != 0o750 {
			t.Fatalf("%s updater mode = %04o; must remain 0750", arch, got)
		}
	}
	root := t.TempDir()
	manifest := completeAMD64ManifestForTest()
	writeExecutableFixture(t, root, manifest)
	checkMode := func(name string, mode os.FileMode) {
		t.Helper()
		if err := os.Chmod(filepath.Join(root, "bin", name+"-amd64"), mode); err != nil {
			t.Fatal(err)
		}
	}
	checkMode("router-update", 0o750)
	checkMode("router-recovery", 0o750)
	if err := ValidateApplianceFileModes(root, manifest); err == nil {
		t.Fatal("root-only recovery accepted even though dropped worker cannot exec it")
	}
	checkMode("router-recovery", 0o755)
	if err := ValidateApplianceFileModes(root, manifest); err != nil {
		t.Fatalf("worker-executable recovery with private updater rejected: %v", err)
	}
}
