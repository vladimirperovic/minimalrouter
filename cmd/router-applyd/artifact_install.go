package main

import (
	"fmt"
	"os"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// Artifact replacement always creates root-owned inodes. Restore service
// access on every write, independently of whether that service needs a restart.
// Dependencies are explicit so the filesystem phase can be exercised without
// applying networking or restarting services.
type artifactInstaller struct {
	write       func(string, []byte, os.FileMode) error
	secureDDNS  func() error
	secureSquid func() error
}

func (i artifactInstaller) install(cfg config.SystemConfig, generated map[string]artifact) error {
	for _, name := range restoreArtifacts {
		item := generated[name]
		if err := i.write(item.path, item.data, item.mode); err != nil {
			return fmt.Errorf("install %s: %w", name, err)
		}
	}
	if cfg.Cloudflare.DDNSEnabled {
		if err := i.secureDDNS(); err != nil {
			return fmt.Errorf("secure DDNS state: %w", err)
		}
	}
	if cfg.SquidProxy.Enabled {
		if err := i.secureSquid(); err != nil {
			return err
		}
	}
	return nil
}
