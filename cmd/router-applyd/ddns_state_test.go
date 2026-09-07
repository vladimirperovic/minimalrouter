package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestDDNSCacheRepairPreservesContentAndIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, "provider-router.example.net.cache")
	content := []byte("198.51.100.8\n")
	if err := os.WriteFile(cache, content, 0644); err != nil {
		t.Fatal(err)
	}
	untouched := filepath.Join(dir, "unrelated.txt")
	if err := os.WriteFile(untouched, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := prepareDDNSCacheStateAt(dir, os.Getuid(), os.Getgid()); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	if info.Mode().Perm() != 0600 || st.Uid != uint32(os.Getuid()) || st.Gid != uint32(os.Getgid()) {
		t.Fatalf("cache access not repaired: mode=%o uid=%d gid=%d", info.Mode().Perm(), st.Uid, st.Gid)
	}
	got, err := os.ReadFile(cache)
	if err != nil || string(got) != string(content) {
		t.Fatalf("cache contents changed: %q, %v", got, err)
	}
	other, err := os.Stat(untouched)
	if err != nil || other.Mode().Perm() != 0644 {
		t.Fatal("non-cache file was changed")
	}
}

func TestDDNSCacheRepairCreatesMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	if err := prepareDDNSCacheStateAt(dir, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0755 {
		t.Fatalf("unexpected directory: %v, %v", info, err)
	}
}

func TestDDNSCacheRepairRejectsSymlinksAndDirectories(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "directory-symlink"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "cache")
			outside := filepath.Join(base, "outside")
			if err := os.WriteFile(outside, []byte("unchanged"), 0644); err != nil {
				t.Fatal(err)
			}
			if kind == "directory-symlink" {
				if err := os.Symlink(base, dir); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(dir, 0755); err != nil {
					t.Fatal(err)
				}
				entry := filepath.Join(dir, "unsafe.cache")
				if kind == "symlink" {
					if err := os.Symlink(outside, entry); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Mkdir(entry, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := prepareDDNSCacheStateAt(dir, os.Getuid(), os.Getgid()); err == nil {
				t.Fatal("unsafe cache accepted")
			}
			info, err := os.Stat(outside)
			if err != nil || info.Mode().Perm() != 0644 {
				t.Fatal("outside target permissions changed")
			}
		})
	}
}
