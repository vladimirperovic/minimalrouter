package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPrivateKeyPreservesRawWhitespaceBytes(t *testing.T) {
	raw := make([]byte, ed25519.PrivateKeySize)
	raw[0] = '\n'
	raw[len(raw)-1] = ' '
	path := filepath.Join(t.TempDir(), "release.key")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadPrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded) != string(raw) {
		t.Fatal("raw private key bytes were modified")
	}
}

func TestLoadPrivateKeyAcceptsBase64(t *testing.T) {
	raw := make([]byte, ed25519.PrivateKeySize)
	for i := range raw {
		raw[i] = byte(i)
	}
	path := filepath.Join(t.TempDir(), "release.key")
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(raw)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadPrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded) != string(raw) {
		t.Fatal("base64 private key was decoded incorrectly")
	}
}
