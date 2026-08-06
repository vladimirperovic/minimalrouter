package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/vladimirperovic/minimalrouter/internal/firmware"
)

const (
	defaultUpdateRoot = "/var/lib/minimalrouter-update"
	defaultPublicKey  = "/etc/minimalrouter/firmware-signing.pub"
)

func main() {
	os.Exit(run(os.Args[1:], os.Geteuid(), os.Stdout, os.Stderr))
}

func run(args []string, euid int, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	if args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		usage(stdout)
		return 0
	}

	root := envOr("MINIMALROUTER_UPDATE_ROOT", defaultUpdateRoot)
	systemRoot := envOr("MINIMALROUTER_SYSTEM_ROOT", "/")
	manager := firmware.SlotManager{Root: root}

	requireRoot := func() bool {
		if euid == 0 {
			return true
		}
		fmt.Fprintln(stderr, "ERROR: router-update must run as root for this command")
		return false
	}

	switch args[0] {
	case "stage":
		if !requireRoot() {
			return 1
		}
		fs := flag.NewFlagSet("stage", flag.ContinueOnError)
		fs.SetOutput(stderr)
		directory := fs.String("dir", "", "already-extracted release directory")
		manifestPath := fs.String("manifest", "", "signed release manifest")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		if *directory == "" || *manifestPath == "" {
			fmt.Fprintln(stderr, "ERROR: stage requires --dir and --manifest")
			return 2
		}
		keyPath := envOr("MINIMALROUTER_FIRMWARE_PUBLIC_KEY", defaultPublicKey)
		key, err := firmware.LoadTrustedPublicKey(keyPath)
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: load pinned firmware key: %v\n", err)
			return 1
		}
		manager.TrustedKey = key
		manifest, err := firmware.LoadManifest(*manifestPath)
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		if err := firmware.ValidateAppliancePayload(manifest); err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		if err := manager.Stage(*directory, manifest); err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Release %s verified and staged. Run activate explicitly after review.\n", manifest.Version)
		return 0

	case "activate":
		if !requireRoot() {
			return 1
		}
		fs := flag.NewFlagSet("activate", flag.ContinueOnError)
		fs.SetOutput(stderr)
		version := fs.String("version", "", "staged version")
		confirm := fs.String("confirm", "", "must equal ACTIVATE-UPDATE")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		if *version == "" {
			fmt.Fprintln(stderr, "ERROR: activate requires --version")
			return 2
		}
		if *confirm != "ACTIVATE-UPDATE" {
			fmt.Fprintln(stderr, "ERROR: activation requires --confirm ACTIVATE-UPDATE")
			return 2
		}
		if err := activateAndRestart(manager, *version, systemRoot); err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "Update slot activated; router-applyd and routerd restarted from the same slot and passed service health checks.")
		return 0

	case "rollback":
		if !requireRoot() {
			return 1
		}
		fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
		fs.SetOutput(stderr)
		confirm := fs.String("confirm", "", "must equal ROLLBACK-UPDATE")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		if *confirm != "ROLLBACK-UPDATE" {
			fmt.Fprintln(stderr, "ERROR: rollback requires --confirm ROLLBACK-UPDATE")
			return 2
		}
		if err := rollbackAndRestart(manager); err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "Previous verified update slot restored and both router services restarted from it.")
		return 0

	case "status":
		fs := flag.NewFlagSet("status", flag.ContinueOnError)
		fs.SetOutput(stderr)
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		state, err := manager.State()
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: %v\n", err)
			return 1
		}
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "ERROR: encode update state: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(data))
		return 0

	default:
		usage(stderr)
		return 2
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `Usage: router-update <command>
  stage --dir PATH --manifest PATH
  activate --version VERSION --confirm ACTIVATE-UPDATE
  rollback --confirm ROLLBACK-UPDATE
  status
  help`)
}
