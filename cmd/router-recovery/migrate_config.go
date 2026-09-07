package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/vladimirperovic/minimalrouter/internal/recovery"
)

func migrateConfigCommand(args []string) error {
	fs := flag.NewFlagSet("migrate-config", flag.ContinueOnError)
	file := fs.String("file", "", "complete unredacted replacement configuration JSON")
	confirmation := fs.String("confirm", "", "must equal MIGRATE-CONFIG")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" || *confirmation != "MIGRATE-CONFIG" || fs.NArg() != 0 {
		return errors.New("offline migration requires --file CONFIG.json --confirm MIGRATE-CONFIG")
	}
	dataDir := os.Getenv("MINIMALROUTER_DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir
	}
	backup, err := recovery.MigrateConfig(dataDir, *file)
	if err != nil {
		return fmt.Errorf("offline migration failed (do not remove its journal; retry with the same file): %w", err)
	}
	fmt.Printf("Offline migration committed. Private original-state backup: %s\nStart router-applyd, then routerd, and verify local connectivity.\n", backup)
	return nil
}
