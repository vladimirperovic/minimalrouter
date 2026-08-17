package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	networkinfo "github.com/vladimirperovic/minimalrouter/internal/network"
	"github.com/vladimirperovic/minimalrouter/internal/recovery"
)

const defaultDataDir = "/var/lib/minimalrouter"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h") {
		usage(os.Stdout)
		return
	}
	if os.Geteuid() != 0 {
		fatal(errors.New("router-recovery must be run as root on the local console"))
	}
	if len(os.Args) < 2 {
		interactiveMenu()
		return
	}
	runCommand(os.Args[1], os.Args[2:])
}

func runCommand(command string, args []string) {
	switch command {
	case "interfaces":
		showInterfaces()
	case "reset-auth":
		fs := flag.NewFlagSet("reset-auth", flag.ExitOnError)
		disable := fs.Bool("disable-totp", false, "remove configured TOTP secret")
		stdin := fs.Bool("password-stdin", false, "read new password from stdin")
		_ = fs.Parse(args)
		m, closeStore := openManager()
		defer closeStore()
		if err := m.ResetAuthentication(readPassword(*stdin), *disable); err != nil {
			fatal(err)
		}
		fmt.Println("Administrator credentials reset and all sessions revoked.")
	case "set-lan":
		fs := flag.NewFlagSet("set-lan", flag.ExitOnError)
		iface := fs.String("interface", "", "Linux LAN interface name")
		cidr := fs.String("cidr", "", "LAN host address/prefix")
		_ = fs.Parse(args)
		m, closeStore := openManager()
		defer closeStore()
		snap, err := m.SetLAN(*iface, *cidr)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("LAN stored. Undo snapshot: %s. Restart router services to reconcile.\n", snap.ID)
	case "set-wan":
		fs := flag.NewFlagSet("set-wan", flag.ExitOnError)
		iface := fs.String("interface", "", "Linux WAN interface name")
		_ = fs.Parse(args)
		m, closeStore := openManager()
		defer closeStore()
		snap, err := m.SetWAN(*iface)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("WAN stored. Undo snapshot: %s. Restart router services to reconcile.\n", snap.ID)
	case "snapshots":
		listSnapshots()
	case "support-bundle":
		fs := flag.NewFlagSet("support-bundle", flag.ExitOnError)
		output := fs.String("output", "", "output .tar.gz path (default: /tmp/minimalrouter-support-<timestamp>.tar.gz)")
		_ = fs.Parse(args)
		path, err := createSupportBundle(*output)
		if err != nil {
			fatal(err)
		}
		fmt.Println("Sanitized support bundle created:", path)
	case "restore-snapshot":
		fs := flag.NewFlagSet("restore-snapshot", flag.ExitOnError)
		id := fs.String("id", "", "snapshot identifier")
		confirmFlag := fs.String("confirm", "", "must equal RESTORE-SNAPSHOT")
		_ = fs.Parse(args)
		if *confirmFlag != "RESTORE-SNAPSHOT" {
			fatal(errors.New("snapshot restore requires --confirm RESTORE-SNAPSHOT"))
		}
		m, closeStore := openManager()
		defer closeStore()
		undo, err := m.RestoreSnapshot(*id)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("Snapshot restored. Undo snapshot: %s. Restart router services.\n", undo.ID)
	case "restore-last-good":
		m, closeStore := openManager()
		defer closeStore()
		undo, id, err := m.RestoreLatestSnapshot()
		if err != nil {
			fatal(err)
		}
		fmt.Printf("Restored latest verified snapshot %s. Undo snapshot: %s. Restart router services.\n", id, undo.ID)
	case "factory-reset":
		factoryReset(args)
	default:
		usage(os.Stderr)
		os.Exit(2)
	}
}

func interactiveMenu() {
	reader := bufio.NewReader(os.Stdin)
	readLine := func(prompt string) (string, bool) {
		fmt.Print(prompt)
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			return "", false
		}
		return strings.TrimSpace(line), true
	}
	ask := func(prompt string) (string, bool) { return readLine(prompt) }
	askConfirm := func(prompt string) bool {
		fmt.Printf("%s (type YES): ", prompt)
		return confirm(func() (string, bool) { return readLine("") }, prompt)
	}

	for {
		fmt.Print("\nminimalrouter recovery\n======================\n1) Show interfaces / status\n2) Assign WAN interface\n3) Assign LAN interface + IP\n4) Restore last-known-good configuration\n5) List / restore snapshot\n6) Factory reset\n7) Reset admin password / TOTP\n8) Restart router services\n9) Reboot\ns) Create sanitized support bundle\n0) Shell\nq) Quit\n\nSelect: ")
		choice, ok := readLine("")
		if !ok {
			return
		}
		if choice == "" {
			continue
		}
		switch choice {
		case "1":
			showInterfaces()
		case "2":
			iface, ok := ask("WAN interface: ")
			if ok {
				withManager(func(m recovery.Manager) error {
					snap, err := m.SetWAN(iface)
					if err == nil {
						fmt.Println("Saved; undo snapshot:", snap.ID)
					}
					return err
				})
			}
		case "3":
			iface, ok := ask("LAN interface: ")
			if !ok {
				break
			}
			cidr, ok := ask("LAN CIDR (e.g. 192.168.1.1/24): ")
			if ok {
				withManager(func(m recovery.Manager) error {
					snap, err := m.SetLAN(iface, cidr)
					if err == nil {
						fmt.Println("Saved; undo snapshot:", snap.ID)
					}
					return err
				})
			}
		case "4":
			if askConfirm("Restore latest verified snapshot?") {
				withManager(func(m recovery.Manager) error {
					undo, id, err := m.RestoreLatestSnapshot()
					if err == nil {
						fmt.Printf("Restored %s; undo snapshot %s\n", id, undo.ID)
					}
					return err
				})
			}
		case "5":
			listSnapshots()
			id, ok := ask("Snapshot ID to restore (blank cancels): ")
			if ok && id != "" && askConfirm("Restore this snapshot?") {
				withManager(func(m recovery.Manager) error {
					undo, err := m.RestoreSnapshot(id)
					if err == nil {
						fmt.Println("Restored; undo snapshot:", undo.ID)
					}
					return err
				})
			}
		case "6":
			fmt.Println("Factory reset remains confirmation-protected. Run: router-recovery factory-reset --password-stdin --confirm FACTORY-RESET")
		case "7":
			fmt.Println("Run: router-recovery reset-auth --password-stdin --disable-totp")
		case "8":
			if askConfirm("Restart router-applyd and routerd?") {
				run("rc-service", "router-applyd", "restart")
				run("rc-service", "routerd", "restart")
			}
		case "9":
			if askConfirm("Reboot this router VM now?") {
				run("reboot")
			}
		case "s", "S":
			path, err := createSupportBundle("")
			if err != nil {
				fmt.Println("ERROR:", err)
			} else {
				fmt.Println("Sanitized support bundle created:", path)
				fmt.Println("It excludes passwords, private keys, configuration DB, process environments and shell history.")
			}
		case "0":
			fmt.Println("Type 'exit' to return to recovery console.")
			cmd := exec.Command("/bin/sh")
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			_ = cmd.Run()
			// Recreate the buffered reader after the shell returns so no buffered
			// terminal input can leak between the recovery menu and the shell.
			reader = bufio.NewReader(os.Stdin)
		case "q", "Q":
			return
		default:
			fmt.Println("Unknown selection.")
		}
	}
}

func showInterfaces() {
	recommendation, err := networkinfo.Discover()
	if err != nil {
		fmt.Println("ERROR:", err)
		return
	}
	fmt.Printf("Recommended WAN: %s\nRecommended LAN: %s\n", recommendation.WAN, recommendation.LAN)
	for _, item := range recommendation.Interfaces {
		fmt.Printf("- %-15s physical=%t carrier=%t up=%t default-route=%t mac=%s\n", item.Name, item.Physical, item.Carrier, item.Up, item.DefaultRoute, item.MACAddress)
	}
	for _, warning := range recommendation.Warnings {
		fmt.Println("WARNING:", warning)
	}
}

func listSnapshots() {
	m, closeStore := openManager()
	defer closeStore()
	snapshots, err := m.ListSnapshots()
	if err != nil {
		fmt.Println("ERROR:", err)
		return
	}
	for _, snapshot := range snapshots {
		fmt.Printf("%s revision=%d created=%s checksum=%s\n", snapshot.ID, snapshot.Revision, snapshot.CreatedAt, snapshot.Checksum)
	}
}

func factoryReset(args []string) {
	fs := flag.NewFlagSet("factory-reset", flag.ExitOnError)
	wan := fs.String("wan", "", "WAN interface")
	lan := fs.String("lan", "", "LAN interface")
	confirmFlag := fs.String("confirm", "", "must equal FACTORY-RESET")
	stdin := fs.Bool("password-stdin", false, "read password from stdin")
	_ = fs.Parse(args)
	if *confirmFlag != "FACTORY-RESET" {
		fatal(errors.New("factory reset requires --confirm FACTORY-RESET"))
	}
	if *wan == "" || *lan == "" {
		recommendation, err := networkinfo.Discover()
		if err != nil {
			fatal(err)
		}
		if *wan == "" {
			*wan = recommendation.WAN
		}
		if *lan == "" {
			*lan = recommendation.LAN
		}
	}
	m, closeStore := openManager()
	defer closeStore()
	snap, err := m.FactoryReset(*wan, *lan, readPassword(*stdin))
	if err != nil {
		fatal(err)
	}
	fmt.Println("Factory defaults stored. Recovery snapshot:", snap.ID)
}

func withManager(fn func(recovery.Manager) error) {
	m, closeStore := openManager()
	defer closeStore()
	if err := fn(m); err != nil {
		fmt.Println("ERROR:", err)
	}
}

func confirm(readLine func() (string, bool), _ string) bool {
	value, ok := readLine()
	if !ok {
		return false
	}
	return value == "YES"
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Println("ERROR:", err)
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
		fatal(err)
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		fatal(errors.New("password cannot be empty"))
	}
	return password
}

func usage(w *os.File) {
	fmt.Fprintln(w, `Usage: router-recovery [command] [options]
No command opens the interactive local recovery console.
Commands:
  interfaces
  reset-auth --password-stdin [--disable-totp]
  set-wan --interface NAME
  set-lan --interface NAME --cidr ADDRESS/PREFIX
  snapshots
  support-bundle [--output PATH]
  restore-last-good
  restore-snapshot --id SNAPSHOT --confirm RESTORE-SNAPSHOT
  factory-reset [--wan NAME --lan NAME] --password-stdin --confirm FACTORY-RESET
  help`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "ERROR:", err)
	os.Exit(1)
}
