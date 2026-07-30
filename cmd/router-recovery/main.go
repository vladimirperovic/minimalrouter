package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	networkinfo "github.com/vladimirperovic/minimalrouter/internal/network"
	"github.com/vladimirperovic/minimalrouter/internal/recovery"
)

const defaultDataDir = "/var/lib/minimalrouter"

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	if os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h" {
		usage(os.Stdout)
		return
	}
	if os.Geteuid() != 0 {
		fatal(errors.New("router-recovery must be run as root on the local console"))
	}

	switch os.Args[1] {
	case "interfaces":
		recommendation, err := networkinfo.Discover()
		if err != nil {
			fatal(err)
		}
		fmt.Printf("Recommended WAN: %s\nRecommended LAN: %s\n", recommendation.WAN, recommendation.LAN)
		for _, item := range recommendation.Interfaces {
			fmt.Printf("- %-15s physical=%t carrier=%t up=%t default-route=%t mac=%s\n",
				item.Name, item.Physical, item.Carrier, item.Up, item.DefaultRoute, item.MACAddress)
		}
		for _, warning := range recommendation.Warnings {
			fmt.Printf("WARNING: %s\n", warning)
		}

	case "reset-auth":
		fs := flag.NewFlagSet("reset-auth", flag.ExitOnError)
		disableTOTP := fs.Bool("disable-totp", false, "remove the configured TOTP secret")
		passwordStdin := fs.Bool("password-stdin", false, "read the new password from stdin")
		_ = fs.Parse(os.Args[2:])
		manager, closeStore := openManager()
		defer closeStore()
		password := readPassword(*passwordStdin)
		if err := manager.ResetAuthentication(password, *disableTOTP); err != nil {
			fatal(err)
		}
		fmt.Println("Administrator credentials reset and all sessions revoked.")

	case "set-lan":
		fs := flag.NewFlagSet("set-lan", flag.ExitOnError)
		iface := fs.String("interface", "", "Linux LAN interface name")
		cidr := fs.String("cidr", "", "LAN host address and prefix, for example 192.168.10.1/24")
		_ = fs.Parse(os.Args[2:])
		manager, closeStore := openManager()
		defer closeStore()
		snapshot, err := manager.SetLAN(*iface, *cidr)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("LAN configuration stored. Undo snapshot: %s. Restart router services to reconcile.\n", snapshot.ID)

	case "snapshots":
		manager, closeStore := openManager()
		defer closeStore()
		snapshots, err := manager.ListSnapshots()
		if err != nil {
			fatal(err)
		}
		for _, snapshot := range snapshots {
			fmt.Printf("%s revision=%d created=%s checksum=%s\n", snapshot.ID, snapshot.Revision, snapshot.CreatedAt, snapshot.Checksum)
		}

	case "restore-snapshot":
		fs := flag.NewFlagSet("restore-snapshot", flag.ExitOnError)
		id := fs.String("id", "", "snapshot identifier")
		confirm := fs.String("confirm", "", "must equal RESTORE-SNAPSHOT")
		_ = fs.Parse(os.Args[2:])
		if *confirm != "RESTORE-SNAPSHOT" {
			fatal(errors.New("snapshot restore requires --confirm RESTORE-SNAPSHOT"))
		}
		manager, closeStore := openManager()
		defer closeStore()
		undo, err := manager.RestoreSnapshot(*id)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("Snapshot restored. Undo snapshot: %s. Restart router services to reconcile.\n", undo.ID)

	case "factory-reset":
		fs := flag.NewFlagSet("factory-reset", flag.ExitOnError)
		wan := fs.String("wan", "", "WAN interface; auto-recommended when omitted")
		lan := fs.String("lan", "", "LAN interface; auto-recommended when omitted")
		confirm := fs.String("confirm", "", "must equal FACTORY-RESET")
		passwordStdin := fs.Bool("password-stdin", false, "read the new administrator password from stdin")
		_ = fs.Parse(os.Args[2:])
		if *confirm != "FACTORY-RESET" {
			fatal(errors.New("factory reset requires --confirm FACTORY-RESET"))
		}
		if *wan == "" || *lan == "" {
			recommendation, err := networkinfo.Discover()
			if err != nil {
				fatal(fmt.Errorf("discover interfaces: %w", err))
			}
			if *wan == "" {
				*wan = recommendation.WAN
			}
			if *lan == "" {
				*lan = recommendation.LAN
			}
		}
		manager, closeStore := openManager()
		defer closeStore()
		password := readPassword(*passwordStdin)
		snapshot, err := manager.FactoryReset(*wan, *lan, password)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("Factory defaults stored; TOTP and sessions cleared. Recovery snapshot: %s. Reboot or restart both router services.\n", snapshot.ID)

	default:
		usage(os.Stderr)
		os.Exit(2)
	}
}

func openManager() (recovery.Manager, func()) {
	dataDir := os.Getenv("MINIMALROUTER_DATA_DIR")
	if dataDir == "" {
		dataDir = defaultDataDir
	}
	store, err := config.NewStore(dataDir)
	if err != nil {
		fatal(err)
	}
	return recovery.Manager{Store: store}, func() { _ = store.Close() }
}

func readPassword(fromStdin bool) string {
	if !fromStdin {
		fatal(errors.New("password must be supplied through --password-stdin to avoid shell history exposure"))
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && len(line) == 0 {
		fatal(fmt.Errorf("read password: %w", err))
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		fatal(errors.New("password cannot be empty"))
	}
	return password
}

func usage(w *os.File) {
	fmt.Fprintln(w, `Usage: router-recovery <command> [options]

Commands:
  interfaces
  reset-auth --password-stdin [--disable-totp]
  set-lan --interface NAME --cidr ADDRESS/PREFIX
  snapshots
  restore-snapshot --id SNAPSHOT --confirm RESTORE-SNAPSHOT
  factory-reset [--wan NAME --lan NAME] --password-stdin --confirm FACTORY-RESET
  help`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}
