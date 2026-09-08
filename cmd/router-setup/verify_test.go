package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestVerifyDoesNotCreateMissingDatabase(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	if err := verifySetup([]string{"--data-dir", dir}); err == nil {
		t.Fatal("missing canonical setup admitted")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("verification created state")
	}
}

func TestVerifyRequiresCommittedAdministratorAndSupportsResume(t *testing.T) {
	dir := t.TempDir()
	store, err := config.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := verifySetup([]string{"--data-dir", dir}); err == nil {
		t.Fatal("unconfigured database admitted")
	}
	provisionPath := filepath.Join(t.TempDir(), "setup.json")
	if err := writeProvision(provisionPath, provision{WANInterface: "eth0", LANInterface: "eth1", AdminPassword: "synthetic-test-password", LANIPAddress: defaultLANIP}); err != nil {
		t.Fatal(err)
	}
	if err := apply([]string{"--offline", "--input", provisionPath, "--data-dir", dir}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := verifySetup([]string{"--data-dir", dir}); err != nil {
			t.Fatalf("resume verification: %v", err)
		}
	}
}
