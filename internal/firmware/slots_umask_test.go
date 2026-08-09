package firmware

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestStagePermissionsIgnoreRestrictiveProcessUmask(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	source := t.TempDir()
	executable := filepath.Join(source, "bin", "routerd-amd64")
	asset := filepath.Join(source, "web", "dist", "assets", "app.js")
	for _, dir := range []string{filepath.Dir(executable), filepath.Dir(asset)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(executable, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(executable, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(asset, []byte("asset"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(asset, 0o644); err != nil {
		t.Fatal(err)
	}

	manifest, err := SignFirmware(source, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Version = "1.2.4"
	if err := SignManifest(manifest, privateKey); err != nil {
		t.Fatal(err)
	}

	oldUmask := unix.Umask(0o077)
	defer unix.Umask(oldUmask)

	root := filepath.Join(t.TempDir(), "update")
	manager := SlotManager{Root: root, TrustedKey: publicKey}
	if err := manager.Stage(source, manifest); err != nil {
		t.Fatal(err)
	}

	for _, relative := range []string{
		"",
		"bin",
		"web",
		filepath.Join("web", "dist"),
		filepath.Join("web", "dist", "assets"),
	} {
		path := filepath.Join(root, "slots", manifest.Version, relative)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o755 {
			t.Fatalf("staged directory %s mode = %o, want 755", relative, got)
		}
	}

	info, err := os.Stat(filepath.Join(root, "slots", manifest.Version, "bin", "routerd-amd64"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("staged executable mode = %o, want 755", got)
	}
	info, err = os.Stat(filepath.Join(root, "slots", manifest.Version, "web", "dist", "assets", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("staged asset mode = %o, want 644", got)
	}
}
