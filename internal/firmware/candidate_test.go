package firmware

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func writeCandidateFile(t *testing.T, root, relative string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(relative+"\n"), mode); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReleaseCandidateIncludesModeAndArchitectureContracts(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		"web/dist/index.html",
		"compatibility.json",
		"sysctl/99-minimalrouter.conf",
		"modules/minimalrouter.conf",
		"logrotate/minimalrouter",
	} {
		writeCandidateFile(t, root, path, 0o644)
	}
	for _, path := range []string{
		"slot-exec",
		"install.sh",
		"init.d/routerd",
		"init.d/router-applyd",
		"init.d/pppoe-wan",
		"ip-up.d-minimalrouter-qos",
		"bin/routerd-amd64",
		"bin/router-applyd-amd64",
		"bin/router-recovery-amd64",
		"bin/router-update-amd64",
	} {
		writeCandidateFile(t, root, path, 0o755)
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := SignFirmware(root, priv)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Version = "0.1.3"
	if err := SignManifest(manifest, priv); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReleaseCandidateForArch(root, manifest, pub, "amd64"); err != nil {
		t.Fatalf("valid AMD64 candidate rejected: %v", err)
	}
	if err := ValidateReleaseCandidateForArch(root, manifest, pub, "arm64"); err == nil {
		t.Fatal("AMD64 candidate was accepted for an ARM64 host")
	}

	// Modes are deliberately not part of SHA-256. A signature-only preflight
	// would still pass after this change; the unified candidate check must not.
	if err := os.Chmod(filepath.Join(root, "bin/routerd-amd64"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFirmware(root, manifest, pub); err != nil {
		t.Fatalf("content verification should still pass after mode-only change: %v", err)
	}
	if err := ValidateReleaseCandidateForArch(root, manifest, pub, "amd64"); err == nil {
		t.Fatal("candidate with missing executable bit was accepted")
	}
}
