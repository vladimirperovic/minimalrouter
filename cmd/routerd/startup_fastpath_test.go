package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func writeStartupHash(t *testing.T, dir string, cfg config.SystemConfig, mode os.FileMode) string {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	path := filepath.Join(dir, "startup.sha256")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(digest[:])+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStartupRuntimeVerifiedAtMatchesExactCanonicalConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	path := writeStartupHash(t, t.TempDir(), cfg, 0600)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !startupRuntimeVerifiedAt(cfg, path, info.ModTime().Add(time.Second)) {
		t.Fatal("fresh exact canonical hash was not accepted")
	}

	changed := cfg.DeepCopy()
	changed.System.Hostname = "different-router"
	if startupRuntimeVerifiedAt(changed, path, info.ModTime().Add(time.Second)) {
		t.Fatal("mismatched canonical configuration was accepted")
	}
}

func TestStartupRuntimeVerifiedAtRejectsStaleWritableAndMalformedMarkers(t *testing.T) {
	cfg := config.DefaultConfig()
	dir := t.TempDir()
	path := writeStartupHash(t, dir, cfg, 0600)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if startupRuntimeVerifiedAt(cfg, path, info.ModTime().Add(startupVerifiedMaxAge+time.Second)) {
		t.Fatal("stale startup marker was accepted")
	}

	if err := os.Chmod(path, 0660); err != nil {
		t.Fatal(err)
	}
	if startupRuntimeVerifiedAt(cfg, path, info.ModTime().Add(time.Second)) {
		t.Fatal("group-writable startup marker was accepted")
	}

	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not-a-sha256\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if startupRuntimeVerifiedAt(cfg, path, time.Now()) {
		t.Fatal("malformed startup marker was accepted")
	}
}
