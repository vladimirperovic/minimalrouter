package faultinject

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRunDisarmed verifies hooks are a complete no-op without the env var,
// including when the directory exists but no phase file is armed.
func TestRunDisarmed(t *testing.T) {
	t.Setenv(HookDirEnv, "")
	Run(PrePrivilegedApply)

	dir := t.TempDir()
	t.Setenv(HookDirEnv, dir)
	Run(PreSQLiteCommit)
}

// TestRunArmed verifies an armed hook actually executes and blocks until its
// command completes.
func TestRunArmed(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	if err := os.WriteFile(filepath.Join(dir, PostProvisionalApply), []byte("touch "+marker), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(HookDirEnv, dir)
	Run(PostProvisionalApply)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("armed hook did not execute: %v", err)
	}
}

// TestRunBlocks verifies a sleeping hook holds the phase open until the sleep
// completes (the window the torture lab uses to hard-stop the VM).
func TestRunBlocks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, PreCanonicalAck), []byte("sleep 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(HookDirEnv, dir)
	start := time.Now()
	Run(PreCanonicalAck)
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("hook returned too early: %v", elapsed)
	}
}
