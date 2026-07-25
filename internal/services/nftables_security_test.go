package services

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestNftablesWANInputIsFailClosed(t *testing.T) {
	cfg := config.DefaultConfig()
	rules, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"tcp flags syn accept",
		"iifname \"eth0\" tcp accept",
		"rebind-localhost-ok",
	} {
		if strings.Contains(rules, forbidden) {
			t.Fatalf("generated rules contain unsafe WAN behavior %q", forbidden)
		}
	}
	if !strings.Contains(rules, "type filter hook input priority filter; policy drop;") {
		t.Fatal("input chain is not default-deny")
	}
}

func TestNftablesNeverEmitsWANPortForward(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "test"
	cfg.WAN.Password = "test"
	cfg.Firewall.WANIngressMode = "port_forwards" // invalid input must still fail closed in the generator
	cfg.Firewall.PortForwards = []config.PortForwardRule{{
		ID: "web", Name: "Web", Protocol: "tcp", ExternalPort: 8444,
		InternalIP: "192.168.1.10", InternalPort: 443, Enabled: true,
	}}
	rules, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rules, "dport 8444") || strings.Contains(rules, "dnat") {
		t.Fatal("secure appliance profile emitted a forbidden WAN port forward")
	}
}

func TestNftablesWANHasOnlyWireGuardNewIngress(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "test"
	cfg.WAN.Password = "long-enough-test-password"
	cfg.WireGuard.Enabled = true
	cfg.WireGuard.PrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	cfg.WireGuard.Peers = []config.WireGuardPeer{{
		ID: "admin", Name: "Admin", PublicKey: "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA=",
		AllowedIPs: []string{"10.8.0.2/32"}, Enabled: true,
	}}
	rules, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rules, `iifname "ppp*" udp dport 51820 accept`) {
		t.Fatal("WireGuard endpoint is not reachable on the PPPoE WAN")
	}
	for _, forbidden := range []string{
		`iifname "eth0" tcp dport`,
		`iifname "ppp*" tcp dport`,
		`iifname "eth0" udp dport 51820`,
		"dnat to",
	} {
		if strings.Contains(rules, forbidden) {
			t.Fatalf("WAN exposes a non-WireGuard entry point: %s", forbidden)
		}
	}
}
