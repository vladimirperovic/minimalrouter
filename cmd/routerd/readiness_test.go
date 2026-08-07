package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSignalRouterdReadyWritesCanonicalRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routerd.ready")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(routerdReadyFileEnv, path)

	if err := signalRouterdReady(42); err != nil {
		t.Fatalf("signal readiness: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "42\n" {
		t.Fatalf("readiness marker=%q, want canonical revision", data)
	}
}

func TestSignalRouterdReadyWithoutConfiguredMarkerIsNoOp(t *testing.T) {
	t.Setenv(routerdReadyFileEnv, "")
	if err := signalRouterdReady(1); err != nil {
		t.Fatalf("unset readiness marker must be optional outside OpenRC: %v", err)
	}
}
