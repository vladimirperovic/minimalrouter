package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func sourceDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("bundle payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The bundle is written as root to a path under a world-writable /tmp. Opening
// it with O_CREATE|O_TRUNC and no O_EXCL followed a symlink, so an unprivileged
// service on the appliance could have root truncate any file it named.
func TestBundleRefusesToFollowASymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim.conf")
	original := []byte("content only root may replace\n")
	if err := os.WriteFile(victim, original, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "bundle.tar.gz")
	if err := os.Symlink(victim, link); err != nil {
		// The appliance is Linux, so this case must never be quietly skipped
		// where it actually applies. Windows needs a privilege for symlinks and
		// is only a development host.
		if runtime.GOOS == "windows" {
			t.Skipf("creating a symlink needs a privilege this Windows host lacks: %v", err)
		}
		t.Fatalf("could not create the symlink this test exists to exercise: %v", err)
	}

	if err := tarGzipDirectory(sourceDirectory(t), link); err == nil {
		t.Error("writing through a symlink must be refused")
	}
	after, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Errorf("the target of the symlink was overwritten: %q", after)
	}
}

// The same flag protects an ordinary existing file from being clobbered.
func TestBundleRefusesAnExistingDestination(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "already-there.tar.gz")
	if err := os.WriteFile(existing, []byte("do not clobber"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tarGzipDirectory(sourceDirectory(t), existing); err == nil {
		t.Error("an existing destination must be refused rather than truncated")
	}
	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "do not clobber" {
		t.Error("the existing file was modified")
	}
}

func TestBundleWritesAReadableArchiveWithRestrictivePermissions(t *testing.T) {
	output := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := tarGzipDirectory(sourceDirectory(t), output); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeIsPOSIX() && info.Mode().Perm() != 0o600 {
		t.Errorf("bundle permissions are %v, want 0600", info.Mode().Perm())
	}

	file, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()

	reader := tar.NewReader(gz)
	header, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != "note.txt" {
		t.Errorf("unexpected archive member %q", header.Name)
	}
	// Entries are flattened to bare names, so an archive can never carry a path
	// that escapes the extraction directory.
	if strings.ContainsAny(header.Name, `/\`) {
		t.Errorf("archive member %q carries a path", header.Name)
	}
}

func runtimeIsPOSIX() bool {
	return os.PathSeparator == '/'
}
