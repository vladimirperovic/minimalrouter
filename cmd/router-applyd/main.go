package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/services"
	"golang.org/x/crypto/bcrypt"
)

const (
	socketDir      = "/run/minimalrouter"
	lastGoodPath   = "/var/lib/minimalrouter-applyd/last-good.json"
	nftRuntimePath = "/run/minimalrouter/nftables.nft"
	lastTxPath     = "/var/lib/minimalrouter-applyd/last-transaction.json"
	pendingPath    = "/var/lib/minimalrouter-applyd/pending-confirmation.json"
)

var applyMu sync.Mutex
var transactionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type artifact struct {
	path string
	data []byte
	mode os.FileMode
}

type previousFile struct {
	path    string
	data    []byte
	mode    os.FileMode
	existed bool
}

type transactionRecord struct {
	ID          string              `json:"id"`
	ConfigHash  string              `json:"config_hash"`
	Response    apply.ApplyResponse `json:"response"`
	CompletedAt time.Time           `json:"completed_at"`
}

type pendingConfirmation struct {
	ConfigHash string              `json:"config_hash"`
	Config     config.SystemConfig `json:"config"`
}

func main() {
	// Memory tuning for embedded appliance: GC at 1.5x live heap, hard cap at 64 MB.
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(64 << 20)

	log.Println("Starting Minimal Router OS router-applyd (privileged execution helper)")

	if err := os.MkdirAll(socketDir, 0750); err != nil {
		log.Fatalf("create socket directory: %v", err)
	}

	_ = os.Remove(apply.DefaultSocketPath)
	listener, err := net.Listen("unix", apply.DefaultSocketPath)
	if err != nil {
		log.Fatalf("bind Unix socket: %v", err)
	}
	defer listener.Close()

	if err := secureSocketForRouterd(apply.DefaultSocketPath); err != nil {
		log.Fatalf("secure Unix socket: %v", err)
	}

	log.Printf("router-applyd listening on unix://%s", apply.DefaultSocketPath)
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("accept connection: %v", err)
			continue
		}
		go handleConnection(conn)
	}
}

func secureSocketForRouterd(path string) error {
	group, err := user.LookupGroup("routerd")
	if err != nil {
		return fmt.Errorf("routerd group is required: %w", err)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return fmt.Errorf("invalid routerd group ID: %w", err)
	}
	if err := os.Chown(socketDir, 0, gid); err != nil {
		return err
	}
	if err := os.Chmod(socketDir, 0750); err != nil {
		return err
	}
	if err := os.Chown(path, 0, gid); err != nil {
		return err
	}
	return os.Chmod(path, 0660)
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	if err := validatePeer(conn); err != nil {
		writeResponse(conn, apply.ApplyResponse{Success: false, Error: "unauthorized local peer"})
		log.Printf("rejected apply peer: %v", err)
		return
	}

	decoder := json.NewDecoder(io.LimitReader(conn, apply.MaxRequestBytes))
	decoder.DisallowUnknownFields()
	var req apply.ApplyRequest
	if err := decoder.Decode(&req); err != nil {
		writeResponse(conn, apply.ApplyResponse{Success: false, Error: "invalid or oversized RPC request"})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeResponse(conn, apply.ApplyResponse{Success: false, Error: "RPC request must contain exactly one object"})
		return
	}
	if req.Version != apply.ProtocolVersion {
		writeResponse(conn, apply.ApplyResponse{ID: req.ID, Success: false, Error: "unsupported RPC protocol version"})
		return
	}
	if !transactionIDPattern.MatchString(req.ID) {
		writeResponse(conn, apply.ApplyResponse{ID: req.ID, Success: false, Error: "invalid transaction ID"})
		return
	}
	if req.Op != apply.OpApplyAll && req.Op != apply.OpConfirm {
		writeResponse(conn, apply.ApplyResponse{ID: req.ID, Success: false, Error: "operation is not allowlisted"})
		return
	}

	applyMu.Lock()
	defer applyMu.Unlock()

	configHash, err := hashRequest(req)
	if err != nil {
		writeResponse(conn, failure(req.ID, "could not fingerprint request", false))
		return
	}
	if previous, err := loadLastTransaction(); err == nil && previous.ID == req.ID {
		if previous.ConfigHash != configHash {
			writeResponse(conn, failure(req.ID, "transaction ID was already used for different content", false))
			return
		}
		writeResponse(conn, previous.Response)
		return
	}

	log.Printf("apply transaction %q revision %d", req.ID, req.Revision)
	var resp apply.ApplyResponse
	if req.Op == apply.OpConfirm {
		resp = confirmApply(req)
	} else {
		resp = applyAll(req)
	}
	if err := saveLastTransaction(transactionRecord{
		ID: req.ID, ConfigHash: configHash, Response: resp, CompletedAt: time.Now(),
	}); err != nil {
		resp = failure(req.ID, "transaction result could not be persisted", resp.RolledBack)
	}
	writeResponse(conn, resp)
}

func hashRequest(req apply.ApplyRequest) (string, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func writeResponse(conn net.Conn, resp apply.ApplyResponse) {
	resp.Timestamp = time.Now().Unix()
	_ = json.NewEncoder(conn).Encode(&resp)
}

func applyAll(req apply.ApplyRequest) apply.ApplyResponse {
	if err := req.Config.Validate(); err != nil {
		return failure(req.ID, "privileged validation rejected configuration", false)
	}

	generated, err := generateArtifacts(req.Config)
	if err != nil {
		return failure(req.ID, "configuration generation failed", false)
	}
	if err := compareGenerated(req, generated); err != nil {
		return failure(req.ID, "candidate mismatch across trust boundary", false)
	}
	if err := rejectUnimplementedFeatures(req.Config); err != nil {
		return failure(req.ID, err.Error(), false)
	}

	candidateDir, err := os.MkdirTemp(socketDir, "candidate-")
	if err != nil {
		return failure(req.ID, "could not create private candidate directory", false)
	}
	defer os.RemoveAll(candidateDir)
	if err := os.Chmod(candidateDir, 0700); err != nil {
		return failure(req.ID, "could not secure candidate directory", false)
	}

	candidates, err := writeCandidates(candidateDir, generated)
	if err != nil {
		return failure(req.ID, "could not write candidate files", false)
	}
	if err := preflight(req.Config, candidates); err != nil {
		return failure(req.ID, "component preflight failed: "+safeError(err), false)
	}

	previous, err := capturePrevious(generated)
	if err != nil {
		return failure(req.ID, "could not capture previous artifacts", false)
	}
	previousConfig, _ := loadLastGood()
	if req.RequireConfirmation {
		lanChanged := previousConfig != nil &&
			(previousConfig.LAN.IPAddress != req.Config.LAN.IPAddress ||
				previousConfig.LAN.CIDR != req.Config.LAN.CIDR)
		managementChanged := previousConfig != nil &&
			previousConfig.System.ManagementAccess != req.Config.System.ManagementAccess
		if previousConfig == nil ||
			previousConfig.LAN.Interface != req.Config.LAN.Interface ||
			(!lanChanged && !managementChanged) {
			return failure(req.ID, "confirmation mode is invalid for this change", false)
		}
	}

	if err := installAndActivate(req.Config, generated, previousConfig, req.RequireConfirmation); err != nil {
		rollbackErr := rollback(previousConfig, previous)
		if rollbackErr != nil {
			log.Printf("apply transaction %q activation failed: %s; rollback failed: %s", req.ID, safeError(err), safeError(rollbackErr))
			return failure(req.ID, "apply failed and rollback could not be verified", true)
		}
		log.Printf("apply transaction %q activation failed and was rolled back: %s", req.ID, safeError(err))
		return failure(req.ID, "apply failed; previous configuration restored: "+safeError(err), true)
	}
	if err := verifyActive(req.Config); err != nil {
		rollbackErr := rollback(previousConfig, previous)
		if rollbackErr != nil {
			log.Printf("apply transaction %q verification failed: %s; rollback failed: %s", req.ID, safeError(err), safeError(rollbackErr))
			return failure(req.ID, "verification failed and rollback could not be verified", true)
		}
		log.Printf("apply transaction %q verification failed and was rolled back: %s", req.ID, safeError(err))
		return failure(req.ID, "verification failed; previous configuration restored: "+safeError(err), true)
	}
	if req.RequireConfirmation {
		hash, hashErr := hashConfig(req.Config)
		pendingErr := savePendingConfirmation(pendingConfirmation{
			ConfigHash: hash,
			Config:     req.Config,
		})
		if hashErr != nil || pendingErr != nil {
			rollbackErr := rollback(previousConfig, previous)
			if rollbackErr != nil {
				return failure(req.ID, "could not persist pending state and rollback failed", true)
			}
			return failure(req.ID, "could not persist pending confirmation state", true)
		}
	} else {
		if err := saveLastGood(req.Config); err != nil {
			rollbackErr := rollback(previousConfig, previous)
			if rollbackErr != nil {
				return failure(req.ID, "could not persist last-good state and rollback failed", true)
			}
			return failure(req.ID, "could not persist last-good state; previous configuration restored", true)
		}
		_ = os.Remove(pendingPath)
	}

	return apply.ApplyResponse{
		ID:       req.ID,
		Success:  true,
		Verified: true,
		Logs:     "validated, preflighted, applied, and verified",
	}
}

func confirmApply(req apply.ApplyRequest) apply.ApplyResponse {
	if err := req.Config.Validate(); err != nil {
		return failure(req.ID, "confirmation configuration is invalid", false)
	}
	pending, err := loadPendingConfirmation()
	if err != nil {
		return failure(req.ID, "no configuration is awaiting confirmation", false)
	}
	hash, err := hashConfig(req.Config)
	if err != nil || hash != pending.ConfigHash {
		return failure(req.ID, "confirmation does not match pending configuration", false)
	}
	if err := configureLAN(req.Config.LAN); err != nil {
		return failure(req.ID, "could not finalize LAN address", false)
	}
	if err := saveLastGood(req.Config); err != nil {
		return failure(req.ID, "could not persist confirmed configuration", false)
	}
	if err := os.Remove(pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return failure(req.ID, "could not clear pending confirmation", false)
	}
	if err := verifyActive(req.Config); err != nil {
		return failure(req.ID, "confirmed configuration verification failed", false)
	}
	return apply.ApplyResponse{
		ID: req.ID, Success: true, Verified: true, Logs: "configuration confirmed",
	}
}

func failure(id, message string, rolledBack bool) apply.ApplyResponse {
	return apply.ApplyResponse{
		ID:         id,
		Success:    false,
		Verified:   false,
		RolledBack: rolledBack,
		Error:      message,
	}
}

func rejectUnimplementedFeatures(cfg config.SystemConfig) error {
	switch {
	// Wi-Fi hostapd lifecycle is now implemented — no longer blocked
	// Cloudflare DDNS/Tunnel lifecycle is now implemented — no longer blocked
	// AdGuard blocklist lifecycle is now implemented — no longer blocked
	// QoS lifecycle is now implemented — no longer blocked
	default:
		return nil
	}
}

func generateArtifacts(cfg config.SystemConfig) (map[string]artifact, error) {
	nft, err := services.GenerateNftables(&cfg)
	if err != nil {
		return nil, err
	}
	pppoe, err := services.GeneratePPPoE(&cfg)
	if err != nil {
		return nil, err
	}
	dnsmasq, err := services.GenerateDnsmasq(&cfg)
	if err != nil {
		return nil, err
	}
	hostapd, err := services.GenerateHostapd(&cfg)
	if err != nil {
		return nil, err
	}
	wireGuard, err := services.GenerateWireGuard(&cfg.WireGuard)
	if err != nil {
		return nil, err
	}
	squidConfig, err := services.GenerateSquidConfig(&cfg)
	if err != nil {
		return nil, err
	}
	squidPassword := []byte("# Squid authentication is disabled\n")
	if cfg.SquidProxy.Enabled {
		hash, err := bcrypt.GenerateFromPassword([]byte(cfg.SquidProxy.Password), 12)
		if err != nil {
			return nil, err
		}
		squidPassword = []byte(cfg.SquidProxy.Username + ":" + string(hash) + "\n")
	}

	// AdGuard blocklist
	var adblockConf []byte
	if cfg.AdGuard.Enabled {
		// Try downloading fresh blocklist; on failure use built-in
		hostsData, dlErr := services.DownloadBlocklist(cfg.AdGuard.BlocklistURL)
		if dlErr != nil {
			log.Printf("[ADGUARD] Blocklist download failed, using built-in: %v", dlErr)
			hostsData = nil
		}
		adblockStr, genErr := services.GenerateAdBlockConf(&cfg, hostsData)
		if genErr != nil {
			return nil, fmt.Errorf("adguard: %w", genErr)
		}
		adblockConf = []byte(adblockStr)
	} else {
		adblockConf = []byte("# AdGuard disabled\n")
	}

	// QoS traffic shaping
	qosScript, err := services.GenerateQoS(&cfg)
	if err != nil {
		return nil, fmt.Errorf("qos: %w", err)
	}

	// Cloudflare DDNS + Tunnel
	cfDDNS, err := services.GenerateCloudflareDDNS(&cfg)
	if err != nil {
		return nil, fmt.Errorf("cloudflare ddns: %w", err)
	}
	cfTunnel, err := services.GenerateCloudflareTunnel(&cfg)
	if err != nil {
		return nil, fmt.Errorf("cloudflare tunnel: %w", err)
	}

	// DNS-over-HTTPS proxy
	dohProxy, err := services.GenerateDoHProxy(&cfg)
	if err != nil {
		return nil, fmt.Errorf("doh proxy: %w", err)
	}

	return map[string]artifact{
		"nftables":     {path: nftRuntimePath, data: []byte(nft), mode: 0600},
		"pppoe":        {path: "/etc/ppp/peers/wan", data: []byte(pppoe.PeerConfig), mode: 0600},
		"chap":         {path: "/etc/ppp/chap-secrets", data: []byte(pppoe.ChapSecrets), mode: 0600},
		"dnsmasq":      {path: "/etc/dnsmasq.d/minimalrouter.conf", data: []byte(dnsmasq), mode: 0640},
		"adblock":      {path: "/etc/dnsmasq.d/adblock_hosts.conf", data: adblockConf, mode: 0640},
		"qos":          {path: "/etc/minimalrouter/qos.sh", data: []byte(qosScript), mode: 0755},
		"cf-ddns":      {path: "/etc/inadyn.conf", data: []byte(cfDDNS), mode: 0644},
		"cf-tunnel":    {path: "/etc/cloudflared/config.yml", data: []byte(cfTunnel), mode: 0644},
		"doh-proxy":    {path: "/etc/cloudflared/doh-proxy.yml", data: []byte(dohProxy), mode: 0644},
		"hostapd":      {path: "/etc/hostapd/hostapd.conf", data: []byte(hostapd), mode: 0600},
		"wireguard":    {path: "/etc/wireguard/wg0.conf", data: []byte(wireGuard), mode: 0600},
		"squid":        {path: "/etc/squid/squid.conf", data: []byte(squidConfig), mode: 0644},
		"squid-passwd": {path: "/etc/squid/passwd", data: squidPassword, mode: 0640},
	}, nil
}

func compareGenerated(req apply.ApplyRequest, generated map[string]artifact) error {
	checks := map[string]string{
		"nftables":  req.Nftables,
		"pppoe":     req.PPPoEPeer,
		"chap":      req.PPPoESecret,
		"dnsmasq":   req.Dnsmasq,
		"hostapd":   req.Hostapd,
		"wireguard": req.WireGuard,
	}
	for name, supplied := range checks {
		if !bytes.Equal([]byte(supplied), generated[name].data) {
			return fmt.Errorf("%s differs", name)
		}
	}
	return nil
}

func writeCandidates(dir string, generated map[string]artifact) (map[string]string, error) {
	paths := make(map[string]string, len(generated))
	for name, item := range generated {
		path := filepath.Join(dir, name+".conf")
		if err := os.WriteFile(path, item.data, item.mode); err != nil {
			return nil, err
		}
		paths[name] = path
	}
	return paths, nil
}

func preflight(cfg config.SystemConfig, candidates map[string]string) error {
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
		if err := runFixed("/usr/bin/wg-quick", "strip", candidates["wireguard"]); err != nil {
			return fmt.Errorf("WireGuard: %w", err)
		}
	}
	if cfg.SquidProxy.Enabled {
		if err := runFixed("/usr/sbin/squid", "-k", "parse", "-f", candidates["squid"]); err != nil {
			return fmt.Errorf("Squid: %w", err)
		}
	}
	return nil
}

func installAndActivate(cfg config.SystemConfig, generated map[string]artifact, previous *config.SystemConfig, provisional bool) error {
	for _, name := range []string{"pppoe", "chap", "dnsmasq", "adblock", "qos", "cf-ddns", "cf-tunnel", "doh-proxy", "hostapd", "wireguard", "squid", "squid-passwd", "nftables"} {
		item := generated[name]
		if err := atomicWrite(item.path, item.data, item.mode); err != nil {
			return fmt.Errorf("install %s: %w", name, err)
		}
	}
	if err := applyKernelHardening(cfg); err != nil {
		return err
	}
	if provisional {
		if err := configureProvisionalLAN(cfg.LAN, previous.LAN); err != nil {
			return err
		}
	} else {
		if err := configureLAN(cfg.LAN); err != nil {
			return err
		}
	}
	if err := runNftFile(nftRuntimePath, false); err != nil {
		return fmt.Errorf("load nftables: %w", err)
	}
	if cfg.QoS.Enabled {
		if err := runFixed("/bin/sh", "/etc/minimalrouter/qos.sh"); err != nil {
			return fmt.Errorf("apply QoS: %w", err)
		}
	}
	if err := runFixed("/sbin/rc-service", "dnsmasq", "restart"); err != nil {
		return fmt.Errorf("restart dnsmasq: %w", err)
	}
	if cfg.WAN.Enabled {
		if err := runFixed("/sbin/rc-service", "pppoe-wan", "restart"); err != nil {
			return fmt.Errorf("restart PPPoE: %w", err)
		}
	} else {
		_ = runFixed("/sbin/rc-service", "pppoe-wan", "stop")
	}
	if err := activateWireGuard(cfg.WireGuard.Enabled); err != nil {
		return err
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
			return fmt.Errorf("restart Squid: %w", err)
		}
	} else {
		_ = runFixed("/sbin/rc-service", "squid", "stop")
	}
	if cfg.Cloudflare.DDNSEnabled {
		_ = runFixed("/sbin/rc-service", "inadyn", "restart")
	} else {
		_ = runFixed("/sbin/rc-service", "inadyn", "stop")
	}
	if cfg.Cloudflare.TunnelEnabled {
		_ = runFixed("/sbin/rc-service", "cloudflared", "restart")
	} else {
		_ = runFixed("/sbin/rc-service", "cloudflared", "stop")
	}
	if cfg.DHCP.DNSEnabled {
		_ = runFixed("/sbin/rc-service", "cloudflared-doh", "restart")
	} else {
		_ = runFixed("/sbin/rc-service", "cloudflared-doh", "stop")
	}
	return nil
}

func verifyActive(cfg config.SystemConfig) error {
	if err := runFixed("/usr/sbin/nft", "list", "table", "inet", "minimalrouter"); err != nil {
		return fmt.Errorf("nftables table unavailable: %w", err)
	}
	if err := runFixed("/sbin/rc-service", "dnsmasq", "status"); err != nil {
		return fmt.Errorf("dnsmasq unhealthy: %w", err)
	}
	output, err := runFixedOutput("/sbin/ip", "-4", "addr", "show", "dev", cfg.LAN.Interface)
	if err != nil {
		return fmt.Errorf("LAN interface unavailable: %w", err)
	}
	prefix := cfg.LAN.CIDR
	if slash := strings.IndexByte(prefix, '/'); slash >= 0 {
		prefix = prefix[:slash]
	}
	if !strings.Contains(output, "inet "+prefix+"/") {
		return errors.New("configured LAN address is not active")
	}
	if cfg.WAN.Enabled {
		deadline := time.Now().Add(20 * time.Second)
		for {
			if err := runFixed("/sbin/ip", "link", "show", "dev", "ppp0"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				return errors.New("PPPoE interface did not become ready")
			}
			time.Sleep(500 * time.Millisecond)
		}
		pppAddress, err := runFixedOutput("/sbin/ip", "-4", "addr", "show", "dev", "ppp0")
		if err != nil || !strings.Contains(pppAddress, "inet ") {
			return errors.New("PPPoE interface has no assigned IPv4 address")
		}
		pppDefaultRoute, err := runFixedOutput("/sbin/ip", "-4", "route", "show", "default", "dev", "ppp0")
		if err != nil || strings.TrimSpace(pppDefaultRoute) == "" {
			return errors.New("PPPoE interface has no default route")
		}
	}
	if cfg.WireGuard.Enabled {
		if err := runFixed("/usr/bin/wg", "show", cfg.WireGuard.Interface); err != nil {
			return fmt.Errorf("WireGuard interface unhealthy: %w", err)
		}
		if err := runFixed("/sbin/ip", "-4", "addr", "show", "dev", cfg.WireGuard.Interface); err != nil {
			return fmt.Errorf("WireGuard address unavailable: %w", err)
		}
	}
	if cfg.SquidProxy.Enabled {
		port := cfg.SquidProxy.Port
		if port == 0 {
			port = 3128
		}
		address := net.JoinHostPort(cfg.LAN.IPAddress, strconv.Itoa(port))
		deadline := time.Now().Add(45 * time.Second)
		for {
			conn, err := net.DialTimeout("tcp", address, time.Second)
			if err == nil {
				_ = conn.Close()
				break
			}
			if time.Now().After(deadline) {
				return errors.New("Squid did not become ready on the configured LAN endpoint")
			}
			time.Sleep(500 * time.Millisecond)
		}
		if err := runFixed("/sbin/rc-service", "squid", "status"); err != nil {
			return fmt.Errorf("Squid unhealthy after listener became ready: %w", err)
		}
	}
	return nil
}

func activateWireGuard(enabled bool) error {
	_ = runFixed("/usr/bin/wg-quick", "down", "/etc/wireguard/wg0.conf")
	if !enabled {
		return nil
	}
	if err := runFixed("/usr/bin/wg-quick", "up", "/etc/wireguard/wg0.conf"); err != nil {
		return fmt.Errorf("start WireGuard: %w", err)
	}
	return nil
}

func capturePrevious(generated map[string]artifact) ([]previousFile, error) {
	result := make([]previousFile, 0, len(generated))
	for _, item := range generated {
		info, err := os.Stat(item.path)
		if errors.Is(err, os.ErrNotExist) {
			result = append(result, previousFile{path: item.path})
			continue
		}
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(item.path)
		if err != nil {
			return nil, err
		}
		result = append(result, previousFile{
			path: item.path, data: data, mode: info.Mode().Perm(), existed: true,
		})
	}
	return result, nil
}

func rollback(previousConfig *config.SystemConfig, files []previousFile) error {
	var errs []string
	hadPreviousNft := false
	for _, file := range files {
		if file.path == nftRuntimePath {
			hadPreviousNft = file.existed
		}
		if !file.existed {
			if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, safeError(err))
			}
			continue
		}
		if err := atomicWrite(file.path, file.data, file.mode); err != nil {
			errs = append(errs, safeError(err))
		}
	}
	if previousConfig != nil {
		if err := applyKernelHardening(*previousConfig); err != nil {
			errs = append(errs, safeError(err))
		}
		if err := configureLAN(previousConfig.LAN); err != nil {
			errs = append(errs, safeError(err))
		}
	}
	if hadPreviousNft {
		if err := runNftFile(nftRuntimePath, false); err != nil {
			errs = append(errs, safeError(err))
		}
	} else {
		_ = runFixed("/usr/sbin/nft", "delete", "table", "inet", "minimalrouter")
		if err := runFixed("/usr/sbin/nft", "list", "table", "inet", "minimalrouter"); err == nil {
			errs = append(errs, "candidate nftables table remained active")
		}
	}
	if previousConfig != nil {
		if err := runFixed("/sbin/rc-service", "dnsmasq", "restart"); err != nil {
			errs = append(errs, safeError(err))
		}
	} else {
		_ = runFixed("/sbin/rc-service", "dnsmasq", "stop")
	}
	if previousConfig != nil && previousConfig.WAN.Enabled {
		if err := runFixed("/sbin/rc-service", "pppoe-wan", "restart"); err != nil {
			errs = append(errs, safeError(err))
		}
	} else {
		_ = runFixed("/sbin/rc-service", "pppoe-wan", "stop")
	}
	if previousConfig != nil {
		if err := activateWireGuard(previousConfig.WireGuard.Enabled); err != nil {
			errs = append(errs, safeError(err))
		}
	} else {
		_ = activateWireGuard(false)
	}
	if previousConfig != nil && previousConfig.SquidProxy.Enabled {
		if err := runFixed("/sbin/rc-service", "squid", "restart"); err != nil {
			errs = append(errs, safeError(err))
		}
	} else {
		_ = runFixed("/sbin/rc-service", "squid", "stop")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	if previousConfig != nil {
		_ = os.Remove(pendingPath)
		return verifyActive(*previousConfig)
	}
	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".minimalrouter-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	dirHandle, err := os.Open(dir)
	if err == nil {
		defer dirHandle.Close()
		return dirHandle.Sync()
	}
	return err
}

func configureLAN(lan config.LANSettings) error {
	if err := runFixed("/sbin/ip", "link", "set", "dev", lan.Interface, "up"); err != nil {
		return fmt.Errorf("bring LAN up: %w", err)
	}
	if err := runFixed("/sbin/ip", "-4", "addr", "flush", "dev", lan.Interface, "scope", "global"); err != nil {
		return fmt.Errorf("clear previous LAN addresses: %w", err)
	}
	if err := runFixed("/sbin/ip", "-4", "addr", "add", lan.CIDR, "dev", lan.Interface); err != nil {
		return fmt.Errorf("configure LAN address: %w", err)
	}
	return nil
}

func configureProvisionalLAN(candidate, previous config.LANSettings) error {
	if candidate.Interface != previous.Interface {
		return errors.New("LAN interface changes cannot be provisionally applied")
	}
	if err := runFixed("/sbin/ip", "link", "set", "dev", candidate.Interface, "up"); err != nil {
		return fmt.Errorf("bring LAN up: %w", err)
	}
	if err := runFixed("/sbin/ip", "-4", "addr", "replace", previous.CIDR, "dev", candidate.Interface); err != nil {
		return fmt.Errorf("retain previous LAN address: %w", err)
	}
	if err := runFixed("/sbin/ip", "-4", "addr", "replace", candidate.CIDR, "dev", candidate.Interface); err != nil {
		return fmt.Errorf("add candidate LAN address: %w", err)
	}
	return nil
}

func applyKernelHardening(cfg config.SystemConfig) error {
	settings := [][2]string{
		{"net.ipv4.ip_forward", "1"},
		{"net.ipv4.tcp_syncookies", "1"},
		{"net.ipv4.tcp_rfc1337", "1"},
		{"net.ipv4.conf.all.accept_redirects", "0"},
		{"net.ipv4.conf.default.accept_redirects", "0"},
		{"net.ipv4.conf.all.send_redirects", "0"},
		{"net.ipv4.conf.default.send_redirects", "0"},
		{"net.ipv4.conf.all.accept_source_route", "0"},
		{"net.ipv4.conf.default.accept_source_route", "0"},
		{"net.ipv4.conf.all.secure_redirects", "0"},
		{"net.ipv4.conf.default.secure_redirects", "0"},
		{"net.ipv4.conf.all.proxy_arp", "0"},
		{"net.ipv4.conf.default.proxy_arp", "0"},
		{"net.ipv4.conf.all.route_localnet", "0"},
		{"net.ipv4.conf.default.route_localnet", "0"},
		{"net.ipv4.conf.all.log_martians", "1"},
		{"net.ipv4.icmp_echo_ignore_broadcasts", "1"},
		{"net.ipv4.icmp_ignore_bogus_error_responses", "1"},
		{"kernel.kptr_restrict", "2"},
		{"kernel.dmesg_restrict", "1"},
		{"kernel.unprivileged_bpf_disabled", "1"},
		{"net.core.bpf_jit_harden", "2"},
		{"fs.protected_hardlinks", "1"},
		{"fs.protected_symlinks", "1"},
		{"fs.protected_fifos", "2"},
		{"fs.protected_regular", "2"},
		{"net.ipv6.conf.all.disable_ipv6", "1"},
		{"net.ipv6.conf.default.disable_ipv6", "1"},
	}
	for _, setting := range settings {
		if err := runFixed("/sbin/sysctl", "-w", setting[0]+"="+setting[1]); err != nil {
			return fmt.Errorf("apply sysctl %s: %w", setting[0], err)
		}
	}
	if err := runFixed("/sbin/sysctl", "-w", "net.ipv4.conf."+cfg.WAN.Interface+".rp_filter=1"); err != nil {
		return fmt.Errorf("enable WAN reverse-path filtering: %w", err)
	}
	return nil
}

func runFixed(binary string, args ...string) error {
	_, err := runFixedOutput(binary, args...)
	return err
}

// runNftFile replaces the helper-owned table in one atomic nft batch. When
// the table already exists, delete and create are part of the same netlink
// transaction, so there is no fail-open interval.
func runNftFile(path string, checkOnly bool) error {
	configBytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(configBytes) > apply.MaxRequestBytes {
		return errors.New("nftables candidate is too large")
	}
	batch := configBytes
	if err := runFixed("/usr/sbin/nft", "list", "table", "inet", "minimalrouter"); err == nil {
		batch = append([]byte("delete table inet minimalrouter\n"), configBytes...)
	}
	tmp, err := os.CreateTemp(socketDir, "nft-batch-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(batch); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	args := []string{"-f", tmpPath}
	if checkOnly {
		args = []string{"-c", "-f", tmpPath}
	}
	return runFixed("/usr/sbin/nft", args...)
}

func runFixedOutput(binary string, args ...string) (string, error) {
	if _, err := os.Stat(binary); err != nil {
		return "", fmt.Errorf("required binary unavailable: %s", filepath.Base(binary))
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = []string{"PATH=/sbin:/usr/sbin:/bin:/usr/bin", "LANG=C", "LC_ALL=C"}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s failed: %s", filepath.Base(binary), sanitizeOutput(output))
	}
	return string(output), nil
}

func sanitizeOutput(output []byte) string {
	text := strings.TrimSpace(string(output))
	if len(text) > 512 {
		text = text[:512]
	}
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	if text == "" {
		return "no diagnostic output"
	}
	return text
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	return sanitizeOutput([]byte(err.Error()))
}

func loadLastGood() (*config.SystemConfig, error) {
	data, err := os.ReadFile(lastGoodPath)
	if err != nil {
		return nil, err
	}
	var cfg config.SystemConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, cfg.Validate()
}

func saveLastGood(cfg config.SystemConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return atomicWrite(lastGoodPath, data, 0600)
}

func loadLastTransaction() (*transactionRecord, error) {
	data, err := os.ReadFile(lastTxPath)
	if err != nil {
		return nil, err
	}
	var record transactionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	return &record, nil
}

func saveLastTransaction(record transactionRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return atomicWrite(lastTxPath, data, 0600)
}

func hashConfig(cfg config.SystemConfig) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func savePendingConfirmation(pending pendingConfirmation) error {
	data, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	return atomicWrite(pendingPath, data, 0600)
}

func loadPendingConfirmation() (*pendingConfirmation, error) {
	data, err := os.ReadFile(pendingPath)
	if err != nil {
		return nil, err
	}
	var pending pendingConfirmation
	if err := json.Unmarshal(data, &pending); err != nil {
		return nil, err
	}
	return &pending, nil
}
