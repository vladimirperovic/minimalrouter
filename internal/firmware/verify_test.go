package firmware

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func TestFirmwareUsesPinnedTrustAnchor(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rootfs.img"), []byte("trusted payload"), 0600); err != nil {
		t.Fatal(err)
	}
	trustedPub, trustedPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := SignFirmware(dir, trustedPriv)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyFirmware(dir, manifest, trustedPub); err != nil {
		t.Fatalf("trusted firmware rejected: %v", err)
	}

	attackerPub, attackerPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	attackerManifest, err := SignFirmware(dir, attackerPriv)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyFirmware(dir, attackerManifest, trustedPub); err == nil {
		t.Fatal("manifest-supplied attacker key was trusted")
	}
	if err := VerifyFirmware(dir, attackerManifest, attackerPub); err != nil {
		t.Fatalf("test setup produced an invalid attacker signature: %v", err)
	}
}

func TestFirmwareRejectsSymlinkArtifact(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "rootfs.img")); err != nil {
		t.Fatal(err)
	}
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SignFirmware(dir, privateKey); err == nil {
		t.Fatal("signer accepted a symlink artifact")
	}
}

func TestVerifyFirmwareRejectsPostSignRootOnlyExecutableMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "bin", "routerd-amd64")
	if err := os.WriteFile(path, []byte("signed binary"), 0755); err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := SignFirmware(dir, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFirmware(dir, manifest, publicKey); err == nil {
		t.Fatal("post-sign root-only executable mode was accepted")
	}
}

func TestVerifyFirmwareRejectsPostSignRootOnlyWebMode(t *testing.T) {
	dir := t.TempDir()
	assetDir := filepath.Join(dir, "web", "dist", "assets")
	if err := os.MkdirAll(assetDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(assetDir, "app.js")
	if err := os.WriteFile(path, []byte("signed asset"), 0644); err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := SignFirmware(dir, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFirmware(dir, manifest, publicKey); err == nil {
		t.Fatal("post-sign root-only web mode was accepted")
	}
}
