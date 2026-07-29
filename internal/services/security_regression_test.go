package services

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestWireGuardInputDropsInvalidBeforeAcceptAndUsesPerSourceMeter(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "subscriber"
	cfg.WAN.Password = "a-valid-pppoe-password"
	cfg.WireGuard.Enabled = true
	cfg.WireGuard.PrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

	rules, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	invalid := strings.Index(rules, "ct state invalid drop")
	accept := strings.Index(rules, "udp dport 51820 accept")
	if invalid < 0 || accept < 0 || invalid > accept {
		t.Fatalf("invalid packets must be dropped before WireGuard accept:\n%s", rules)
	}
	if !strings.Contains(rules, "meter wg_ppp_rate { ip saddr timeout 10s") {
		t.Fatalf("WireGuard PPPoE ingress is missing per-source rate limiting:\n%s", rules)
	}
	if strings.Contains(rules, "limit rate over 100/second burst 200 packets") {
		t.Fatal("legacy global WireGuard limiter is still present")
	}
}

func TestCloudflareDDNSGetsConditionalHTTPSEgress(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Cloudflare.DDNSEnabled = true

	rules, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"meta skuid root tcp dport 443 accept",
		"meta skuid inadyn tcp dport 443 accept",
	} {
		if !strings.Contains(rules, expected) {
			t.Fatalf("DDNS egress rule %q is missing:\n%s", expected, rules)
		}
	}

	cfg.Cloudflare.DDNSEnabled = false
	rules, err = GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rules, "meta skuid inadyn tcp dport 443 accept") {
		t.Fatal("inadyn HTTPS egress must disappear when DDNS is disabled")
	}
}

func TestWireGuardClientProfileIsSplitTunnel(t *testing.T) {
	bundle, err := GenerateClientConfig(
		"clientPrivateKey123=",
		"10.8.0.2/32",
		"serverPublicKey456=",
		"router.example.com:51820",
		"pskKey789=",
		"1.1.1.1",
		"10.8.0.1/24",
		"192.168.1.1/24",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bundle.ConfigText, "AllowedIPs = 10.8.0.0/24, 192.168.1.0/24") {
		t.Fatalf("client profile does not contain WireGuard and LAN split routes:\n%s", bundle.ConfigText)
	}
	if strings.Contains(bundle.ConfigText, "0.0.0.0/0") {
		t.Fatalf("client profile unexpectedly routes Internet through home:\n%s", bundle.ConfigText)
	}
}
