package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mrapply "github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	mrnetwork "github.com/vladimirperovic/minimalrouter/internal/network"
)

const defaultLANIP = "192.168.1.1"

type provision struct {
	WANInterface  string `json:"wan_interface"`
	LANInterface  string `json:"lan_interface"`
	PPPoEUsername string `json:"pppoe_username"`
	PPPoEPassword string `json:"pppoe_password"`
	AdminPassword string `json:"admin_password"`
	LANIPAddress  string `json:"lan_ip_address"`
}

type consoleUI struct {
	reader *bufio.Reader
	color  bool
}

func printBanner() {
	art := `
           _       _                 _                 _
 _ __ ___ (_)_ __ (_)_ __ ___   __ _| |_ __ ___  _   _| |_ ___ _ __
| '_ ` + "`" + ` _ \| | '_ \| | '_ ` + "`" + ` _ \ / _` + "`" + ` | | '__/ _ \| | | | __/ _ \ '__|
| | | | | | | | | | | | | | | | (_| | | | | (_) | |_| | ||  __/ |
|_| |_| |_|_|_| |_|_|_| |_| |_|\__,_|_|_|  \___/ \__,_|\__\___|_|
`
	for _, line := range strings.Split(strings.Trim(art, "\n"), "\n") {
		fmt.Println(line)
	}
	fmt.Println("  first-run console setup")
	fmt.Println("  -----------------------")
}

func main() {
	if os.Geteuid() != 0 {
		fatalf("router-setup must run as root")
	}
	if len(os.Args) < 2 {
		usage()
	}

	var err error
	switch os.Args[1] {
	case "collect":
		err = collect(os.Args[2:])
	case "apply":
		err = apply(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fatalf("%v", err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: router-setup collect --output <file> [--data-dir <dir>] | router-setup apply --input <file> [--data-dir <dir>]")
	os.Exit(2)
}

func collect(args []string) error {
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	output := fs.String("output", "/run/minimalrouter-console-setup.json", "root-only provisioning file")
	dataDir := fs.String("data-dir", "/var/lib/minimalrouter", "canonical routerd data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if _, err := os.Stat(filepath.Join(*dataDir, "minimalrouter.db")); err == nil {
		store, openErr := config.NewFileStore(*dataDir)
		if openErr != nil {
			return fmt.Errorf("inspect existing configuration store: %w", openErr)
		}
		_, hashErr := store.GetAdminHash()
		closeErr := store.Close()
		if closeErr != nil {
			return fmt.Errorf("close existing configuration store: %w", closeErr)
		}
		if hashErr == nil {
			fmt.Println("Minimal Router OS is already configured; console first-run setup is skipped.")
			return nil
		}
		if !errors.Is(hashErr, sql.ErrNoRows) {
			return fmt.Errorf("inspect administrator state: %w", hashErr)
		}
	}

	ui := &consoleUI{reader: bufio.NewReader(os.Stdin), color: terminalColor()}
	fmt.Println()
	printBanner()
	fmt.Println()
	fmt.Println("What happens now:")
	fmt.Println("  1. PPPoE credentials — leave empty to configure them later in the Web Dashboard")
	fmt.Println("  2. WAN/LAN roles — WAN faces the ISP, LAN faces your clients (auto-assigned with two adapters)")
	fmt.Println("  3. Dashboard administrator password (minimum 12 characters)")
	fmt.Println("  4. Recovery console password — used only for local console recovery")
	fmt.Println("  5. A final summary — nothing is applied before you confirm it")
	fmt.Println()
	fmt.Println("Nothing is committed until you review and confirm the final summary.")
	fmt.Println()

	pppoeUser, err := ui.readLine("PPPoE username (leave empty for an isolated lab): ")
	if err != nil {
		return err
	}
	pppoePass := ""
	if pppoeUser != "" {
		pppoePass, err = ui.readSecret("PPPoE password: ")
		if err != nil {
			return err
		}
		if pppoePass == "" {
			return errors.New("PPPoE password cannot be empty when a username is supplied")
		}
	}

	recommendation, err := mrnetwork.Discover()
	if err != nil {
		return fmt.Errorf("discover interfaces: %w", err)
	}

	fmt.Println()
	fmt.Println("Testing network interfaces for a PPPoE access concentrator...")
	probeResults := make(map[string]bool, len(recommendation.Interfaces))
	physical := make([]mrnetwork.InterfaceInfo, 0, len(recommendation.Interfaces))
	for _, item := range recommendation.Interfaces {
		if item.Physical {
			physical = append(physical, item)
		}
	}
	candidates := physical
	if len(candidates) < 2 {
		candidates = recommendation.Interfaces
	}
	for _, item := range candidates {
		if !item.Up {
			_ = runQuiet("ip", "link", "set", "dev", item.Name, "up")
		}
		found, detail := discoverPPPoE(item.Name)
		probeResults[item.Name] = found
		if found {
			ui.ok(fmt.Sprintf("%s  PPPoE concentrator found%s", item.Name, optionalDetail(detail)))
		} else if item.Carrier {
			ui.warn(fmt.Sprintf("%s  link detected, no PPPoE concentrator response", item.Name))
		} else {
			ui.fail(fmt.Sprintf("%s  no carrier / no PPPoE response", item.Name))
		}
	}

	wan, lan, reason := recommendRoles(recommendation, probeResults)
	fmt.Println()
	fmt.Printf("Suggested WAN: %s\n", wan)
	fmt.Printf("Suggested LAN: %s\n", lan)
	fmt.Printf("Reason: %s\n", reason)

	useSuggested, err := ui.confirm("Use these WAN/LAN roles?", true)
	if err != nil {
		return err
	}
	if !useSuggested {
		wan, err = ui.selectInterface("Select WAN", candidates, "")
		if err != nil {
			return err
		}
		if len(candidates) == 2 {
			for _, candidate := range candidates {
				if candidate.Name != wan {
					lan = candidate.Name
					break
				}
			}
			fmt.Printf("LAN: %s (the only other interface)\n", lan)
		} else {
			lan, err = ui.selectInterface("Select LAN", candidates, wan)
			if err != nil {
				return err
			}
		}
	}
	if wan == lan || wan == "" || lan == "" {
		return errors.New("WAN and LAN must be two different interfaces")
	}

	adminPass := ""
	for {
		adminPass, err = ui.readSecret("Dashboard administrator password (minimum 12 characters): ")
		if err != nil {
			return err
		}
		if len([]rune(adminPass)) < 12 {
			ui.fail("Password is too short; use at least 12 characters.")
			continue
		}
		confirmPass, readErr := ui.readSecret("Confirm dashboard password: ")
		if readErr != nil {
			return readErr
		}
		if adminPass != confirmPass {
			ui.fail("Passwords do not match.")
			continue
		}
		break
	}

	fmt.Println()
	fmt.Println("Review")
	fmt.Println("------")
	fmt.Printf("WAN:       %s\n", wan)
	fmt.Printf("LAN:       %s\n", lan)
	fmt.Printf("LAN IP:    %s/24\n", defaultLANIP)
	if pppoeUser != "" {
		fmt.Printf("PPPoE:     %s (password hidden)\n", pppoeUser)
	} else {
		fmt.Println("PPPoE:     skipped")
	}
	fmt.Println("Dashboard: https://192.168.1.1:8443")
	fmt.Println("Admin:     password set (hidden)")
	fmt.Println()
	confirmed, err := ui.confirm("Install and apply this configuration?", true)
	if err != nil {
		return err
	}
	if !confirmed {
		return errors.New("installation cancelled by operator")
	}

	cfg := provision{
		WANInterface:  wan,
		LANInterface:  lan,
		PPPoEUsername: pppoeUser,
		PPPoEPassword: pppoePass,
		AdminPassword: adminPass,
		LANIPAddress:  defaultLANIP,
	}
	if err := writeProvision(*output, cfg); err != nil {
		return err
	}
	ui.ok("Console choices saved in volatile root-only memory; installing appliance files now.")
	return nil
}

func apply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	input := fs.String("input", "/run/minimalrouter-console-setup.json", "root-only provisioning file")
	dataDir := fs.String("data-dir", "/var/lib/minimalrouter", "canonical routerd data directory")
	offline := fs.Bool("offline", false, "write the configuration without applying the network; the first disk boot reconciles it")
	if err := fs.Parse(args); err != nil {
		return err
	}

	data, err := os.ReadFile(*input)
	if err != nil {
		return fmt.Errorf("read provisioning file: %w", err)
	}
	var requested provision
	if err := json.Unmarshal(data, &requested); err != nil {
		return fmt.Errorf("decode provisioning file: %w", err)
	}
	if requested.WANInterface == "" || requested.LANInterface == "" || requested.WANInterface == requested.LANInterface {
		return errors.New("provisioning file contains invalid WAN/LAN roles")
	}
	if len([]rune(requested.AdminPassword)) < 12 {
		return errors.New("provisioning file contains an invalid administrator password")
	}
	if (requested.PPPoEUsername == "") != (requested.PPPoEPassword == "") {
		return errors.New("provisioning file contains incomplete PPPoE credentials")
	}

	store, err := config.NewFileStore(*dataDir)
	if err != nil {
		return fmt.Errorf("open canonical configuration store: %w", err)
	}
	defer store.Close()
	if _, hashErr := store.GetAdminHash(); hashErr == nil {
		return errors.New("system is already configured; refusing to rerun first-run console setup")
	} else if !errors.Is(hashErr, sql.ErrNoRows) {
		return fmt.Errorf("read administrator state: %w", hashErr)
	}
	initial, err := store.GetLatestConfig()
	if err != nil {
		return fmt.Errorf("read initial configuration: %w", err)
	}

	hashedPassword, err := auth.HashPassword(requested.AdminPassword)
	if err != nil {
		return fmt.Errorf("hash administrator password: %w", err)
	}

	cfg := config.DefaultConfig()
	cfg.Revision = initial.Revision
	cfg.Cloudflare.DDNSEnabled = false
	cfg.Cloudflare.TunnelEnabled = false
	cfg.WiFi.Enabled = false
	cfg.WAN.Interface = requested.WANInterface
	cfg.WAN.Username = requested.PPPoEUsername
	cfg.WAN.Password = requested.PPPoEPassword
	cfg.WAN.Enabled = requested.PPPoEUsername != ""
	cfg.LAN.Interface = requested.LANInterface
	if requested.LANIPAddress != "" && requested.LANIPAddress != cfg.LAN.IPAddress {
		return fmt.Errorf("first-run LAN address must remain %s", cfg.LAN.IPAddress)
	}

	if *offline {
		// Live installer path: persist the reviewed configuration directly so
		// the first boot of the installed system reconciles it natively. The
		// live environment never runs the production router stack.
		cfg.Revision = initial.Revision + 1
		if err := store.CommitInitialSetup(cfg, hashedPassword); err != nil {
			return fmt.Errorf("store initial configuration for first boot: %w", err)
		}
		return nil
	}

	engine := mrapply.NewEngine(initial, store)
	txID := fmt.Sprintf("console-setup-%d", time.Now().UnixNano())
	pppInterface := ""
	pppAddress := ""
	tx, err := engine.ProcessInitialSetup(txID, cfg, func(applied config.SystemConfig) error {
		// Do not commit credentials or administrator state until the real production
		// PPPoE path has produced an IPv4 session. Returning an error here makes the
		// transaction engine restore the previous configuration instead.
		if requested.PPPoEUsername != "" {
			iface, address, ok := waitForPPPSession(60 * time.Second)
			if !ok {
				return errors.New("PPPoE authentication did not produce an IPv4 session within 60 seconds")
			}
			pppInterface = iface
			pppAddress = address
		}
		return store.CommitInitialSetup(applied, hashedPassword)
	})
	if err != nil {
		state := "unknown"
		if tx != nil {
			state = string(tx.CurrentState)
		}
		return fmt.Errorf("initial setup failed in state %s: %w", state, err)
	}

	ui := &consoleUI{color: terminalColor()}
	ui.ok("Configuration applied and verified by router-applyd.")
	if requested.PPPoEUsername != "" {
		ui.ok(fmt.Sprintf("PPPoE connected on %s (%s).", pppInterface, pppAddress))
	}
	ui.ok("Initial setup committed. The dashboard can now start at https://192.168.1.1:8443")
	return nil
}

func discoverPPPoE(iface string) (bool, string) {
	if _, err := exec.LookPath("pppoe-discovery"); err != nil {
		return false, "pppoe-discovery unavailable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pppoe-discovery", "-I", iface)
	output, _ := cmd.CombinedOutput()
	text := strings.ToLower(string(output))
	found := strings.Contains(text, "access-concentrator") || strings.Contains(text, "ac-ethernet-address") || strings.Contains(text, "pado")
	if !found {
		return false, ""
	}
	for _, line := range strings.Split(string(output), "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "access-concentrator:") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				return true, strings.TrimSpace(parts[1])
			}
		}
	}
	return true, ""
}

func recommendRoles(rec mrnetwork.RoleRecommendation, probes map[string]bool) (string, string, string) {
	found := make([]string, 0, len(probes))
	for _, item := range rec.Interfaces {
		if probes[item.Name] {
			found = append(found, item.Name)
		}
	}
	wan := rec.WAN
	reason := "link/default-route hardware scoring"
	if len(found) == 1 {
		wan = found[0]
		reason = "only this interface answered PPPoE discovery"
	} else if len(found) > 1 {
		reason = "PPPoE answered on multiple interfaces; manual confirmation is required"
	} else {
		reason = "no PPPoE discovery response; falling back to link/default-route hardware scoring"
	}

	lan := rec.LAN
	if lan == wan {
		lan = ""
		for _, item := range rec.Interfaces {
			if item.Name != wan && item.Physical {
				lan = item.Name
				break
			}
		}
		if lan == "" {
			for _, item := range rec.Interfaces {
				if item.Name != wan {
					lan = item.Name
					break
				}
			}
		}
	}
	return wan, lan, reason
}

func (ui *consoleUI) selectInterface(label string, items []mrnetwork.InterfaceInfo, excluded string) (string, error) {
	fmt.Println()
	fmt.Println(label + ":")
	choices := make([]mrnetwork.InterfaceInfo, 0, len(items))
	for _, item := range items {
		if item.Name == excluded {
			continue
		}
		choices = append(choices, item)
		fmt.Printf("  %d) %-10s MAC %-17s link=%s\n", len(choices), item.Name, fallback(item.MACAddress, "unknown"), yesNo(item.Carrier))
	}
	if len(choices) == 0 {
		return "", errors.New("no selectable interface remains")
	}
	for {
		value, err := ui.readLine("Choice: ")
		if err != nil {
			return "", err
		}
		index, err := strconv.Atoi(value)
		if err == nil && index >= 1 && index <= len(choices) {
			return choices[index-1].Name, nil
		}
		ui.fail("Enter one of the listed numbers.")
	}
}

func (ui *consoleUI) readLine(prompt string) (string, error) {
	fmt.Print(prompt)
	line, err := ui.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if errors.Is(err, io.EOF) && line == "" {
		return "", io.EOF
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (ui *consoleUI) readSecret(prompt string) (string, error) {
	fmt.Print(prompt)
	hidden := terminalInput() && runQuietWithStdin("stty", "-echo") == nil
	if hidden {
		defer func() { _ = runQuietWithStdin("stty", "echo") }()
	}
	line, err := ui.reader.ReadString('\n')
	fmt.Println()
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if errors.Is(err, io.EOF) && line == "" {
		return "", io.EOF
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (ui *consoleUI) confirm(prompt string, defaultYes bool) (bool, error) {
	suffix := " [Y/n]: "
	if !defaultYes {
		suffix = " [y/N]: "
	}
	for {
		answer, err := ui.readLine(prompt + suffix)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "":
			return defaultYes, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			ui.warn("Please answer y or n.")
		}
	}
}

func writeProvision(path string, cfg provision) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	tmp := path + fmt.Sprintf(".tmp-%d", os.Getpid())
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func waitForPPPSession(timeout time.Duration) (string, string, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		interfaces, err := net.Interfaces()
		if err == nil {
			for _, iface := range interfaces {
				if !strings.HasPrefix(strings.ToLower(iface.Name), "ppp") {
					continue
				}
				addrs, err := iface.Addrs()
				if err != nil {
					continue
				}
				for _, addr := range addrs {
					ip, _, err := net.ParseCIDR(addr.String())
					if err == nil && ip.To4() != nil && !ip.IsLoopback() {
						return iface.Name, ip.String(), true
					}
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", "", false
}

func runQuiet(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func runQuietWithStdin(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func terminalColor() bool {
	return terminalOutput() && os.Getenv("TERM") != "dumb" && os.Getenv("NO_COLOR") == ""
}

func terminalInput() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func terminalOutput() bool {
	info, err := os.Stdout.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (ui *consoleUI) ok(message string) {
	fmt.Printf("%s %s\n", ui.mark("\x1b[32m●\x1b[0m", "[OK]"), message)
}

func (ui *consoleUI) warn(message string) {
	fmt.Printf("%s %s\n", ui.mark("\x1b[33m●\x1b[0m", "[WARN]"), message)
}

func (ui *consoleUI) fail(message string) {
	fmt.Printf("%s %s\n", ui.mark("\x1b[31m●\x1b[0m", "[FAIL]"), message)
}

func (ui *consoleUI) mark(colored, plain string) string {
	if ui.color {
		return colored
	}
	return plain
}

func optionalDetail(detail string) string {
	if strings.TrimSpace(detail) == "" {
		return ""
	}
	return " (" + strings.TrimSpace(detail) + ")"
}

func fallback(value, other string) string {
	if strings.TrimSpace(value) == "" {
		return other
	}
	return value
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
