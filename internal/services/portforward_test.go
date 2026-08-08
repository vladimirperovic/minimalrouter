package services_test

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/services"
)

// TestPortForwardRealWorldShape — the lab's exact use case: forward the
// router's WG address 10.6.0.1:4080 to the opencode host.
func TestPortForwardRealWorldShape(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "mr-test"
	cfg.WAN.Password = "x"
	cfg.WireGuard.Enabled = true
	cfg.WireGuard.Interface = "wg0"
	cfg.WireGuard.PrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	cfg.WireGuard.Address = "10.6.0.1/24"
	cfg.Firewall.PortForwards = []config.PortForwardRule{
		{ID: "pf-opencode", Name: "opencode", Protocol: "tcp", ExternalPort: 4080, InternalIP: "192.168.1.161", InternalPort: 4080, Enabled: true},
		{ID: "pf-ssh", Name: "ssh-lxc", Protocol: "tcp", ExternalPort: 22, InternalIP: "192.168.1.161", InternalPort: 22, Enabled: true},
		{ID: "pf-both", Name: "vpn-udp", Protocol: "both", ExternalPort: 51899, InternalIP: "192.168.1.2", InternalPort: 51899, Enabled: true},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	out, err := services.GenerateNftables(&cfg)
	if err != nil {
		t.Fatalf("GenerateNftables: %v", err)
	}
	for _, want := range []string{
		`iifname "wg0" tcp dport 4080 dnat to 192.168.1.161:4080`,
		`iifname "wg0" tcp dport 22 dnat to 192.168.1.161:22`,
		`iifname "wg0" tcp dport 51899 dnat to 192.168.1.2:51899`,
		`iifname "wg0" udp dport 51899 dnat to 192.168.1.2:51899`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing rule: %s", want)
		}
	}
	// fail closed: disabled rule must not render
	if strings.Contains(out, "dport 9999") {
		t.Error("disabled forward rendered")
	}
	t.Logf("nftables dnat section:\n%s", rulesSection(out))
}

func rulesSection(out string) string {
	start := strings.Index(out, "chain dnat")
	end := strings.Index(out[start:], "\n  }\n")
	if start < 0 || end < 0 {
		return "(no dnat chain)"
	}
	return out[start : start+end]
}
