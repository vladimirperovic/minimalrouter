package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicSymlinkReplacesExistingPath(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")
	link := filepath.Join(dir, "active")
	if err := os.WriteFile(first, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := atomicSymlink(first, link); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || target != first {
		t.Fatalf("first target = %q, %v", target, err)
	}
	if err := atomicSymlink(second, link); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || target != second {
		t.Fatalf("second target = %q, %v", target, err)
	}
}
