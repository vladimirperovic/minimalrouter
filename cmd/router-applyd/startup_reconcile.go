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

const emergencyNftables = `table inet minimalrouter {
    chain input {
        type filter hook input priority filter; policy drop;
        iifname "lo" accept
        ct state established,related accept
    }
    chain forward {
        type filter hook forward priority filter; policy drop;
    }
    chain output {
        type filter hook output priority filter; policy accept;
    }
}
`

type startupReconcileHooks struct {
	loadLastGood    func() (*config.SystemConfig, error)
	pendingExists   func() (bool, error)
	restoreRuntime  func(config.SystemConfig) error
	restoreFirstRun func() error
	clearPending    func() error
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
		loadLastGood:    loadLastGood,
		pendingExists:   pendingConfirmationExists,
		restoreRuntime:  restoreLastGoodRuntime,
		restoreFirstRun: restoreFirstRunRuntime,
		clearPending:    clearPendingConfirmation,
	}); err != nil {
		// Only failures of the security/core dataplane reach this point. Optional
		// features are restored best-effort and reported as degraded instead of
		// taking a safe LAN/firewall/router offline.
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
		// A missing last-good configuration is a legitimate first-run state and
		// is also the deterministic recovery target for a power loss after a
		// provisional setup apply but before SQLite/auth committed. Production
		// supplies restoreFirstRun; tests that omit the hook retain the old pure
		// no-op behavior so unit tests never touch host networking.
		if h.restoreFirstRun != nil {
			if err := h.restoreFirstRun(); err != nil {
				return fmt.Errorf("restore first-run runtime: %w", err)
			}
			if pending {
				log.Printf("discarding provisional setup state without canonical last-good; first-run runtime restored")
				if err := h.clearPending(); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("clear provisional first-run state: %w", err)
				}
			}
			return nil
		}
		if pending {
			return errors.New("pending configuration exists without a recoverable last-good configuration")
		}
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
	if err := cfg.ValidateScenarioSafety(); err != nil {
		return fmt.Errorf("last-good scenario safety is invalid: %w", err)
	}

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

// restoreFirstRunRuntime exposes only the local setup plane. It deliberately
// keeps IPv4 forwarding disabled and WAN/optional services stopped until the
// wizard has atomically committed both network configuration and administrator
// credentials.
func restoreFirstRunRuntime() (retErr error) {
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = false
	cfg.WireGuard.Enabled = false
	cfg.WGClient.Enabled = false
	cfg.WiFi.Enabled = false
	cfg.Cloudflare.DDNSEnabled = false
	cfg.Cloudflare.TunnelEnabled = false
	cfg.SquidProxy.Enabled = false
	cfg.QoS.Enabled = false
	cfg.Firewall.ExtraLANs = nil

	if err := os.MkdirAll(socketDir, 0750); err != nil {
		return fmt.Errorf("create first-run runtime directory: %w", err)
	}
	generated, err := generateArtifacts(cfg)
	if err != nil {
		return fmt.Errorf("generate first-run artifacts: %w", err)
	}
	candidateDir, err := os.MkdirTemp(socketDir, "first-run-candidate-")
	if err != nil {
		return fmt.Errorf("create first-run candidate directory: %w", err)
	}
	defer os.RemoveAll(candidateDir)
	if err := os.Chmod(candidateDir, 0700); err != nil {
		return err
	}
	candidates, err := writeCandidates(candidateDir, generated)
	if err != nil {
		return err
	}
	if err := runNftFile(candidates["nftables"], true); err != nil {
		return fmt.Errorf("first-run nftables preflight: %w", err)
	}
	if err := runFixed("/usr/sbin/dnsmasq", "--test", "--conf-file="+candidates["dnsmasq"]); err != nil {
		return fmt.Errorf("first-run dnsmasq preflight: %w", err)
	}

	activated := false
	defer func() {
		if !activated {
			failClosedStartup(cfg)
		}
	}()

	// Only artifacts required by the setup plane are installed. Optional
	// credentials/configuration are intentionally not materialized before the
	// administrator opts into those services.
	for _, name := range []string{"dnsmasq", "adblock", "resolv-conf", "nftables"} {
		item := generated[name]
		if err := atomicWrite(item.path, item.data, item.mode); err != nil {
			return fmt.Errorf("install first-run %s: %w", name, err)
		}
	}
	if err := prepareDnsmasqLeaseState(); err != nil {
		return fmt.Errorf("prepare first-run DHCP lease state: %w", err)
	}

	if err := applyKernelHardening(cfg); err != nil {
		return err
	}
	if err := runFixed("/sbin/sysctl", "-w", "net.ipv4.ip_forward=0"); err != nil {
		return fmt.Errorf("disable first-run forwarding: %w", err)
	}
	_ = runFixed("/sbin/rc-service", "pppoe-wan", "stop")
	_ = removeWireGuard("wg0")
	_ = removeWireGuard("wg1")
	_ = runFixed("/sbin/rc-service", "hostapd", "stop")
	_ = runFixed("/sbin/rc-service", "inadyn", "stop")
	_ = runFixed("/sbin/rc-service", "squid", "stop")
	if err := configureRuntimeLAN(cfg); err != nil {
		return fmt.Errorf("configure first-run LAN: %w", err)
	}
	if err := runNftFile(nftRuntimePath, false); err != nil {
		return fmt.Errorf("load first-run nftables: %w", err)
	}
	if err := runFixed("/sbin/rc-service", "dnsmasq", "restart"); err != nil {
		return fmt.Errorf("start first-run dnsmasq: %w", err)
	}
	if err := verifyFirstRunRuntime(cfg); err != nil {
		return err
	}
	activated = true
	return nil
}

func verifyFirstRunRuntime(cfg config.SystemConfig) error {
	forwarding, err := runFixedOutput("/sbin/sysctl", "-n", "net.ipv4.ip_forward")
	if err != nil || strings.TrimSpace(forwarding) != "0" {
		return errors.New("first-run IPv4 forwarding is not disabled")
	}
	if err := runFixed("/usr/sbin/nft", "list", "table", "inet", "minimalrouter"); err != nil {
		return fmt.Errorf("first-run nftables table unavailable: %w", err)
	}
	if err := runFixed("/sbin/rc-service", "dnsmasq", "status"); err != nil {
		return fmt.Errorf("first-run dnsmasq unhealthy: %w", err)
	}
	output, err := runFixedOutput("/sbin/ip", "-4", "addr", "show", "dev", cfg.RuntimeLANInterface())
	if err != nil || !strings.Contains(output, "inet "+cfg.LAN.IPAddress+"/") {
		return errors.New("first-run LAN address is not active")
	}
	return nil
}

// preflightStartup checks only security/core configuration that must be proven
// before routing can start. Optional services are deliberately left out of this
// critical path: in particular an outbound WireGuard hostname must never delay
// PPPoE, because PPPoE is what provides the DNS/Internet path it may need.
func preflightStartup(cfg config.SystemConfig, candidates map[string]string) error {
	if err := runNftFile(candidates["nftables"], true); err != nil {
		return fmt.Errorf("nftables: %w", err)
	}
	if err := runFixed("/usr/sbin/dnsmasq", "--test", "--conf-file="+candidates["dnsmasq"]); err != nil {
		return fmt.Errorf("dnsmasq: %w", err)
	}
	if cfg.WAN.Enabled {
		if err := runFixed("/usr/sbin/pppd", "dryrun", "file", candidates["pppoe"]); err != nil {
			return fmt.Errorf("pppd: %w", err)
		}
	}
	if cfg.WireGuard.Enabled {
		if err := preflightWireGuard(candidates["wireguard-runtime"]); err != nil {
			if cfg.System.ManagementAccess == "wireguard_only" {
				return fmt.Errorf("management WireGuard: %w", err)
			}
			log.Printf("startup WireGuard server preflight degraded (LAN management remains available): %v", err)
		}
	}
	return nil
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
	if err := preflightStartup(cfg, candidates); err != nil {
		return fmt.Errorf("preflight last-good runtime: %w", err)
	}

	activated := false
	defer func() {
		if !activated {
			failClosedStartup(cfg)
		}
	}()
	if err := prepareDnsmasqLeaseState(); err != nil {
		return fmt.Errorf("prepare startup DHCP lease state: %w", err)
	}

	for _, name := range restoreArtifacts {
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
	// ExtraLAN is a service segment, not a prerequisite for the core home LAN.
	for _, lan := range cfg.Firewall.ExtraLANs {
		if !lan.Enabled {
			continue
		}
		single := cfg.DeepCopy()
		single.Firewall.ExtraLANs = []config.ExtraLANConfig{lan}
		if err := configureExtraLANs(single); err != nil {
			log.Printf("startup extra LAN %s degraded (non-fatal): %v", lan.Interface, err)
		}
	}
	if err := runNftFile(nftRuntimePath, false); err != nil {
		return fmt.Errorf("load startup nftables: %w", err)
	}
	// Forwarding is enabled only once the policy is loaded, so a cold boot never
	// routes through an empty ruleset.
	if err := enableIPForwarding(); err != nil {
		return err
	}
	// The generated dnsmasq configuration binds the WG server interface and
	// its tunnel address, so bring the management tunnel up before DNS.
	if err := activateWireGuard(cfg); err != nil {
		if cfg.System.ManagementAccess == "wireguard_only" {
			return fmt.Errorf("restore startup management WireGuard: %w", err)
		}
		log.Printf("startup WireGuard server degraded (LAN management remains online): %v", err)
	}
	if err := runFixed("/sbin/rc-service", "dnsmasq", "restart"); err != nil {
		return fmt.Errorf("restart startup dnsmasq: %w", err)
	}

	// PPPoE is the primary WAN dependency and must be launched before optional
	// services that may themselves need DNS/Internet. Starting it here removes
	// up to 23 seconds of avoidable wg1 DNS timeout from the cold-boot path.
	if cfg.WAN.Enabled {
		if err := runFixed("/sbin/rc-service", "pppoe-wan", "restart"); err != nil {
			log.Printf("startup PPPoE unavailable (LAN management remains online): %v", err)
		}
	} else {
		_ = runFixed("/sbin/rc-service", "pppoe-wan", "stop")
	}

	// Optional outbound WireGuard is attempted only after PPPoE has been
	// launched. Failure is degraded, never a reason to hold the primary WAN.
	if err := activateWireGuardClientTimeout(cfg, 15*time.Second); err != nil {
		log.Printf("startup WireGuard client not brought up (non-fatal): %v", err)
	}
	if cfg.QoS.Enabled {
		if err := applyQoS(cfg); err != nil {
			log.Printf("startup QoS not applied (non-fatal): %v", err)
		}
	} else {
		clearQoS(cfg)
	}
	if cfg.WiFi.Enabled {
		if err := preflightWiFi(cfg.WiFi.Interface); err != nil {
			log.Printf("startup Wi-Fi preflight degraded (wired LAN remains available): %v", err)
		} else if err := runFixed("/sbin/rc-service", "hostapd", "restart"); err != nil {
			log.Printf("startup Wi-Fi unavailable (wired LAN remains online): %v", err)
		}
	} else {
		_ = runFixed("/sbin/rc-service", "hostapd", "stop")
	}
	if cfg.Cloudflare.DDNSEnabled {
		if err := runFixed("/usr/sbin/inadyn", "--check-config", "-f", candidates["cf-ddns"]); err != nil {
			log.Printf("startup DDNS preflight degraded (non-fatal): %v", err)
			_ = runFixed("/sbin/rc-service", "inadyn", "stop")
		} else if err := secureDDNSConfiguration(); err != nil {
			log.Printf("startup DDNS unavailable: %v", err)
			_ = runFixed("/sbin/rc-service", "inadyn", "stop")
		} else if err := runFixed("/sbin/rc-service", "inadyn", "restart"); err != nil {
			log.Printf("startup DDNS unavailable (non-fatal): %v", err)
		}
	} else {
		_ = runFixed("/sbin/rc-service", "inadyn", "stop")
	}
	if cfg.SquidProxy.Enabled {
		if err := runFixed("/usr/sbin/squid", "-k", "parse", "-f", candidates["squid"]); err != nil {
			log.Printf("startup Squid preflight degraded (non-fatal): %v", err)
			_ = runFixed("/sbin/rc-service", "squid", "stop")
		} else if group, lookupErr := user.LookupGroup("squid"); lookupErr != nil {
			log.Printf("startup Squid unavailable: service group missing: %v", lookupErr)
			_ = runFixed("/sbin/rc-service", "squid", "stop")
		} else if gid, parseErr := strconv.Atoi(group.Gid); parseErr != nil || os.Chown("/etc/squid/passwd", 0, gid) != nil {
			log.Printf("startup Squid unavailable: could not secure password-file ownership")
			_ = runFixed("/sbin/rc-service", "squid", "stop")
		} else if err := runFixed("/sbin/rc-service", "squid", "restart"); err != nil {
			log.Printf("startup Squid unavailable (non-fatal): %v", err)
		}
	} else {
		if err := stopSquid(); err != nil {
			log.Printf("startup Squid stop failed: %v", err)
		}
	}
	if err := verifyStartupLocal(cfg); err != nil {
		return err
	}
	activated = true
	return nil
}

// verifyStartupLocal proves only security/core invariants. Optional components
// are inspected and logged but never decide whether LAN/firewall management is
// allowed to boot, except wg0 when management is explicitly wireguard-only.
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
		wgErr := runFixed("/usr/bin/wg", "show", interfaceName)
		addrErr := runFixed("/sbin/ip", "-4", "addr", "show", "dev", interfaceName)
		if wgErr != nil || addrErr != nil {
			if cfg.System.ManagementAccess == "wireguard_only" {
				return errors.New("startup management WireGuard is unavailable")
			}
			log.Printf("startup WireGuard server degraded (non-fatal): wg=%v address=%v", wgErr, addrErr)
		}
	}
	if cfg.WGClient.Enabled {
		interfaceName := cfg.WGClient.Interface
		if interfaceName == "" {
			interfaceName = "wg1"
		}
		if err := runFixed("/usr/bin/wg", "show", interfaceName); err != nil {
			log.Printf("startup WireGuard client interface not available (non-fatal): %v", err)
		} else if cfg.WGClient.Address != "" {
			if err := runFixed("/sbin/ip", "-4", "addr", "show", "dev", interfaceName); err != nil {
				log.Printf("startup WireGuard client address unavailable (non-fatal): %v", err)
			}
		}
	}
	for _, lan := range cfg.Firewall.ExtraLANs {
		if !lan.Enabled || lan.Interface == "" || lan.RouterAddress == "" {
			continue
		}
		addressOutput, err := runFixedOutput("/sbin/ip", "-4", "addr", "show", "dev", lan.Interface)
		if err != nil {
			log.Printf("startup extra LAN %s unavailable (non-fatal): %v", lan.Interface, err)
			continue
		}
		addressPrefix := lan.RouterAddress
		if slash := strings.IndexByte(addressPrefix, '/'); slash >= 0 {
			addressPrefix = addressPrefix[:slash]
		}
		if !strings.Contains(addressOutput, "inet "+addressPrefix+"/") {
			log.Printf("startup extra LAN %s router address unavailable (non-fatal)", lan.Interface)
		}
	}
	if cfg.WiFi.Enabled {
		if err := runFixed("/sbin/rc-service", "hostapd", "status"); err != nil {
			log.Printf("startup hostapd unhealthy (wired LAN remains available): %v", err)
		} else if bridgeLink, linkErr := os.Readlink(filepath.Join("/sys/class/net", cfg.WiFi.Interface, "master")); linkErr != nil || filepath.Base(bridgeLink) != config.WiFiBridgeInterface {
			log.Printf("startup Wi-Fi interface is not attached to LAN bridge (non-fatal)")
		}
	}
	if cfg.SquidProxy.Enabled {
		port := cfg.SquidProxy.Port
		if port == 0 {
			port = 3128
		}
		address := net.JoinHostPort(cfg.LAN.IPAddress, strconv.Itoa(port))
		conn, dialErr := net.DialTimeout("tcp", address, 2*time.Second)
		if dialErr != nil {
			log.Printf("startup Squid listener unavailable (non-fatal): %v", dialErr)
		} else {
			_ = conn.Close()
		}
		if err := runFixed("/sbin/rc-service", "squid", "status"); err != nil {
			log.Printf("startup Squid service unhealthy (non-fatal): %v", err)
		}
	}
	return nil
}

func installEmergencyFirewall() error {
	// Never delete the last known firewall while trying to fail closed. If the
	// emergency batch cannot be installed, leaving the previous table in place
	// is safer than returning the host to the kernel's default ACCEPT policy.
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		return err
	}
	path := filepath.Join(socketDir, "fail-closed.nft")
	if err := atomicWrite(path, []byte(emergencyNftables), 0600); err != nil {
		return err
	}
	return runNftFile(path, false)
}

func failClosedStartup(cfg config.SystemConfig) {
	_ = runFixed("/sbin/sysctl", "-w", "net.ipv4.ip_forward=0")
	if err := installEmergencyFirewall(); err != nil {
		log.Printf("CRITICAL: could not install emergency fail-closed firewall; preserving the existing nftables state: %v", err)
	}
	// A helper startup failure must not leave the management API alive behind
	// uncertain network state. Local console recovery remains available.
	_ = runFixed("/sbin/rc-service", "routerd", "stop")
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
}
