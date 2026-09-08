package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClearPendingFileRemovesDurablyAndAllowsRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	if err := os.WriteFile(path, []byte("synthetic pending state"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := clearPendingFile(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("pending file retained: %v", err)
	}
	if err := clearPendingFile(path); err != nil {
		t.Fatalf("retry after removal failed: %v", err)
	}
}

func TestClearPendingFileDoesNotAcknowledgeRemovalFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(path, "do-not-remove")
	if err := os.WriteFile(child, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := clearPendingFile(path); err == nil {
		t.Fatal("acknowledged failed pending removal")
	}
	if data, err := os.ReadFile(child); err != nil || string(data) != "unchanged" {
		t.Fatal("removal modified unexpected state")
	}
}
