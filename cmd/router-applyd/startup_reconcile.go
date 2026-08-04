package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

const startupReconcileEnv = "MINIMALROUTER_APPLYD_STARTUP_RECONCILE"

type startupReconcileHooks struct {
	loadLastGood   func() (*config.SystemConfig, error)
	pendingExists  func() (bool, error)
	restoreRuntime func(config.SystemConfig) error
	clearPending   func() error
}

// init runs only for the installed OpenRC service. Keeping the opt-in in the
// service environment prevents host unit tests and developer builds from ever
// touching the host network merely because the package is loaded.
func init() {
	if os.Getenv(startupReconcileEnv) != "1" {
		return
	}
	if err := hardenProcess(); err != nil {
		failClosedStartup(config.SystemConfig{})
		log.Fatalf("applyd startup hardening failed closed: %v", err)
	}
	if err := reconcileStartup(startupReconcileHooks{
		loadLastGood:   loadLastGood,
		pendingExists:  pendingConfirmationExists,
		restoreRuntime: restoreLastGoodRuntime,
		clearPending:   clearPendingConfirmation,
	}); err != nil {
		// Errors that occur before restoreLastGoodRuntime begins (for example a
		// corrupt last-good file or an orphaned pending marker) must also disable
		// forwarding. Otherwise the persistent sysctl could leave a booted host
		// forwarding packets without a verified helper-owned firewall table.
		failClosedStartup(config.SystemConfig{})
		log.Fatalf("applyd startup reconciliation failed closed: %v", err)
	}
}

func reconcileStartup(h startupReconcileHooks) error {
	if h.loadLastGood == nil || h.pendingExists == nil || h.restoreRuntime == nil || h.clearPending == nil {
		return errors.New("startup reconciliation hooks are incomplete")
	}

	cfg, err := h.loadLastGood()
	if errors.Is(err, os.ErrNotExist) {
		pending, pendingErr := h.pendingExists()
		if pendingErr != nil {
			return fmt.Errorf("inspect pending confirmation: %w", pendingErr)
		}
		if pending {
			return errors.New("pending configuration exists without a recoverable last-good configuration")
		}
		// A fresh installation has no canonical state until setup completes.
		return nil
	}
	if err != nil {
		return fmt.Errorf("load last-good configuration: %w", err)
	}
	if cfg == nil {
		return errors.New("last-good configuration loader returned no configuration")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("last-good configuration is invalid: %w", err)
	}

	// /run is volatile and kernel interfaces disappear on power loss, so every
	// installed boot restores the confirmed state even when no transaction was
	// pending. This also deliberately rolls back an unconfirmed topology change.
	if err := h.restoreRuntime(*cfg); err != nil {
		return fmt.Errorf("restore confirmed runtime: %w", err)
	}
	if err := h.clearPending(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear stale pending confirmation: %w", err)
	}
	return nil
}

func pendingConfirmationExists() (bool, error) {
	_, err := os.Stat(pendingPath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func clearPendingConfirmation() error {
	return os.Remove(pendingPath)
}

func restoreLastGoodRuntime(cfg config.SystemConfig) (retErr error) {
	if err := os.MkdirAll(socketDir, 0750); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}

	generated, err := generateArtifacts(cfg)
	if err != nil {
		return fmt.Errorf("generate last-good artifacts: %w", err)
	}
	candidateDir, err := os.MkdirTemp(socketDir, "startup-candidate-")
	if err != nil {
		return fmt.Errorf("create startup candidate directory: %w", err)
	}
	defer os.RemoveAll(candidateDir)
	if err := os.Chmod(candidateDir, 0700); err != nil {
		return fmt.Errorf("secure startup candidate directory: %w", err)
	}
	candidates, err := writeCandidates(candidateDir, generated)
	if err != nil {
		return fmt.Errorf("write startup candidates: %w", err)
	}
	if err := preflight(cfg, candidates); err != nil {
		return fmt.Errorf("preflight last-good runtime: %w", err)
	}

	// Once activation begins, any failure leaves the appliance deliberately
	// non-routing. This is safer than serving an unknown mixture of old kernel
	// state and newly written service files.
	activated := false
	defer func() {
		if !activated {
			failClosedStartup(cfg)
		}
	}()

	for _, name := range []string{
		"pppoe", "chap", "dnsmasq", "adblock", "qos", "cf-ddns",
		"cf-tunnel", "doh-proxy", "hostapd", "wireguard",
		"wireguard-runtime", "squid", "squid-passwd", "nftables",
	} {
		item := generated[name]
		if err := atomicWrite(item.path, item.data, item.mode); err != nil {
			return fmt.Errorf("install startup %s: %w", name, err)
		}
	}

	if err := applyKernelHardening(cfg); err != nil {
		return err
	}
	_ = runFixed("/sbin/rc-service", "hostapd", "stop")
	if err := configureRuntimeLAN(cfg); err != nil {
		return err
	}
	if err := runNftFile(nftRuntimePath, false); err != nil {
		return fmt.Errorf("load startup nftables: %w", err)
	}
	if err := runFixed("/sbin/rc-service", "dnsmasq", "restart"); err != nil {
		return fmt.Errorf("restart startup dnsmasq: %w", err)
	}
	if cfg.WAN.Enabled {
		// Starting pppd is a local requirement; an ISP outage must not prevent the
		// LAN management plane from booting. Runtime health can reconnect later.
		if err := runFixed("/sbin/rc-service", "pppoe-wan", "restart"); err != nil {
			return fmt.Errorf("restart startup PPPoE service: %w", err)
		}
	} else {
		_ = runFixed("/sbin/rc-service", "pppoe-wan", "stop")
	}
	// QoS attaches to ppp0, which only exists after PPPoE negotiates. It is a
	// traffic-shaping optimization, not a security boundary: a failed tc attach
	// must never fail-closed the appliance, or a slow ISP handshake would block
	// routing entirely at every boot.
	if cfg.QoS.Enabled {
		if err := applyQoS(cfg); err != nil {
			log.Printf("startup QoS not applied (non-fatal): %v", err)
		}
	} else {
		clearQoS(cfg)
	}
	if err := activateWireGuard(cfg); err != nil {
		return fmt.Errorf("restore startup WireGuard: %w", err)
	}
	if err := activateWireGuardClient(cfg); err != nil {
		return fmt.Errorf("restore startup WireGuard client: %w", err)
	}
	if cfg.WiFi.Enabled {
		if err := runFixed("/sbin/rc-service", "hostapd", "restart"); err != nil {
			return fmt.Errorf("restart startup hostapd: %w", err)
		}
	} else {
		_ = runFixed("/sbin/rc-service", "hostapd", "stop")
	}
	if cfg.Cloudflare.DDNSEnabled {
		group, lookupErr := user.LookupGroup("inadyn")
		if lookupErr != nil {
			return errors.New("Cloudflare DDNS service group is unavailable")
		}
		gid, parseErr := strconv.Atoi(group.Gid)
		if parseErr != nil || os.Chown("/etc/inadyn/inadyn.conf", 0, gid) != nil {
			return errors.New("could not secure Cloudflare DDNS configuration ownership")
		}
		// Do not perform the normal forced external update here. Power recovery
		// must succeed while the ISP is unavailable; inadyn retries asynchronously.
		if err := runFixed("/sbin/rc-service", "inadyn", "restart"); err != nil {
			return fmt.Errorf("restart startup Cloudflare DDNS: %w", err)
		}
	} else {
		_ = runFixed("/sbin/rc-service", "inadyn", "stop")
	}
	if cfg.SquidProxy.Enabled {
		group, lookupErr := user.LookupGroup("squid")
		if lookupErr != nil {
			return errors.New("Squid service group is unavailable")
		}
		gid, parseErr := strconv.Atoi(group.Gid)
		if parseErr != nil || os.Chown("/etc/squid/passwd", 0, gid) != nil {
			return errors.New("could not secure Squid password file ownership")
		}
		if err := runFixed("/sbin/rc-service", "squid", "restart"); err != nil {
			return fmt.Errorf("restart startup Squid: %w", err)
		}
	} else {
		_ = runFixed("/sbin/rc-service", "squid", "stop")
	}
	if err := verifyStartupLocal(cfg); err != nil {
		return err
	}
	activated = true
	return nil
}

func verifyStartupLocal(cfg config.SystemConfig) error {
	forwarding, err := runFixedOutput("/sbin/sysctl", "-n", "net.ipv4.ip_forward")
	if err != nil || strings.TrimSpace(forwarding) != "1" {
		return errors.New("startup IPv4 forwarding is not enabled")
	}
	if err := runFixed("/usr/sbin/nft", "list", "table", "inet", "minimalrouter"); err != nil {
		return fmt.Errorf("startup nftables table unavailable: %w", err)
	}
	if err := runFixed("/sbin/rc-service", "dnsmasq", "status"); err != nil {
		return fmt.Errorf("startup dnsmasq unhealthy: %w", err)
	}
	lanInterface := cfg.RuntimeLANInterface()
	output, err := runFixedOutput("/sbin/ip", "-4", "addr", "show", "dev", lanInterface)
	if err != nil {
		return fmt.Errorf("startup LAN interface unavailable: %w", err)
	}
	prefix := cfg.LAN.CIDR
	if slash := strings.IndexByte(prefix, '/'); slash >= 0 {
		prefix = prefix[:slash]
	}
	if !strings.Contains(output, "inet "+prefix+"/") {
		return errors.New("startup LAN address is not active")
	}
	if cfg.WireGuard.Enabled {
		interfaceName := cfg.WireGuard.Interface
		if interfaceName == "" {
			interfaceName = "wg0"
		}
		if err := runFixed("/usr/bin/wg", "show", interfaceName); err != nil {
			return fmt.Errorf("startup WireGuard interface unhealthy: %w", err)
		}
		if err := runFixed("/sbin/ip", "-4", "addr", "show", "dev", interfaceName); err != nil {
			return fmt.Errorf("startup WireGuard address unavailable: %w", err)
		}
	}
	if cfg.WGClient.Enabled {
		interfaceName := cfg.WGClient.Interface
		if interfaceName == "" {
			interfaceName = "wg1"
		}
		if err := runFixed("/usr/bin/wg", "show", interfaceName); err != nil {
			return fmt.Errorf("startup WireGuard client interface unhealthy: %w", err)
		}
		if cfg.WGClient.Address != "" {
			if err := runFixed("/sbin/ip", "-4", "addr", "show", "dev", interfaceName); err != nil {
				return fmt.Errorf("startup WireGuard client address unavailable: %w", err)
			}
		}
	}
	if cfg.WiFi.Enabled {
		if err := runFixed("/sbin/rc-service", "hostapd", "status"); err != nil {
			return fmt.Errorf("startup hostapd unhealthy: %w", err)
		}
		bridgeLink, err := os.Readlink(filepath.Join("/sys/class/net", cfg.WiFi.Interface, "master"))
		if err != nil || filepath.Base(bridgeLink) != config.WiFiBridgeInterface {
			return errors.New("startup Wi-Fi interface is not attached to the LAN bridge")
		}
	}
	if cfg.SquidProxy.Enabled {
		port := cfg.SquidProxy.Port
		if port == 0 {
			port = 3128
		}
		address := net.JoinHostPort(cfg.LAN.IPAddress, strconv.Itoa(port))
		conn, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err != nil {
			return fmt.Errorf("startup Squid listener unavailable: %w", err)
		}
		_ = conn.Close()
		if err := runFixed("/sbin/rc-service", "squid", "status"); err != nil {
			return fmt.Errorf("startup Squid unhealthy: %w", err)
		}
	}
	return nil
}

func failClosedStartup(cfg config.SystemConfig) {
	_ = runFixed("/sbin/sysctl", "-w", "net.ipv4.ip_forward=0")
	interfaceName := cfg.WireGuard.Interface
	if interfaceName == "" {
		interfaceName = "wg0"
	}
	_ = removeWireGuard(interfaceName)
	clientInterface := cfg.WGClient.Interface
	if clientInterface == "" {
		clientInterface = "wg1"
	}
	_ = removeWireGuard(clientInterface)
	_ = runFixed("/sbin/rc-service", "dnsmasq", "stop")
	_ = runFixed("/sbin/rc-service", "pppoe-wan", "stop")
	_ = runFixed("/sbin/rc-service", "hostapd", "stop")
	_ = runFixed("/sbin/rc-service", "inadyn", "stop")
	_ = runFixed("/sbin/rc-service", "squid", "stop")
	_ = runFixed("/usr/sbin/nft", "delete", "table", "inet", "minimalrouter")
}
