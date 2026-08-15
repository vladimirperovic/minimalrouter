package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareDnsmasqLeaseStateRepairsExistingFile(t *testing.T) {
	dir := t.TempDir()
	leaseFile := filepath.Join(dir, "dnsmasq.leases")
	if err := os.WriteFile(leaseFile, []byte("old lease\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(leaseFile, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}

	if err := prepareDnsmasqLeaseStateAt(dir, leaseFile, os.Getuid(), os.Getgid()); err != nil {
		t.Fatalf("prepareDnsmasqLeaseStateAt: %v", err)
	}
	info, err := os.Stat(leaseFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("lease mode = %04o, want 0640", info.Mode().Perm())
	}
	content, err := os.ReadFile(leaseFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "old lease\n" {
		t.Fatalf("lease content changed: %q", content)
	}
}

func TestPrepareDnsmasqLeaseStateCreatesOwnedFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	leaseFile := filepath.Join(dir, "dnsmasq.leases")

	if err := prepareDnsmasqLeaseStateAt(dir, leaseFile, os.Getuid(), os.Getgid()); err != nil {
		t.Fatalf("prepareDnsmasqLeaseStateAt: %v", err)
	}
	info, err := os.Stat(leaseFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatalf("lease mode = %04o, want 0640", info.Mode().Perm())
	}
	if info.Size() != 0 {
		t.Fatalf("new lease file size = %d, want 0", info.Size())
	}
}

func TestPrepareDnsmasqLeaseStateRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	leaseFile := filepath.Join(dir, "dnsmasq.leases")
	if err := os.Symlink(filepath.Join(dir, "outside"), leaseFile); err != nil {
		t.Fatal(err)
	}

	if err := prepareDnsmasqLeaseStateAt(dir, leaseFile, os.Getuid(), os.Getgid()); err == nil {
		t.Fatal("symlink lease file was accepted")
	}
}
