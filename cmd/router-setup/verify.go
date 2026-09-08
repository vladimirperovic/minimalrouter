package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// verifySetup proves provisioning exists without generating a default DB or
// prompting again after a later firstboot step (password/SSH) was interrupted.
func verifySetup(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dir := fs.String("data-dir", "/var/lib/minimalrouter", "canonical data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	info, err := os.Stat(filepath.Join(*dir, "minimalrouter.db"))
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("canonical database is missing")
	}
	store, err := config.NewFileStore(*dir)
	if err != nil {
		return err
	}
	defer store.Close()
	_, err = store.GetLatestConfig()
	if err != nil {
		return err
	}
	hash, err := store.GetAdminHash()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		return errors.New("administrator credentials are not configured")
	}
	// Validate the bounded stored hash encoding without knowing/logging the
	// administrator password. A non-matching probe is the expected result.
	_, err = auth.VerifyPassword("", hash)
	return err
}
