package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/vladimirperovic/minimalrouter/internal/firmware"
)

const (
	defaultUpdateRoot = "/var/lib/minimalrouter-update"
	defaultPublicKey  = "/etc/minimalrouter/firmware-signing.pub"
)

func main() {
	if os.Geteuid() != 0 {
		fatal(errors.New("router-update must run as root"))
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	root := envOr("MINIMALROUTER_UPDATE_ROOT", defaultUpdateRoot)
	keyPath := envOr("MINIMALROUTER_FIRMWARE_PUBLIC_KEY", defaultPublicKey)
	key, err := firmware.LoadTrustedPublicKey(keyPath)
	if err != nil {
		fatal(fmt.Errorf("load pinned firmware key: %w", err))
	}
	manager := firmware.SlotManager{Root: root, TrustedKey: key}

	switch os.Args[1] {
	case "stage":
		fs := flag.NewFlagSet("stage", flag.ExitOnError)
		directory := fs.String("dir", "", "already-extracted release directory")
		manifestPath := fs.String("manifest", "", "signed release manifest")
		_ = fs.Parse(os.Args[2:])
		if *directory == "" || *manifestPath == "" {
			fatal(errors.New("stage requires --dir and --manifest"))
		}
		manifest, err := firmware.LoadManifest(*manifestPath)
		if err != nil {
			fatal(err)
		}
		if err := manager.Stage(*directory, manifest); err != nil {
			fatal(err)
		}
		fmt.Printf("Release %s verified and staged. Run activate explicitly after review.\n", manifest.Version)
	case "activate":
		fs := flag.NewFlagSet("activate", flag.ExitOnError)
		version := fs.String("version", "", "staged version")
		confirm := fs.String("confirm", "", "must equal ACTIVATE-UPDATE")
		_ = fs.Parse(os.Args[2:])
		if *confirm != "ACTIVATE-UPDATE" {
			fatal(errors.New("activation requires --confirm ACTIVATE-UPDATE"))
		}
		if err := manager.Activate(*version); err != nil {
			fatal(err)
		}
		fmt.Println("Update slot activated. Restart the appliance and verify health before pruning the previous slot.")
	case "rollback":
		fs := flag.NewFlagSet("rollback", flag.ExitOnError)
		confirm := fs.String("confirm", "", "must equal ROLLBACK-UPDATE")
		_ = fs.Parse(os.Args[2:])
		if *confirm != "ROLLBACK-UPDATE" {
			fatal(errors.New("rollback requires --confirm ROLLBACK-UPDATE"))
		}
		if err := manager.Rollback(); err != nil {
			fatal(err)
		}
		fmt.Println("Previous verified update slot restored.")
	case "status":
		state, err := manager.State()
		if err != nil {
			fatal(err)
		}
		data, _ := json.MarshalIndent(state, "", "  ")
		fmt.Println(string(data))
	default:
		usage()
		os.Exit(2)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: router-update <command>
  stage --dir PATH --manifest PATH
  activate --version VERSION --confirm ACTIVATE-UPDATE
  rollback --confirm ROLLBACK-UPDATE
  status`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}
