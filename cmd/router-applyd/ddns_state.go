package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Earlier root-run provider checks could leave a cache unreadable by the
// daemon. Repair existing regular cache files without reading their contents.
// Root confines operations to the service directory even if an entry changes
// between inspection and repair; a cache symlink cannot target host secrets.
func prepareDDNSCacheStateAt(dir string, uid, gid int) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create DDNS cache directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("DDNS cache directory is not a real directory")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Chown(".", uid, gid); err != nil {
		return fmt.Errorf("own DDNS cache directory: %w", err)
	}
	if err := root.Chmod(".", 0755); err != nil {
		return fmt.Errorf("secure DDNS cache directory: %w", err)
	}
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".cache") {
			continue
		}
		info, err := root.Lstat(entry.Name())
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("DDNS cache entry is not a regular file")
		}
		if err := root.Chown(entry.Name(), uid, gid); err != nil {
			return fmt.Errorf("own DDNS cache file: %w", err)
		}
		if err := root.Chmod(entry.Name(), 0600); err != nil {
			return fmt.Errorf("secure DDNS cache file: %w", err)
		}
	}
	return nil
}
