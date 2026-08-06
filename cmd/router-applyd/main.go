package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
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
	socketDir            = "/run/minimalrouter"
	lastGoodPath         = "/var/lib/minimalrouter-applyd/last-good.json"
	nftRuntimePath       = "/run/minimalrouter/nftables.nft"
	wireGuardConfigPath  = "/run/minimalrouter/wg0.conf"
	wireGuardRuntimePath = "/run/minimalrouter/wg0.runtime.conf"
	// wireGuardClientRuntimePath carries the outbound tunnel (wg1) peer config.
	// The interface is created, addressed and routed by the same activation
	// code path used for wg0, without wg-quick or shell hooks.
	wireGuardClientRuntimePath = "/run/minimalrouter/wg1.runtime.conf"
	lastTxPath                 = "/var/lib/minimalrouter-applyd/last-transaction.json"
	pendingPath                = "/var/lib/minimalrouter-applyd/pending-confirmation.json"
)

var applyMu sync.Mutex
var lastTransactionMemory *transactionRecord
var transactionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var interfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,14}$`)

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
	Response    apply.ApplyResponse `json:"response,omitempty"`
	StartedAt   time.Time           `json:"started_at"`
	CompletedAt time.Time           `json:"completed_at,omitempty"`
}

type pendingConfirmation struct {
	ConfigHash string              `json:"config_hash"`
	Config     config.SystemConfig `json:"config"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--reapply-qos" {
		// Legacy installers used to launch a second privileged helper from the
		// PPP ip-up hook. QoS reapply is now serialized through SIGUSR1 on the
		// long-running helper. Keep this argv only as a harmless compatibility
		// sink so a stale hook cannot mutate tc concurrently with a transaction.
		log.Printf("deprecated --reapply-qos invocation ignored; install the signal-based PPP hook")
		return
	}

	// Memory tuning for embedded appliance: GC at 1.5x live heap, hard cap at 64 MB.
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(64 << 20)
	if err := hardenProcess(); err != nil {
		log.Fatalf("applyd process hardening failed: %v", err)
	}

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
	available := make(chan struct{}, 8)
	for {
		available <- struct{}{}
		conn, err := listener.Accept()
		if err != nil {
			<-available
			log.Printf("accept connection: %v", err)
			continue
		}
		go func() {
			defer func() { <-available }()
			handleConnection(conn)
		}()
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

	// A compromised routerd could otherwise hold RPC connections open and
	// exhaust the helper's descriptor budget with idle sockets.
	if err := conn.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return
	}

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
	switch req.Op {
	case apply.OpApplyAll, apply.OpConfirm, apply.OpCommitConfirmed, apply.OpReconcile, apply.OpWGTunnelStatus:
	default:
		writeResponse(conn, apply.ApplyResponse{ID: req.ID, Success: false, Error: "operation is not allowlisted"})
		return
	}

	// Telemetry queries are read-only and non-transactional: no transaction
	// intent is persisted and no replay state is consulted.
	if req.Op == apply.OpWGTunnelStatus {
		writeResponse(conn, wgTunnelStatus(req))
		return
	}

	applyMu.Lock()
	defer applyMu.Unlock()

	configHash, err := hashRequest(req)
	if err != nil {
		writeResponse(conn, failure(req.ID, "could not fingerprint request", false))
		return
	}
	canonicalReconcile := req.Op == apply.OpReconcile
	if lastTransactionMemory != nil {
		if replay, handled := replayTransactionResponseWithOverride(req.ID, configHash, lastTransactionMemory, nil, canonicalReconcile); handled {
			writeResponse(conn, *replay)
			return
		}
	}
	previous, loadErr := loadLastTransaction()
	if replay, handled := replayTransactionResponseWithOverride(req.ID, configHash, previous, loadErr, canonicalReconcile); handled {
		writeResponse(conn, *replay)
		return
	}

	intent := transactionRecord{
		ID: req.ID, ConfigHash: configHash, StartedAt: time.Now(),
	}
	if err := saveLastTransaction(intent); err != nil {
		writeResponse(conn, failure(req.ID, "privileged transaction intent could not be persisted", false))
		return
	}
	lastTransactionMemory = &intent

	log.Printf("apply transaction %q revision %d", req.ID, req.Revision)
	var resp apply.ApplyResponse
	switch req.Op {
	case apply.OpConfirm:
		resp = confirmApply(req)
	case apply.OpCommitConfirmed:
		resp = commitConfirmedApply(req)
	default:
		resp = applyAll(req)
	}
	record := transactionRecord{
		ID: req.ID, ConfigHash: configHash, Response: resp,
		StartedAt: intent.StartedAt, CompletedAt: time.Now(),
	}
	record, resp = persistTransactionOutcome(record, saveLastTransaction)
	lastTransactionMemory = &record
	writeResponse(conn, resp)
}

// wgTunnelStatus answers the read-only telemetry query with a sanitized dump
// projection. The raw `wg show <if> dump` output carries private and
// preshared keys; only endpoint, handshake epoch, and transfer counters are
// returned across the privilege boundary.
func wgTunnelStatus(req apply.ApplyRequest) apply.ApplyResponse {
	interfaceName := strings.TrimSpace(req.TunnelInterface)
	serverInterface := req.Config.WireGuard.Interface
	if serverInterface == "" {
		serverInterface = "wg0"
	}
	clientInterface := req.Config.WGClient.Interface
	if clientInterface == "" {
		clientInterface = "wg1"
	}
	if interfaceName == "" {
		interfaceName = clientInterface
	}
	if !interfaceNamePattern.MatchString(interfaceName) || (interfaceName != serverInterface && interfaceName != clientInterface) {
		return failure(req.ID, "invalid WireGuard interface name", false)
	}
	out, err := runFixedOutput("/usr/bin/wg", "show", interfaceName, "dump")
	if err != nil {
		return failure(req.ID, "WireGuard status unavailable: "+safeError(err), false)
	}
	return apply.ApplyResponse{
		ID: req.ID, Success: true, Verified: true,
		Logs: "sanitized WireGuard tunnel status", TunnelStatus: parseWGTunnelStatus(interfaceName, out),
	}
}

// parseWGTunnelStatus projects a `wg show <if> dump` to the status fields
// that may cross the privilege boundary. fields[0] of a peer line is the
// public key and is deliberately never copied; the first line contains the
// interface's private key and is skipped entirely.
func parseWGTunnelStatus(interfaceName, dump string) *apply.TunnelStatus {
	status := &apply.TunnelStatus{Interface: interfaceName}
	for _, line := range strings.Split(dump, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 || fields[0] == "interface" {
			continue
		}
		status.Endpoint = fields[2]
		status.LastHandshake, _ = strconv.ParseInt(fields[4], 10, 64)
		status.RxBytes, _ = strconv.ParseInt(fields[5], 10, 64)
		status.TxBytes, _ = strconv.ParseInt(fields[6], 10, 64)
		break
	}
	return status
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

type runtimeVerificationPlan struct {
	WAN       bool
	WireGuard bool
	ExtraLAN  bool
	WiFi      bool
	DDNS      bool
	Squid     bool
	WGClient  bool
}

func verificationPlan(op apply.OperationType, previous *config.SystemConfig, candidate config.SystemConfig) runtimeVerificationPlan {
	plan := runtimeVerificationPlan{
		WireGuard: candidate.WireGuard.Enabled && candidate.System.ManagementAccess == "wireguard_only",
	}
	if op == apply.OpReconcile || op == apply.OpCommitConfirmed || op == apply.OpConfirm {
		return plan
	}
	if previous == nil {
		plan.WAN = candidate.WAN.Enabled
		plan.WireGuard = candidate.WireGuard.Enabled
		plan.ExtraLAN = len(candidate.Firewall.ExtraLANs) > 0
		plan.WiFi = candidate.WiFi.Enabled
		plan.DDNS = candidate.Cloudflare.DDNSEnabled
		plan.Squid = candidate.SquidProxy.Enabled
		plan.WGClient = candidate.WGClient.Enabled
		return plan
	}
	plan.WAN = candidate.WAN.Enabled && !reflect.DeepEqual(candidate.WAN, previous.WAN)
	plan.WireGuard = plan.WireGuard || !reflect.DeepEqual(candidate.WireGuard, previous.WireGuard)
	plan.ExtraLAN = !reflect.DeepEqual(candidate.Firewall.ExtraLANs, previous.Firewall.ExtraLANs)
	plan.WiFi = !reflect.DeepEqual(candidate.WiFi, previous.WiFi)
	plan.DDNS = !reflect.DeepEqual(candidate.Cloudflare, previous.Cloudflare)
	plan.Squid = !reflect.DeepEqual(candidate.SquidProxy, previous.SquidProxy)
	plan.WGClient = !reflect.DeepEqual(candidate.WGClient, previous.WGClient)
	return plan
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
	loadedPrevious, previousErr := loadLastGood()
	previousConfig, previousErr := normalizeLastGood(loadedPrevious, previousErr)
	if previousErr != nil {
		return recoveryFailure(req.ID, "last-good configuration could not be read; canonical reconciliation is required")
	}
	plan := verificationPlan(req.Op, previousConfig, req.Config)
	if err := preflight(req.Config, candidates, plan); err != nil {
		return failure(req.ID, "component preflight failed: "+safeError(err), false)
	}

	previous, err := capturePrevious(generated)
	if err != nil {
		return failure(req.ID, "could not capture previous artifacts", false)
	}
	if req.RequireConfirmation && !confirmationModeAllowed(previousConfig, req.Config) {
		return failure(req.ID, "confirmation mode is invalid for this change", false)
	}

	if err := installAndActivate(req.Config, generated, previousConfig, req.RequireConfirmation); err != nil {
		rollbackErr := rollback(previousConfig, &req.Config, previous)
		if rollbackErr != nil {
			log.Printf("apply transaction %q activation failed: %s; rollback failed: %s", req.ID, safeError(err), safeError(rollbackErr))
			return recoveryFailure(req.ID, "apply failed and rollback could not be verified")
		}
		log.Printf("apply transaction %q activation failed and was rolled back: %s", req.ID, safeError(err))
		return failure(req.ID, "apply failed; previous configuration restored: "+safeError(err), true)
	}
	if err := verifyActive(req.Config, plan); err != nil {
		rollbackErr := rollback(previousConfig, &req.Config, previous)
		if rollbackErr != nil {
			log.Printf("apply transaction %q verification failed: %s; rollback failed: %s", req.ID, safeError(err), safeError(rollbackErr))
			return recoveryFailure(req.ID, "verification failed and rollback could not be verified")
		}
		log.Printf("apply transaction %q verification failed and was rolled back: %s", req.ID, safeError(err))
		return failure(req.ID, "verification failed; previous configuration restored: "+safeError(err), true)
	}
	if requiresFunctionalDNSVerification(previousConfig, req.Config) {
		if err := verifyFunctionalDNS(); err != nil {
			rollbackErr := rollback(previousConfig, &req.Config, previous)
			if rollbackErr != nil {
				log.Printf("apply transaction %q functional DNS verification failed: %s; rollback failed: %s", req.ID, safeError(err), safeError(rollbackErr))
				return recoveryFailure(req.ID, "functional DNS verification failed and rollback could not be verified")
			}
			log.Printf("apply transaction %q functional DNS verification failed and was rolled back: %s", req.ID, safeError(err))
			return failure(req.ID, "functional DNS verification failed; previous configuration restored: "+safeError(err), true)
		}
	}
	if requiresDDNSVerification(previousConfig, req.Config) {
		if err := verifyDDNSUpdate(); err != nil {
			rollbackErr := rollback(previousConfig, &req.Config, previous)
			if rollbackErr != nil {
				log.Printf("apply transaction %q dynamic DNS verification failed: %s; rollback failed: %s", req.ID, safeError(err), safeError(rollbackErr))
				return recoveryFailure(req.ID, "dynamic DNS verification failed and rollback could not be verified")
			}
			log.Printf("apply transaction %q dynamic DNS verification failed and was rolled back: %s", req.ID, safeError(err))
			return failure(req.ID, "dynamic DNS verification failed; previous configuration restored: "+safeError(err), true)
		}
	}
	if req.RequireConfirmation || req.DeferLastGood {
		hash, hashErr := hashConfig(req.Config)
		pendingErr := savePendingConfirmation(pendingConfirmation{
			ConfigHash: hash,
			Config:     req.Config,
		})
		if hashErr != nil || pendingErr != nil {
			rollbackErr := rollback(previousConfig, &req.Config, previous)
			if rollbackErr != nil {
				return recoveryFailure(req.ID, "could not persist pending state and rollback failed")
			}
			return failure(req.ID, "could not persist pending confirmation state", true)
		}
	} else {
		if err := saveLastGood(req.Config); err != nil {
			rollbackErr := rollback(previousConfig, &req.Config, previous)
			if rollbackErr != nil {
				return recoveryFailure(req.ID, "could not persist last-good state and rollback failed")
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
		return pendingLoadFailure(req.ID, err)
	}
	hash, err := hashConfig(req.Config)
	if err != nil || hash != pending.ConfigHash {
		return failure(req.ID, "confirmation does not match pending configuration", false)
	}
	if err := configureRuntimeLAN(req.Config); err != nil {
		return recoveryFailure(req.ID, "could not finalize LAN address; verified rollback is required")
	}
	if err := verifyActive(req.Config, verificationPlan(apply.OpConfirm, nil, req.Config)); err != nil {
		return recoveryFailure(req.ID, "confirmed runtime verification failed; verified rollback is required")
	}
	if req.Config.WGClient.Enabled {
		// Confirm must prove the outbound tunnel actually works: a wrong
		// remote key, endpoint, or mis-scoped network must never commit from
		// a handshake-less interface.
		if err := verifyWGClientHandshake(req.Config.WGClient.Interface, req.Config.WGClient.PersistentKeepalive); err != nil {
			return recoveryFailure(req.ID, "confirmed WireGuard client tunnel is not functional: "+safeError(err)+"; verified rollback is required")
		}
	}
	return apply.ApplyResponse{
		ID: req.ID, Success: true, Verified: true, Logs: "runtime confirmation verified; canonical commit pending",
	}
}

// verifyWGClientHandshake requires a completed and, when keepalive is
// configured, recently refreshed wg1 handshake. A keepalive-0 tunnel may sit
// idle indefinitely, so any completed handshake is accepted for it.
func verifyWGClientHandshake(interfaceName string, keepalive int) error {
	if interfaceName == "" {
		interfaceName = "wg1"
	}
	out, err := runFixedOutput("/usr/bin/wg", "show", interfaceName, "dump")
	if err != nil {
		return fmt.Errorf("WireGuard client interface unavailable: %w", err)
	}
	return wgHandshakeFresh(out, keepalive)
}

// wgHandshakeFresh inspects `wg show <if> dump` output: line 1 is the
// interface, each peer line has latest-handshake as field 5 (index 4).
func wgHandshakeFresh(dump string, keepalive int) error {
	for _, line := range strings.Split(dump, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 || fields[0] == "interface" {
			continue
		}
		ts, parseErr := strconv.ParseInt(fields[4], 10, 64)
		if parseErr != nil || ts <= 0 {
			return errors.New("WireGuard client tunnel has no completed handshake")
		}
		if keepalive > 0 {
			maxAge := 3 * time.Minute
			if age := time.Duration(keepalive) * 3 * time.Second; age > maxAge {
				maxAge = age
			}
			if lastSeen := time.Since(time.Unix(ts, 0)); lastSeen > maxAge {
				return fmt.Errorf("WireGuard client handshake is stale (last %s ago)", lastSeen.Round(time.Second))
			}
		}
		return nil
	}
	return errors.New("WireGuard client tunnel has no peer")
}

func commitConfirmedApply(req apply.ApplyRequest) apply.ApplyResponse {
	if err := req.Config.Validate(); err != nil {
		return failure(req.ID, "confirmed commit configuration is invalid", false)
	}
	pending, err := loadPendingConfirmation()
	if err != nil {
		return pendingLoadFailure(req.ID, err)
	}
	hash, err := hashConfig(req.Config)
	if err != nil || hash != pending.ConfigHash {
		return failure(req.ID, "confirmed commit does not match pending configuration", false)
	}
	// The candidate was already verified when it was applied. Confirmation and
	// canonical acknowledgement must prove the local management/core runtime,
	// not depend on an ISP that may flap after the operator confirmed access.
	if err := verifyActive(req.Config, verificationPlan(apply.OpCommitConfirmed, nil, req.Config)); err != nil {
		return recoveryFailure(req.ID, "confirmed runtime is no longer active; canonical reconciliation is required")
	}
	if err := saveLastGood(req.Config); err != nil {
		return recoveryFailure(req.ID, "could not persist canonical last-good configuration")
	}
	if err := os.Remove(pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return recoveryFailure(req.ID, "could not clear pending confirmation; canonical reconciliation is required")
	}
	return apply.ApplyResponse{
		ID: req.ID, Success: true, Verified: true, Logs: "confirmed configuration committed as canonical last-good",
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
	case cfg.Cloudflare.TunnelEnabled:
		return errors.New("Cloudflare Tunnel is unavailable because WireGuard is the only allowed remote-entry path")
	case cfg.DHCP.DNSEnabled:
		return errors.New("DNS-over-HTTPS has no verified Alpine 3.22 runtime adapter")
	case len(cfg.AdGuard.FilterDevices) > 0:
		return errors.New("per-device DNS filtering is unsupported because dnsmasq address rules are global")
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
	wireGuardRuntime, err := services.GenerateWireGuardRuntime(&cfg.WireGuard)
	if err != nil {
		return nil, err
	}
	wireGuardClientRuntime := []byte("# WireGuard client disabled\n")
	if cfg.WGClient.Enabled {
		generated, genErr := services.GenerateWireGuardClientRuntime(&cfg.WGClient)
		if genErr != nil {
			return nil, genErr
		}
		wireGuardClientRuntime = []byte(generated)
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
		// Root never fetches network-controlled configuration. The hardened
		// pilot uses the reviewed built-in global list only.
		adblockStr, genErr := services.GenerateAdBlockConf(&cfg, nil)
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

	// Router's own resolver is its local dnsmasq; upstreams and the nftables
	// output allow-list are both derived from cfg.DHCP.DNSServers, so pointing
	// resolv.conf at 127.0.0.1 keeps router-local lookups working even when the
	// ISP pushes broken nameservers via usepeerdns.
	resolvConf := "nameserver 127.0.0.1\n"

	return map[string]artifact{
		"nftables":  {path: nftRuntimePath, data: []byte(nft), mode: 0600},
		"pppoe":     {path: "/etc/ppp/peers/wan", data: []byte(pppoe.PeerConfig), mode: 0600},
		"chap":      {path: "/etc/ppp/chap-secrets", data: []byte(pppoe.ChapSecrets), mode: 0600},
		"dnsmasq":   {path: "/etc/dnsmasq.d/minimalrouter.conf", data: []byte(dnsmasq), mode: 0640},
		"adblock":   {path: "/etc/dnsmasq.d/adblock_hosts.conf", data: adblockConf, mode: 0640},
		"qos":       {path: "/etc/minimalrouter/qos.plan", data: []byte(qosScript), mode: 0600},
		"cf-ddns":   {path: "/etc/inadyn/inadyn.conf", data: []byte(cfDDNS), mode: 0640},
		"cf-tunnel": {path: "/etc/cloudflared/config.yml", data: []byte(cfTunnel), mode: 0600},
		"doh-proxy": {path: "/etc/cloudflared/doh-proxy.yml", data: []byte(dohProxy), mode: 0600},
		"hostapd":   {path: "/etc/hostapd/hostapd.conf", data: []byte(hostapd), mode: 0600},
		"wireguard": {path: wireGuardConfigPath, data: []byte(wireGuard), mode: 0600},
		"wireguard-runtime": {
			path: wireGuardRuntimePath,
			data: []byte(wireGuardRuntime),
			mode: 0600,
		},
		"wireguard-client-runtime": {
			path: wireGuardClientRuntimePath,
			data: wireGuardClientRuntime,
			mode: 0600,
		},
		"squid":        {path: "/etc/squid/squid.conf", data: []byte(squidConfig), mode: 0644},
		"squid-passwd": {path: "/etc/squid/passwd", data: squidPassword, mode: 0640},
		"resolv-conf":  {path: "/etc/resolv.conf", data: []byte(resolvConf), mode: 0644},
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

func preflight(cfg config.SystemConfig, candidates map[string]string, plan runtimeVerificationPlan) error {
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
			if plan.WireGuard {
				return fmt.Errorf("WireGuard: %w", err)
			}
			log.Printf("WireGuard preflight degraded (non-fatal for unrelated change): %v", err)
		}
	}
	if cfg.WGClient.Enabled {
		if err := preflightWireGuard(candidates["wireguard-client-runtime"]); err != nil {
			if plan.WGClient {
				return fmt.Errorf("WireGuard client: %w", err)
			}
			log.Printf("WireGuard client preflight degraded (non-fatal for unrelated change): %v", err)
		}
	}
	if cfg.WiFi.Enabled {
		if err := preflightWiFi(cfg.WiFi.Interface); err != nil {
			if plan.WiFi {
				return fmt.Errorf("Wi-Fi: %w", err)
			}
			log.Printf("Wi-Fi preflight degraded (non-fatal for unrelated change): %v", err)
		}
	}
	if cfg.Cloudflare.DDNSEnabled {
		if err := runFixed("/usr/sbin/inadyn", "--check-config", "-f", candidates["cf-ddns"]); err != nil {
			if plan.DDNS {
				return fmt.Errorf("Cloudflare DDNS: %w", err)
			}
			log.Printf("DDNS preflight degraded (non-fatal for unrelated change): %v", err)
		}
	}
	if cfg.SquidProxy.Enabled {
		if err := runFixed("/usr/sbin/squid", "-k", "parse", "-f", candidates["squid"]); err != nil {
			if plan.Squid {
				return fmt.Errorf("Squid: %w", err)
			}
			log.Printf("Squid preflight degraded (non-fatal for unrelated change): %v", err)
		}
	}
	return nil
}

// restoreArtifacts is the canonical write order for generated configuration
// artifacts on both install-apply and startup reconcile. One shared list
// prevents a reboot from restoring a partial runtime (for example activating
// the outbound tunnel without its peer configuration, which would fail closed
// an otherwise healthy appliance).
var restoreArtifacts = []string{
	"pppoe", "chap", "dnsmasq", "adblock", "qos", "cf-ddns",
	"cf-tunnel", "doh-proxy", "hostapd", "wireguard",
	"wireguard-runtime", "wireguard-client-runtime",
	"squid", "squid-passwd", "resolv-conf", "nftables",
}

func installAndActivate(cfg config.SystemConfig, generated map[string]artifact, previous *config.SystemConfig, provisional bool) error {
	dnsmasqChanged, err := dnsmasqArtifactsChanged(generated, previous)
	if err != nil {
		return err
	}
	for _, name := range restoreArtifacts {
		item := generated[name]
		if err := atomicWrite(item.path, item.data, item.mode); err != nil {
			return fmt.Errorf("install %s: %w", name, err)
		}
	}
	if err := applyKernelHardening(cfg); err != nil {
		return err
	}
	// Determine deltas to avoid unnecessary network drops. Unchanged
	// subsystems must not be torn down: an unrelated Save must never drop
	// wg0/wg1 sessions or restart Squid/DDNS.
	wifiChanged := previous == nil || !reflect.DeepEqual(cfg.WiFi, previous.WiFi)
	wanChanged := previous == nil || !reflect.DeepEqual(cfg.WAN, previous.WAN)
	wgChanged := previous == nil || !reflect.DeepEqual(cfg.WireGuard, previous.WireGuard)
	wgClientChanged := previous == nil || !reflect.DeepEqual(cfg.WGClient, previous.WGClient)
	ddnsChanged := previous == nil || !reflect.DeepEqual(cfg.Cloudflare, previous.Cloudflare)
	squidChanged := previous == nil || !reflect.DeepEqual(cfg.SquidProxy, previous.SquidProxy)
	extraLANChanged := previous == nil || !reflect.DeepEqual(cfg.Firewall.ExtraLANs, previous.Firewall.ExtraLANs)

	// A running AP owns the wireless bridge membership. Stop it before any LAN
	// topology change so enabling, disabling, and rollback are deterministic.
	if wifiChanged {
		_ = runFixed("/sbin/rc-service", "hostapd", "stop")
	}
	if provisional {
		if err := configureProvisionalRuntimeLAN(cfg, *previous); err != nil {
			return err
		}
	} else {
		if err := configureRuntimeLAN(cfg); err != nil {
			return err
		}
	}
	if extraLANChanged {
		if err := configureExtraLANs(cfg); err != nil {
			return err
		}
	}
	// A segment disabled or removed in this change keeps its kernel address
	// and connected route unless explicitly flushed: the firewall rules are
	// already gone, so the stale segment would otherwise keep routing.
	if previous != nil && extraLANChanged {
		if err := cleanRemovedExtraLANs(*previous, cfg); err != nil {
			return err
		}
	}
	if err := runNftFile(nftRuntimePath, false); err != nil {
		return fmt.Errorf("load nftables: %w", err)
	}
	// QoS attaches to ppp0, which only exists after PPPoE negotiates. It is a
	// traffic-shaping optimization, not a security boundary: a failed tc attach
	// must never fail an apply or roll back a valid config, or a slow ISP
	// handshake would block routing at every boot.
	if cfg.QoS.Enabled {
		if err := applyQoS(cfg); err != nil {
			log.Printf("apply QoS not applied (non-fatal): %v", err)
		}
	} else {
		clearQoS(cfg)
	}

	// Bring management tunnels up before dnsmasq restarts. dnsmasq uses
	// bind-dynamic as a second defense, but normal applies should expose DNS on
	// wg0 immediately instead of relying on a later interface notification.
	if wgChanged {
		if err := syncOrActivateWireGuard(cfg, previous); err != nil {
			return err
		}
	}
	if wgClientChanged {
		if err := syncOrActivateWireGuardClient(cfg, previous); err != nil {
			return err
		}
	}
	if dnsmasqChanged {
		if err := runFixed("/sbin/rc-service", "dnsmasq", "restart"); err != nil {
			return fmt.Errorf("restart dnsmasq: %w", err)
		}
	}

	if cfg.WAN.Enabled {
		if wanChanged {
			if err := runFixed("/sbin/rc-service", "pppoe-wan", "restart"); err != nil {
				return fmt.Errorf("restart PPPoE: %w", err)
			}
		}
	} else if wanChanged {
		_ = runFixed("/sbin/rc-service", "pppoe-wan", "stop")
	}
	if cfg.WiFi.Enabled {
		if wifiChanged {
			if err := runFixed("/sbin/rc-service", "hostapd", "restart"); err != nil {
				return fmt.Errorf("restart hostapd: %w", err)
			}
		}
	} else if wifiChanged {
		_ = runFixed("/sbin/rc-service", "hostapd", "stop")
	}
	if ddnsChanged {
		if cfg.Cloudflare.DDNSEnabled {
			group, lookupErr := user.LookupGroup("inadyn")
			if lookupErr != nil {
				return errors.New("Cloudflare DDNS service group is unavailable")
			}
			gid, parseErr := strconv.Atoi(group.Gid)
			if parseErr != nil || os.Chown("/etc/inadyn/inadyn.conf", 0, gid) != nil {
				return errors.New("could not secure Cloudflare DDNS configuration ownership")
			}
			if err := runFixed("/sbin/rc-service", "inadyn", "restart"); err != nil {
				return fmt.Errorf("restart Cloudflare DDNS: %w", err)
			}
		} else {
			_ = runFixed("/sbin/rc-service", "inadyn", "stop")
		}
	}
	if cfg.QoS.Enabled {
		iface := cfg.WAN.Interface
		if cfg.WAN.Enabled {
			iface = "ppp0"
		}
		output, err := runFixedOutput("/sbin/tc", "qdisc", "show", "dev", iface)
		if err != nil || (!strings.Contains(output, " cake ") &&
			!strings.Contains(output, " fq_codel ") &&
			!strings.Contains(output, " htb ")) {
			log.Printf("QoS qdisc not active on %s (non-fatal, retried on next apply): %v", iface, err)
		}
	}
	if squidChanged {
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
	}
	return nil
}

func verifyActive(cfg config.SystemConfig, plan runtimeVerificationPlan) error {
	if err := runFixed("/usr/sbin/nft", "list", "table", "inet", "minimalrouter"); err != nil {
		return fmt.Errorf("nftables table unavailable: %w", err)
	}
	if err := runFixed("/sbin/rc-service", "dnsmasq", "status"); err != nil {
		return fmt.Errorf("dnsmasq unhealthy: %w", err)
	}
	lanInterface := cfg.RuntimeLANInterface()
	output, err := runFixedOutput("/sbin/ip", "-4", "addr", "show", "dev", lanInterface)
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
	if cfg.WAN.Enabled && plan.WAN {
		deadline := time.Now().Add(20 * time.Second)
		for {
			pppAddress, err := runFixedOutput("/sbin/ip", "-4", "addr", "show", "dev", "ppp0")
			if err == nil && strings.Contains(pppAddress, "inet ") {
				break
			}
			if time.Now().After(deadline) {
				return errors.New("PPPoE interface has no assigned IPv4 address")
			}
			time.Sleep(500 * time.Millisecond)
		}
		pppDefaultRoute, err := runFixedOutput("/sbin/ip", "-4", "route", "show", "default", "dev", "ppp0")
		if err != nil || strings.TrimSpace(pppDefaultRoute) == "" {
			return errors.New("PPPoE interface has no default route")
		}
	}
	if cfg.WireGuard.Enabled && plan.WireGuard {
		interfaceName := cfg.WireGuard.Interface
		if interfaceName == "" {
			interfaceName = "wg0"
		}
		if err := runFixed("/usr/bin/wg", "show", interfaceName); err != nil {
			return fmt.Errorf("WireGuard interface unhealthy: %w", err)
		}
		if err := runFixed("/sbin/ip", "-4", "addr", "show", "dev", interfaceName); err != nil {
			return fmt.Errorf("WireGuard address unavailable: %w", err)
		}
	}
	if plan.ExtraLAN {
		for _, lan := range cfg.Firewall.ExtraLANs {
			if !lan.Enabled || lan.Interface == "" || lan.RouterAddress == "" {
				continue
			}
			addressOutput, err := runFixedOutput("/sbin/ip", "-4", "addr", "show", "dev", lan.Interface)
			if err != nil {
				return fmt.Errorf("extra LAN %s interface unavailable: %w", lan.Interface, err)
			}
			addressPrefix := lan.RouterAddress
			if slash := strings.IndexByte(addressPrefix, '/'); slash >= 0 {
				addressPrefix = addressPrefix[:slash]
			}
			if !strings.Contains(addressOutput, "inet "+addressPrefix+"/") {
				return fmt.Errorf("extra LAN %s router address is not active", lan.Interface)
			}
		}
	}
	if cfg.WiFi.Enabled && plan.WiFi {
		if err := runFixed("/sbin/rc-service", "hostapd", "status"); err != nil {
			return fmt.Errorf("hostapd unhealthy: %w", err)
		}
		if err := runFixed("/sbin/ip", "link", "show", "dev", cfg.WiFi.Interface); err != nil {
			return fmt.Errorf("Wi-Fi interface unavailable: %w", err)
		}
		bridgeLink, err := os.Readlink(filepath.Join("/sys/class/net", cfg.WiFi.Interface, "master"))
		if err != nil || filepath.Base(bridgeLink) != config.WiFiBridgeInterface {
			return errors.New("Wi-Fi interface is not attached to the LAN bridge")
		}
	}
	if cfg.Cloudflare.DDNSEnabled && plan.DDNS {
		if err := runFixed("/sbin/rc-service", "inadyn", "status"); err != nil {
			return fmt.Errorf("Cloudflare DDNS unhealthy: %w", err)
		}
	}
	if cfg.SquidProxy.Enabled && plan.Squid {
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

func preflightWireGuard(runtimePath string) error {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return errors.New("could not allocate validation interface name")
	}
	checkInterface := "mrwg" + hex.EncodeToString(suffix[:])
	if err := runFixed("/sbin/ip", "link", "add", "dev", checkInterface, "type", "wireguard"); err != nil {
		return fmt.Errorf("create validation interface: %w", err)
	}
	defer func() {
		_ = runFixed("/sbin/ip", "link", "delete", "dev", checkInterface)
	}()
	if err := runFixed("/usr/bin/wg", "setconf", checkInterface, runtimePath); err != nil {
		return fmt.Errorf("validate configuration: %w", err)
	}
	return nil
}

func preflightWiFi(interfaceName string) error {
	if err := runFixed("/sbin/ip", "link", "show", "dev", interfaceName); err != nil {
		return fmt.Errorf("interface unavailable: %w", err)
	}
	if _, err := runFixedOutput("/usr/sbin/iw", "dev", interfaceName, "info"); err != nil {
		return fmt.Errorf("interface is not a managed wireless radio: %w", err)
	}
	capabilities, err := runFixedOutput("/usr/sbin/iw", "list")
	if err != nil {
		return fmt.Errorf("read radio capabilities: %w", err)
	}
	if !strings.Contains(capabilities, "Supported interface modes:") ||
		!strings.Contains(capabilities, "* AP") {
		return errors.New("wireless radio does not advertise access-point mode")
	}
	return nil
}

func removeWireGuard(interfaceName string) error {
	_ = runFixed("/sbin/ip", "link", "delete", "dev", interfaceName)
	if err := runFixed("/sbin/ip", "link", "show", "dev", interfaceName); err == nil {
		return errors.New("WireGuard interface remained active")
	}
	return nil
}

func wireGuardInterfaceName(cfg config.WireGuardConfig) string {
	if cfg.Interface != "" {
		return cfg.Interface
	}
	return "wg0"
}

func wireGuardClientInterfaceName(cfg config.WGClientConfig) string {
	if cfg.Interface != "" {
		return cfg.Interface
	}
	return "wg1"
}

func wireGuardMTU(cfg config.SystemConfig) int {
	const wireGuardOverhead = 80
	mtu := cfg.WAN.MTU - wireGuardOverhead
	if mtu < 576 {
		return 576
	}
	if mtu > 1420 {
		return 1420
	}
	return mtu
}

// wireGuardFullRecreate reports whether the change requires rebuilding the
// interface: address, interface name, enabled state, or MTU edits cannot be
// applied by syncconf. Peer, key, and endpoint edits are synced live.
func wireGuardFullRecreate(cfg, previous config.SystemConfig) bool {
	return cfg.WireGuard.Interface != previous.WireGuard.Interface ||
		cfg.WireGuard.Address != previous.WireGuard.Address ||
		cfg.WireGuard.Enabled != previous.WireGuard.Enabled ||
		wireGuardMTU(cfg) != wireGuardMTU(previous)
}

// wireGuardClientFullRecreate reports whether the outbound tunnel change
// cannot be applied by syncconf.
func wireGuardClientFullRecreate(cfg, previous config.SystemConfig) bool {
	return cfg.WGClient.Interface != previous.WGClient.Interface ||
		cfg.WGClient.Address != previous.WGClient.Address ||
		cfg.WGClient.Enabled != previous.WGClient.Enabled ||
		wireGuardMTU(cfg) != wireGuardMTU(previous)
}

// syncOrActivateWireGuard applies a WireGuard change with the least
// disruption: peer/key/endpoint edits go through `wg syncconf` on the live
// interface (sessions survive), while topology changes rebuild it. Routes are
// reconciled afterwards because syncconf never touches them.
func syncOrActivateWireGuard(cfg config.SystemConfig, previous *config.SystemConfig) error {
	if previous == nil || wireGuardFullRecreate(cfg, *previous) {
		if previous != nil {
			oldInterface := wireGuardInterfaceName(previous.WireGuard)
			newInterface := wireGuardInterfaceName(cfg.WireGuard)
			if oldInterface != newInterface {
				if err := removeWireGuard(oldInterface); err != nil {
					return fmt.Errorf("remove previous WireGuard interface: %w", err)
				}
			}
		}
		return activateWireGuard(cfg)
	}
	interfaceName := wireGuardInterfaceName(cfg.WireGuard)
	if !cfg.WireGuard.Enabled {
		return removeWireGuard(interfaceName)
	}
	if _, err := runFixedOutput("/sbin/ip", "link", "show", "dev", interfaceName); err != nil {
		// The interface does not exist (sessionless boot or prior failure):
		// nothing to preserve, rebuild from scratch.
		return activateWireGuard(cfg)
	}
	if err := runFixed("/usr/bin/wg", "syncconf", interfaceName, wireGuardRuntimePath); err != nil {
		return fmt.Errorf("sync WireGuard configuration: %w", err)
	}
	if err := syncWireGuardRoutes(cfg.WireGuard.Peers, previous.WireGuard.Peers, interfaceName); err != nil {
		return err
	}
	return nil
}

func syncOrActivateWireGuardClient(cfg config.SystemConfig, previous *config.SystemConfig) error {
	if previous == nil || wireGuardClientFullRecreate(cfg, *previous) {
		if previous != nil {
			oldInterface := wireGuardClientInterfaceName(previous.WGClient)
			newInterface := wireGuardClientInterfaceName(cfg.WGClient)
			if oldInterface != newInterface {
				if err := removeWireGuard(oldInterface); err != nil {
					return fmt.Errorf("remove previous WireGuard client interface: %w", err)
				}
			}
		}
		return activateWireGuardClient(cfg)
	}
	interfaceName := wireGuardClientInterfaceName(cfg.WGClient)
	if !cfg.WGClient.Enabled {
		return removeWireGuard(interfaceName)
	}
	if _, err := runFixedOutput("/sbin/ip", "link", "show", "dev", interfaceName); err != nil {
		return activateWireGuardClient(cfg)
	}
	if err := runFixed("/usr/bin/wg", "syncconf", interfaceName, wireGuardClientRuntimePath); err != nil {
		return fmt.Errorf("sync WireGuard client configuration: %w", err)
	}
	if err := syncWireGuardClientRoutes(cfg.WGClient.AllowedIPs, previous.WGClient.AllowedIPs, interfaceName); err != nil {
		return err
	}
	return nil
}

func syncWireGuardRoutes(peers, oldPeers []config.WireGuardPeer, interfaceName string) error {
	for _, peer := range peers {
		if !peer.Enabled {
			continue
		}
		for _, allowedIP := range peer.AllowedIPs {
			if err := runFixed("/sbin/ip", "-4", "route", "replace", allowedIP, "dev", interfaceName); err != nil {
				return fmt.Errorf("install WireGuard peer route: %w", err)
			}
		}
	}
	for _, stale := range removedAllowedIPs(peerAllowedIPs(oldPeers), peerAllowedIPs(peers)) {
		_ = runFixed("/sbin/ip", "-4", "route", "del", stale, "dev", interfaceName)
	}
	return nil
}

func syncWireGuardClientRoutes(allowed, oldAllowed []string, interfaceName string) error {
	for _, allowedIP := range allowed {
		if err := runFixed("/sbin/ip", "-4", "route", "replace", allowedIP, "dev", interfaceName); err != nil {
			return fmt.Errorf("install WireGuard client route: %w", err)
		}
	}
	for _, stale := range removedAllowedIPs(oldAllowed, allowed) {
		_ = runFixed("/sbin/ip", "-4", "route", "del", stale, "dev", interfaceName)
	}
	return nil
}

func peerAllowedIPs(peers []config.WireGuardPeer) []string {
	var out []string
	for _, peer := range peers {
		if !peer.Enabled {
			continue
		}
		out = append(out, peer.AllowedIPs...)
	}
	return out
}

// removedAllowedIPs returns the normalized routes present in oldRoutes but
// not in newRoutes: syncconf leaves their kernel routes behind.
func removedAllowedIPs(oldRoutes, newRoutes []string) []string {
	keep := make(map[string]struct{}, len(newRoutes))
	for _, r := range newRoutes {
		keep[normalizeCIDR(r)] = struct{}{}
	}
	var stale []string
	seen := make(map[string]struct{})
	for _, r := range oldRoutes {
		key := normalizeCIDR(r)
		if _, ok := keep[key]; ok {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		stale = append(stale, key)
	}
	return stale
}

func normalizeCIDR(value string) string {
	value = strings.TrimSpace(value)
	if ip := net.ParseIP(value); ip != nil {
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		return ip.String() + fmt.Sprintf("/%d", bits)
	}
	if _, network, err := net.ParseCIDR(value); err == nil {
		return network.String()
	}
	return value
}

func activateWireGuard(cfg config.SystemConfig) error {
	interfaceName := wireGuardInterfaceName(cfg.WireGuard)
	if err := removeWireGuard(interfaceName); err != nil {
		return fmt.Errorf("stop WireGuard: %w", err)
	}
	if !cfg.WireGuard.Enabled {
		return nil
	}

	cleanup := true
	defer func() {
		if cleanup {
			_ = removeWireGuard(interfaceName)
		}
	}()

	if err := runFixed("/sbin/ip", "link", "add", "dev", interfaceName, "type", "wireguard"); err != nil {
		return fmt.Errorf("create WireGuard interface: %w", err)
	}
	if err := runFixed("/usr/bin/wg", "setconf", interfaceName, wireGuardRuntimePath); err != nil {
		return fmt.Errorf("load WireGuard configuration: %w", err)
	}
	if err := runFixed("/sbin/ip", "-4", "address", "add", cfg.WireGuard.Address, "dev", interfaceName); err != nil {
		return fmt.Errorf("assign WireGuard address: %w", err)
	}
	if err := runFixed("/sbin/ip", "link", "set", "dev", interfaceName, "mtu", strconv.Itoa(wireGuardMTU(cfg)), "up"); err != nil {
		return fmt.Errorf("bring WireGuard up: %w", err)
	}
	for _, peer := range cfg.WireGuard.Peers {
		if !peer.Enabled {
			continue
		}
		for _, allowedIP := range peer.AllowedIPs {
			if err := runFixed("/sbin/ip", "-4", "route", "replace", allowedIP, "dev", interfaceName); err != nil {
				return fmt.Errorf("install WireGuard peer route: %w", err)
			}
		}
	}
	cleanup = false
	return nil
}

// activateWireGuardClient brings the outbound tunnel (wg1) up: a WireGuard
// interface, the local tunnel address, MTU, and a route for each remote
// network. The remote peer is expected to be reachable for handshakes; a
// missing endpoint only means the tunnel stays quiet until it is.
func activateWireGuardClient(cfg config.SystemConfig) error {
	interfaceName := wireGuardClientInterfaceName(cfg.WGClient)
	if err := removeWireGuard(interfaceName); err != nil {
		return fmt.Errorf("stop WireGuard client: %w", err)
	}
	if !cfg.WGClient.Enabled {
		return nil
	}

	cleanup := true
	defer func() {
		if cleanup {
			_ = removeWireGuard(interfaceName)
		}
	}()

	if err := runFixed("/sbin/ip", "link", "add", "dev", interfaceName, "type", "wireguard"); err != nil {
		return fmt.Errorf("create WireGuard client interface: %w", err)
	}
	if err := runFixed("/usr/bin/wg", "setconf", interfaceName, wireGuardClientRuntimePath); err != nil {
		return fmt.Errorf("load WireGuard client configuration: %w", err)
	}
	if cfg.WGClient.Address != "" {
		if err := runFixed("/sbin/ip", "-4", "address", "add", cfg.WGClient.Address, "dev", interfaceName); err != nil {
			return fmt.Errorf("assign WireGuard client address: %w", err)
		}
	}
	if err := runFixed("/sbin/ip", "link", "set", "dev", interfaceName, "mtu", strconv.Itoa(wireGuardMTU(cfg)), "up"); err != nil {
		return fmt.Errorf("bring WireGuard client up: %w", err)
	}
	for _, allowedIP := range cfg.WGClient.AllowedIPs {
		if err := runFixed("/sbin/ip", "-4", "route", "replace", allowedIP, "dev", interfaceName); err != nil {
			return fmt.Errorf("install WireGuard client route: %w", err)
		}
	}
	cleanup = false
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

func rollback(previousConfig *config.SystemConfig, candidateConfig *config.SystemConfig, files []previousFile) error {
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
		_ = runFixed("/sbin/rc-service", "hostapd", "stop")
		if err := configureRuntimeLAN(*previousConfig); err != nil {
			errs = append(errs, safeError(err))
		}
		if err := configureExtraLANs(*previousConfig); err != nil {
			log.Printf("rollback optional ExtraLAN restore degraded: %v", err)
		}
		if candidateConfig != nil {
			if err := cleanCandidateOnlyExtraLANs(*previousConfig, *candidateConfig); err != nil {
				errs = append(errs, safeError(err))
			}
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
		if err := activateWireGuard(*previousConfig); err != nil {
			if previousConfig.System.ManagementAccess == "wireguard_only" {
				errs = append(errs, safeError(err))
			} else {
				log.Printf("rollback optional WireGuard restore degraded: %v", err)
			}
		}
		if err := runFixed("/sbin/rc-service", "dnsmasq", "restart"); err != nil {
			errs = append(errs, safeError(err))
		}
	} else {
		_ = runFixed("/sbin/rc-service", "dnsmasq", "stop")
	}
	if previousConfig != nil && previousConfig.WAN.Enabled {
		if err := runFixed("/sbin/rc-service", "pppoe-wan", "restart"); err != nil {
			log.Printf("rollback PPPoE unavailable (non-fatal to local rollback): %v", err)
		}
	} else {
		_ = runFixed("/sbin/rc-service", "pppoe-wan", "stop")
	}
	if previousConfig != nil {
		if err := activateWireGuardClient(*previousConfig); err != nil {
			log.Printf("rollback optional WireGuard client restore degraded: %v", err)
		}
	} else {
		_ = removeWireGuard("wg0")
		_ = removeWireGuard("wg1")
	}
	if previousConfig != nil && previousConfig.WiFi.Enabled {
		if err := runFixed("/sbin/rc-service", "hostapd", "restart"); err != nil {
			log.Printf("rollback optional Wi-Fi restore degraded: %v", err)
		}
	} else {
		_ = runFixed("/sbin/rc-service", "hostapd", "stop")
	}
	if previousConfig != nil && previousConfig.Cloudflare.DDNSEnabled {
		if err := runFixed("/sbin/rc-service", "inadyn", "restart"); err != nil {
			log.Printf("rollback optional DDNS restore degraded: %v", err)
		}
	} else {
		_ = runFixed("/sbin/rc-service", "inadyn", "stop")
	}
	if previousConfig != nil && previousConfig.QoS.Enabled {
		if err := applyQoS(*previousConfig); err != nil {
			log.Printf("rollback QoS not applied (non-fatal): %v", err)
		}
	} else if previousConfig != nil {
		clearQoS(*previousConfig)
	}
	if previousConfig != nil && previousConfig.SquidProxy.Enabled {
		if err := runFixed("/sbin/rc-service", "squid", "restart"); err != nil {
			log.Printf("rollback optional Squid restore degraded: %v", err)
		}
	} else {
		_ = runFixed("/sbin/rc-service", "squid", "stop")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	if previousConfig != nil {
		_ = os.Remove(pendingPath)
		// Rollback verification is structural only: an ISP or optional-service
		// outage must never convert a successful local rollback into RecoveryRequired.
		return verifyActive(*previousConfig, verificationPlan(apply.OpReconcile, nil, *previousConfig))
	}
	// Failed first-run transactions return to the setup-only LAN rather than
	// leaving the wizard unreachable with all services stopped.
	return restoreFirstRunRuntime()
}

func extraLANInterfaces(cfg config.SystemConfig) map[string]struct{} {
	interfaces := make(map[string]struct{})
	for _, lan := range cfg.Firewall.ExtraLANs {
		if lan.Enabled && lan.Interface != "" {
			interfaces[lan.Interface] = struct{}{}
		}
	}
	return interfaces
}

// cleanCandidateOnlyExtraLANs removes kernel state that only the failed
// candidate introduced: an interface that carried an extra LAN in the
// candidate but is not an extra LAN in the previous configuration keeps its
// address and connected route unless explicitly flushed.
// cleanExtraLANState flushes and takes down the interfaces in `stale` that
// are not owned by the surviving configuration. Kernel state for a removed
// segment must not linger: the nftables rules are gone, but the address and
// connected route would still be served.
func cleanExtraLANState(stale, keep map[string]struct{}) error {
	var errs []string
	for iface := range stale {
		if _, ok := keep[iface]; ok {
			continue
		}
		if err := runFixed("/sbin/ip", "-4", "addr", "flush", "dev", iface, "scope", "global"); err != nil {
			errs = append(errs, safeError(err))
		}
		if err := runFixed("/sbin/ip", "link", "set", "dev", iface, "down"); err != nil {
			errs = append(errs, safeError(err))
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func cleanCandidateOnlyExtraLANs(previous, candidate config.SystemConfig) error {
	return cleanExtraLANState(extraLANInterfaces(candidate), extraLANInterfaces(previous))
}

func cleanRemovedExtraLANs(previous, current config.SystemConfig) error {
	return cleanExtraLANState(extraLANInterfaces(previous), extraLANInterfaces(current))
}

func applyQoS(cfg config.SystemConfig) error {
	clearQoS(cfg)
	commands, err := services.QoSCommands(&cfg)
	if err != nil {
		return err
	}
	if len(commands) == 0 {
		return nil
	}
	// Download shaping lives on the ifb0 dummy; create it once, raise it, and
	// let tc attach everything else.
	if _, err := runFixedOutput("/sbin/ip", "link", "show", "dev", services.QoSInterfaceName); err != nil {
		if err := runFixed("/sbin/ip", "link", "add", services.QoSInterfaceName, "type", "ifb"); err != nil {
			return fmt.Errorf("create %s: %w", services.QoSInterfaceName, err)
		}
	}
	if err := runFixed("/sbin/ip", "link", "set", "dev", services.QoSInterfaceName, "up"); err != nil {
		return err
	}
	for _, args := range commands {
		if err := runFixed("/sbin/tc", args...); err != nil {
			clearQoS(cfg)
			return err
		}
	}
	return nil
}

func clearQoS(cfg config.SystemConfig) {
	iface := cfg.WAN.Interface
	if cfg.WAN.Enabled {
		iface = "ppp0"
	}
	if iface == "" {
		iface = "eth0"
	}
	_ = runFixed("/sbin/tc", "qdisc", "del", "dev", iface, "root")
	_ = runFixed("/sbin/tc", "qdisc", "del", "dev", iface, "ingress")
	_ = runFixed("/sbin/tc", "qdisc", "del", "dev", services.QoSInterfaceName, "root")
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
	sendGratuitousARP(lan.Interface, lan.CIDR)
	return nil
}

func removeOwnedLANBridge() error {
	bridge := config.WiFiBridgeInterface
	if err := runFixed("/sbin/ip", "link", "show", "dev", bridge); err != nil {
		return nil
	}
	members, err := os.ReadDir(filepath.Join("/sys/class/net", bridge, "brif"))
	if err == nil {
		for _, member := range members {
			if !interfaceNamePattern.MatchString(member.Name()) {
				return errors.New("LAN bridge has an invalid member name")
			}
			_ = runFixed("/sbin/ip", "link", "set", "dev", member.Name(), "nomaster")
		}
	}
	_ = runFixed("/sbin/ip", "-4", "addr", "flush", "dev", bridge, "scope", "global")
	if err := runFixed("/sbin/ip", "link", "delete", "dev", bridge, "type", "bridge"); err != nil {
		return fmt.Errorf("remove LAN bridge: %w", err)
	}
	return nil
}

func configureRuntimeLAN(cfg config.SystemConfig) error {
	if !cfg.WiFi.Enabled {
		if err := removeOwnedLANBridge(); err != nil {
			return err
		}
		return configureLAN(cfg.LAN)
	}

	bridge := config.WiFiBridgeInterface
	if err := runFixed("/sbin/ip", "link", "show", "dev", bridge); err != nil {
		if err := runFixed("/sbin/ip", "link", "add", "name", bridge, "type", "bridge"); err != nil {
			return fmt.Errorf("create LAN bridge: %w", err)
		}
	}
	if err := runFixed("/sbin/ip", "link", "set", "dev", cfg.LAN.Interface, "up"); err != nil {
		return fmt.Errorf("bring wired LAN up: %w", err)
	}
	if err := runFixed("/sbin/ip", "-4", "addr", "flush", "dev", cfg.LAN.Interface, "scope", "global"); err != nil {
		return fmt.Errorf("move LAN address to bridge: %w", err)
	}
	if err := runFixed("/sbin/ip", "link", "set", "dev", cfg.LAN.Interface, "master", bridge); err != nil {
		return fmt.Errorf("attach wired LAN to bridge: %w", err)
	}
	if err := runFixed("/sbin/ip", "link", "set", "dev", bridge, "up"); err != nil {
		return fmt.Errorf("bring LAN bridge up: %w", err)
	}
	if err := runFixed("/sbin/ip", "-4", "addr", "flush", "dev", bridge, "scope", "global"); err != nil {
		return fmt.Errorf("clear previous bridge addresses: %w", err)
	}
	if err := runFixed("/sbin/ip", "-4", "addr", "add", cfg.LAN.CIDR, "dev", bridge); err != nil {
		return fmt.Errorf("configure LAN bridge address: %w", err)
	}
	sendGratuitousARP(bridge, cfg.LAN.CIDR)
	return nil
}

func configureProvisionalRuntimeLAN(candidate, previous config.SystemConfig) error {
	if candidate.LAN.Interface != previous.LAN.Interface {
		return errors.New("LAN interface changes cannot be provisionally applied")
	}
	if err := configureRuntimeLAN(candidate); err != nil {
		return err
	}
	if previous.LAN.CIDR != candidate.LAN.CIDR {
		if err := runFixed("/sbin/ip", "-4", "addr", "add", previous.LAN.CIDR, "dev", candidate.RuntimeLANInterface()); err != nil {
			return fmt.Errorf("retain previous LAN address: %w", err)
		}
	}
	return nil
}

func sendGratuitousARP(iface, cidr string) {
	parts := strings.Split(cidr, "/")
	if len(parts) == 0 {
		return
	}
	ip := parts[0]
	// Send an unsolicited ARP reply (-A) or request (-U) to update peers
	// using busybox arping (which typically supports -U for Unsolicited)
	_ = runFixed("/usr/sbin/arping", "-U", "-c", "3", "-I", iface, ip)
}

// configureExtraLANs reconstructs every enabled isolated segment on the
// kernel: interface up, deterministic address flush, then the configured
// router-side address. The nftables rules (loaded separately) provide the
// routing and isolation; without this reconstruction the segment interface
// would carry no address after a reboot or rollback.
func configureExtraLANs(cfg config.SystemConfig) error {
	for _, lan := range cfg.Firewall.ExtraLANs {
		if !lan.Enabled || lan.Interface == "" || lan.RouterAddress == "" {
			continue
		}
		if err := runFixed("/sbin/ip", "link", "set", "dev", lan.Interface, "up"); err != nil {
			return fmt.Errorf("bring extra LAN %s up: %w", lan.Interface, err)
		}
		if err := runFixed("/sbin/ip", "-4", "addr", "flush", "dev", lan.Interface, "scope", "global"); err != nil {
			return fmt.Errorf("clear extra LAN %s addresses: %w", lan.Interface, err)
		}
		if err := runFixed("/sbin/ip", "-4", "addr", "add", lan.RouterAddress, "dev", lan.Interface); err != nil {
			return fmt.Errorf("configure extra LAN %s address: %w", lan.Interface, err)
		}
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
	if cfg.WAN.Interface != "" {
		if err := runFixed("/sbin/sysctl", "-w", "net.ipv4.conf."+cfg.WAN.Interface+".rp_filter=1"); err != nil {
			return fmt.Errorf("enable WAN reverse-path filtering: %w", err)
		}
	}
	return nil
}

func runFixed(binary string, args ...string) error {
	_, err := runCommandOutput(defaultCommandTimeout, binary, args...)
	return err
}

func runFixedTimeout(timeout time.Duration, binary string, args ...string) error {
	_, err := runCommandOutput(timeout, binary, args...)
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
	return runCommandOutput(defaultCommandTimeout, binary, args...)
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
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid last-good configuration: %w", err)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return atomicWrite(lastGoodPath, data, 0600)
}

func validateTransactionRecord(record transactionRecord) error {
	if !transactionIDPattern.MatchString(record.ID) {
		return errors.New("transaction record has an invalid ID")
	}
	digest, err := hex.DecodeString(record.ConfigHash)
	if err != nil || len(digest) != sha256.Size {
		return errors.New("transaction record has an invalid configuration fingerprint")
	}
	if record.StartedAt.IsZero() {
		return errors.New("transaction record has no start time")
	}
	if record.CompletedAt.IsZero() {
		if record.Response.ID != "" || record.Response.Success || record.Response.Verified ||
			record.Response.RolledBack || record.Response.RecoveryRequired || record.Response.Error != "" {
			return errors.New("incomplete transaction record contains a final response")
		}
		return nil
	}
	if record.CompletedAt.Before(record.StartedAt) {
		return errors.New("transaction record completion precedes start")
	}
	if record.Response.ID != record.ID {
		return errors.New("transaction record response ID does not match")
	}
	if err := record.Response.Validate(); err != nil {
		return fmt.Errorf("transaction record response is invalid: %w", err)
	}
	return nil
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
	if err := validateTransactionRecord(record); err != nil {
		return nil, err
	}
	return &record, nil
}

func saveLastTransaction(record transactionRecord) error {
	if err := validateTransactionRecord(record); err != nil {
		return err
	}
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

func validatePendingConfirmation(pending pendingConfirmation) error {
	if err := pending.Config.Validate(); err != nil {
		return fmt.Errorf("pending configuration is invalid: %w", err)
	}
	expected, err := hashConfig(pending.Config)
	if err != nil {
		return err
	}
	if pending.ConfigHash == "" || pending.ConfigHash != expected {
		return errors.New("pending configuration fingerprint does not match")
	}
	return nil
}

func savePendingConfirmation(pending pendingConfirmation) error {
	if err := validatePendingConfirmation(pending); err != nil {
		return err
	}
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
	if err := validatePendingConfirmation(pending); err != nil {
		return nil, err
	}
	return &pending, nil
}
