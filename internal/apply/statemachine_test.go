package apply

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

type testApplyClient struct {
	response *ApplyResponse
	err      error
	requests []ApplyRequest
}

func (c *testApplyClient) Apply(_ context.Context, req ApplyRequest) (*ApplyResponse, error) {
	c.requests = append(c.requests, req)
	if c.err != nil {
		return nil, c.err
	}
	resp := *c.response
	resp.ID = req.ID
	return &resp, nil
}

func TestEngineTransactionLifecycle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "engine-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := config.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	initialCfg := config.DefaultConfig()
	initialCfg.TrustedNetworks = []string{"192.168.1.0/24", "10.0.0.0/24"}
	client := &testApplyClient{response: &ApplyResponse{Success: true, Verified: true}}
	engine := NewEngineWithClient(initialCfg, store, client)

	if engine.GetStore() == nil {
		t.Errorf("Expected store to be attached to engine")
	}

	// Same-subnet gateway changes remain live/commit-confirmed. Moving the
	// entire subnet is deliberately recovery-console-only because existing DHCP
	// clients still carry the old gateway/DNS until lease renewal.
	newCfg := initialCfg
	newCfg.LAN.IPAddress = "192.168.1.2"
	newCfg.LAN.CIDR = "192.168.1.2/24"

	tx, err := engine.ProcessTransaction("tx-test-1", newCfg)
	if err != nil {
		t.Fatalf("ProcessTransaction failed: %v", err)
	}
	if tx.CurrentState != StateAwaitingConfirmation {
		t.Errorf("Expected state StateAwaitingConfirmation, got %s", tx.CurrentState)
	}
	if tx.Config.Revision != initialCfg.Revision+1 {
		t.Fatalf("expected pending revision %d, got %d", initialCfg.Revision+1, tx.Config.Revision)
	}
	if len(client.requests) != 1 || client.requests[0].Config.Revision != tx.Config.Revision {
		t.Fatalf("privileged helper and pending transaction used different revisions")
	}

	tx, err = engine.ConfirmTransaction(tx.ID)
	if err != nil {
		t.Fatalf("ConfirmTransaction failed: %v", err)
	}
	if tx.CurrentState != StateCommitted {
		t.Errorf("Expected confirmed state StateCommitted, got %s", tx.CurrentState)
	}
	if len(client.requests) != 3 {
		t.Fatalf("expected apply, canonical helper commit, and LAN finalize reconcile; got %d requests", len(client.requests))
	}
	wantOps := []OperationType{OpApplyAll, OpCommitConfirmed, OpReconcile}
	for i, wantOp := range wantOps {
		if client.requests[i].Op != wantOp {
			t.Fatalf("request %d op=%s, want %s", i, client.requests[i].Op, wantOp)
		}
		if client.requests[i].Config.Revision != client.requests[0].Config.Revision {
			t.Fatalf("request %d did not use the exact applied revision", i)
		}
	}
	if !client.requests[1].SkipWANVerify {
		t.Fatal("canonical helper acknowledgement after user confirmation must not depend on WAN availability")
	}

	if engine.GetCurrentConfig().LAN.IPAddress != "192.168.1.2" {
		t.Errorf("Expected current config LAN IP to be 192.168.1.2")
	}
}

func TestCrossSubnetLANChangeRequiresRecoveryConsole(t *testing.T) {
	initialCfg := config.DefaultConfig()
	client := &testApplyClient{response: &ApplyResponse{Success: true, Verified: true}}
	engine := NewEngineWithClient(initialCfg, nil, client)

	candidate := initialCfg
	candidate.LAN.IPAddress = "192.168.2.1"
	candidate.LAN.CIDR = "192.168.2.1/24"
	candidate.DHCP.RangeStart = "192.168.2.100"
	candidate.DHCP.RangeEnd = "192.168.2.200"

	tx, err := engine.ProcessTransaction("tx-cross-subnet", candidate)
	if err == nil {
		t.Fatal("cross-subnet live LAN migration must be rejected")
	}
	if tx.CurrentState != StateRejected || !strings.Contains(tx.Error, "local recovery console") {
		t.Fatalf("unexpected rejection: state=%s error=%q", tx.CurrentState, tx.Error)
	}
	if len(client.requests) != 0 {
		t.Fatal("unsafe cross-subnet candidate reached privileged helper")
	}
}

func TestInitialSetupDefersHelperLastGoodUntilCanonicalCommit(t *testing.T) {
	initialCfg := config.DefaultConfig()
	client := &testApplyClient{response: &ApplyResponse{Success: true, Verified: true}}
	engine := NewEngineWithClient(initialCfg, nil, client)
	committed := false

	tx, err := engine.ProcessInitialSetup("setup-two-phase", initialCfg, func(applied config.SystemConfig) error {
		committed = true
		if applied.Revision != initialCfg.Revision+1 {
			t.Fatalf("canonical setup commit revision=%d want=%d", applied.Revision, initialCfg.Revision+1)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ProcessInitialSetup failed: %v", err)
	}
	if !committed || tx.CurrentState != StateCommitted {
		t.Fatalf("setup did not commit atomically: committed=%v state=%s", committed, tx.CurrentState)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected provisional apply plus helper canonical ack, got %d requests", len(client.requests))
	}
	if !client.requests[0].DeferLastGood {
		t.Fatal("initial setup helper apply must defer last-good until SQLite/auth commit succeeds")
	}
	if client.requests[1].Op != OpCommitConfirmed || !client.requests[1].SkipWANVerify {
		t.Fatalf("unexpected setup canonical ack: op=%s skip_wan=%v", client.requests[1].Op, client.requests[1].SkipWANVerify)
	}
}

func TestEngineInvalidTransactionRejection(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "engine-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := config.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	initialCfg := config.DefaultConfig()
	engine := NewEngineWithClient(initialCfg, store, &testApplyClient{response: &ApplyResponse{Success: true, Verified: true}})

	invalidCfg := initialCfg
	invalidCfg.WAN.Interface = "eth0"
	invalidCfg.LAN.Interface = "eth0"

	tx, err := engine.ProcessTransaction("tx-invalid-1", invalidCfg)
	if err == nil {
		t.Fatalf("Expected validation error for collision WAN=LAN")
	}
	if tx.CurrentState != StateRejected {
		t.Errorf("Expected state StateRejected, got %s", tx.CurrentState)
	}
}

func TestWiFiTopologyChangesRequireConfirmation(t *testing.T) {
	previous := config.DefaultConfig()

	enabled := previous
	enabled.WiFi.Enabled = true
	if !requiresConfirmation(previous, enabled) {
		t.Fatal("enabling the Wi-Fi LAN bridge must require confirmation")
	}

	newRadio := enabled
	newRadio.WiFi.Interface = "wlan1"
	if !requiresConfirmation(enabled, newRadio) {
		t.Fatal("changing the Wi-Fi bridge radio must require confirmation")
	}

	newSSID := enabled
	newSSID.WiFi.SSID = "Renamed-Network"
	if !requiresConfirmation(enabled, newSSID) {
		t.Fatal("SSID changes on an active AP can disconnect the administrator and must require confirmation")
	}

	newPassphrase := enabled
	newPassphrase.WiFi.Passphrase = "replacement-passphrase-123"
	if !requiresConfirmation(enabled, newPassphrase) {
		t.Fatal("passphrase changes on an active AP can disconnect the administrator and must require confirmation")
	}
}

func TestWGClientChangesRequireConfirmation(t *testing.T) {
	previous := config.DefaultConfig()

	enabled := previous
	enabled.WGClient.Enabled = true
	enabled.WGClient.PrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	enabled.WGClient.PublicKey = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA="
	enabled.WGClient.Address = "10.7.0.2/32"
	enabled.WGClient.Endpoint = "office.example.com:51820"
	enabled.WGClient.AllowedIPs = []string{"10.9.0.0/24"}
	if !requiresConfirmation(previous, enabled) {
		t.Fatal("enabling the outbound WireGuard tunnel must require confirmation")
	}

	newEndpoint := enabled
	newEndpoint.WGClient.Endpoint = "backup.example.com:51820"
	if !requiresConfirmation(enabled, newEndpoint) {
		t.Fatal("changing the tunnel endpoint must require confirmation")
	}

	rotatedKeys := enabled
	rotatedKeys.WGClient.PrivateKey = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCE="
	if !requiresConfirmation(enabled, rotatedKeys) {
		t.Fatal("rotating the tunnel key must require confirmation")
	}

	newNets := enabled
	newNets.WGClient.AllowedIPs = []string{"10.9.0.0/24", "10.10.0.0/24"}
	if !requiresConfirmation(enabled, newNets) {
		t.Fatal("changing allowed remote networks must require confirmation")
	}

	keepaliveOnly := enabled
	keepaliveOnly.WGClient.PersistentKeepalive = 15
	if !requiresConfirmation(enabled, keepaliveOnly) {
		t.Fatal("changing the tunnel keepalive must require confirmation")
	}

	disabled := enabled
	disabled.WGClient.Enabled = false
	if !requiresConfirmation(enabled, disabled) {
		t.Fatal("disabling the outbound tunnel must require confirmation")
	}
}

func TestGetCurrentConfigReturnsDetachedCopy(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WireGuard.Peers = []config.WireGuardPeer{
		{ID: "p1", Name: "phone", PublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", PresharedKey: "secret-psk", AllowedIPs: []string{"10.8.0.2/32"}, Enabled: true},
	}
	cfg.WGClient.AllowedIPs = []string{"10.7.0.0/24"}
	cfg.Firewall.ExtraLANs = []config.ExtraLANConfig{
		{ID: "xl1", Name: "media", Interface: "eth3", CIDR: "192.168.50.0/24", DstIP: "192.168.50.10", DstPort: 8080, AllowFrom: []string{cfg.LAN.CIDR}, Enabled: true},
	}
	cfg.DNS.Records = []config.DNSRecord{{Name: "immich.home.arpa", IP: "192.168.1.50"}}
	cfg.TrustedNetworks = []string{"192.168.1.0/24"}
	engine := NewEngineWithClient(cfg, nil, &testApplyClient{response: &ApplyResponse{}})

	view := engine.GetCurrentConfig()
	view.WireGuard.Peers[0].PresharedKey = "MUTATED"
	view.WireGuard.Peers[0].AllowedIPs[0] = "10.8.0.99/32"
	view.WireGuard.Peers = append(view.WireGuard.Peers, config.WireGuardPeer{ID: "other", PublicKey: "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC="})
	view.WGClient.AllowedIPs[0] = "0.0.0.0/0"
	view.Firewall.ExtraLANs[0].AllowFrom[0] = "0.0.0.0/0"
	view.DNS.Records[0].Name = "mutated.home.arpa"
	view.TrustedNetworks[0] = "0.0.0.0/0"

	current := engine.GetCurrentConfig()
	if current.WireGuard.Peers[0].PresharedKey != "secret-psk" {
		t.Fatalf("peer preshared key was mutated through a config view: %q", current.WireGuard.Peers[0].PresharedKey)
	}
	if len(current.WireGuard.Peers) != 1 || current.WireGuard.Peers[0].AllowedIPs[0] != "10.8.0.2/32" {
		t.Fatalf("peer slice/AllowedIPs were mutated through a config view: %+v", current.WireGuard.Peers)
	}
	if current.WGClient.AllowedIPs[0] != "10.7.0.0/24" {
		t.Fatalf("WGClient AllowedIPs were mutated through a config view: %v", current.WGClient.AllowedIPs)
	}
	if current.Firewall.ExtraLANs[0].AllowFrom[0] != cfg.LAN.CIDR {
		t.Fatalf("ExtraLAN AllowFrom was mutated through a config view: %v", current.Firewall.ExtraLANs[0].AllowFrom)
	}
	if current.DNS.Records[0].Name != "immich.home.arpa" || current.TrustedNetworks[0] != "192.168.1.0/24" {
		t.Fatal("nested DNS records or trusted networks were mutated through a config view")
	}
}
