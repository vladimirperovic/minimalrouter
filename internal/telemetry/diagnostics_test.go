package telemetry

import (
	"reflect"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestRedactSecrets(t *testing.T) {
	raw := `{"username":"user1","password":"mySecretPassword123","token":"ghp_abc123"}`
	redacted := RedactSecrets(raw)

	if strings.Contains(redacted, "mySecretPassword123") {
		t.Errorf("Expected password to be redacted from log string")
	}
	if !strings.Contains(redacted, "[REDACTED]") {
		t.Errorf("Expected [REDACTED] placeholder in text")
	}
}

func TestBuildDiagnosticBundle(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Password = "SuperSecretPass!"
	cfg.WireGuard.PrivateKey = "VerySecretPrivateKey"
	cfg.WireGuard.Peers = []config.WireGuardPeer{{PresharedKey: "VerySecretPresharedKey"}}
	cfg.Cloudflare.APIToken = "VerySecretAPIToken"
	cfg.Cloudflare.TunnelToken = "VerySecretTunnelToken"

	bundleBytes, err := BuildDiagnosticBundle(cfg)
	if err != nil {
		t.Fatalf("BuildDiagnosticBundle failed: %v", err)
	}

	bundleStr := string(bundleBytes)
	if strings.Contains(bundleStr, "SuperSecretPass!") {
		t.Errorf("Expected WAN password to be redacted from diagnostic bundle")
	}
	for _, secret := range []string{
		"VerySecretPrivateKey",
		"VerySecretPresharedKey",
		"VerySecretAPIToken",
		"VerySecretTunnelToken",
	} {
		if strings.Contains(bundleStr, secret) {
			t.Errorf("diagnostic bundle leaked %q", secret)
		}
	}
}

// TestRedactionDoesNotMutateCanonicalConfig proves that building a diagnostic
// bundle (or redacting a config view) never mutates the canonical in-memory
// configuration: the reported preshared key must survive byte-for-byte.
func TestRedactionDoesNotMutateCanonicalConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WireGuard.Peers = []config.WireGuardPeer{
		{ID: "p1", Name: "phone", PublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", PresharedKey: "preshared-secret-1", AllowedIPs: []string{"10.8.0.2/32"}, Enabled: true},
		{ID: "p2", Name: "laptop", PublicKey: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=", PresharedKey: "preshared-secret-2", AllowedIPs: []string{"10.8.0.3/32"}, Enabled: true},
	}
	cfg.WGClient.AllowedIPs = []string{"10.7.0.0/24"}
	cfg.Firewall.ExtraLANs = []config.ExtraLANConfig{
		{ID: "xl1", Name: "media", Interface: "eth3", CIDR: "192.168.50.0/24", DstIP: "192.168.50.10", DstPort: 8080, AllowFrom: []string{cfg.LAN.CIDR}, Enabled: true},
	}
	cfg.DNS.Records = []config.DNSRecord{{Name: "immich.home.arpa", IP: "192.168.1.50"}}
	cfg.DHCP.StaticLeases = []config.StaticLease{{ID: "l1", Hostname: "nas", MAC: "aa:bb:cc:dd:ee:ff", IPAddress: "192.168.1.42"}}
	cfg.AdGuard.DeviceProfiles = []config.DeviceProfile{{ID: "d1", Name: "kids", IPAddresses: []string{"192.168.1.5"}, Services: []string{"youtube"}, Schedule: config.WeeklyAccessSchedule{DayWindows: map[string][]config.AccessWindow{"mon": {{Start: "20:00", End: "21:00"}}}}}}
	cfg.TrustedNetworks = []string{"192.168.1.0/24"}
	original := cfg.DeepCopy()

	if _, err := BuildDiagnosticBundle(cfg); err != nil {
		t.Fatalf("BuildDiagnosticBundle failed: %v", err)
	}
	redacted := RedactedSystemConfig(cfg)
	if redacted.WireGuard.Peers[0].PresharedKey != "[REDACTED]" {
		t.Fatalf("redaction did not redact the preshared key")
	}
	if !reflect.DeepEqual(cfg, original) {
		t.Fatal("canonical config was mutated by diagnostics/redaction")
	}
}
