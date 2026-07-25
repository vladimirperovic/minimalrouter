package services

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestGenerateNftables(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.Firewall.PortForwards = []config.PortForwardRule{
		{
			ID:           "pf1",
			Name:         "Web Server",
			Protocol:     "tcp",
			ExternalPort: 8080,
			InternalIP:   "192.168.1.50",
			InternalPort: 80,
			Enabled:      true,
		},
	}

	out, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatalf("GenerateNftables failed: %v", err)
	}

	if !strings.Contains(out, "table inet minimalrouter") {
		t.Errorf("Expected output to contain table definition")
	}
	if strings.Contains(out, "tcp dport 8080") || strings.Contains(out, "dnat to 192.168.1.50:80") {
		t.Errorf("secure appliance profile emitted a forbidden WAN port forward")
	}
}

func TestGeneratePPPoE(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Username = "user@isp.com"
	cfg.WAN.Password = "secretpass"

	bundle, err := GeneratePPPoE(&cfg)
	if err != nil {
		t.Fatalf("GeneratePPPoE failed: %v", err)
	}

	if !strings.Contains(bundle.PeerConfig, "user@isp.com") {
		t.Errorf("Expected peer config to contain username")
	}
	if !strings.Contains(bundle.ChapSecrets, "secretpass") {
		t.Errorf("Expected chap-secrets to contain password")
	}
}

func TestGenerateDnsmasq(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DHCP.RangeStart = "192.168.1.10"
	cfg.DHCP.RangeEnd = "192.168.1.50"

	out, err := GenerateDnsmasq(&cfg)
	if err != nil {
		t.Fatalf("GenerateDnsmasq failed: %v", err)
	}

	if !strings.Contains(out, "dhcp-range=192.168.1.10,192.168.1.50,255.255.255.0,12h") {
		t.Errorf("Expected dnsmasq config to contain rendered dhcp-range")
	}
}
