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
	if !strings.Contains(bundle.PeerConfig, "\nnoipv6\n") {
		t.Errorf("Expected peer config to disable IPv6CP when appliance IPv6 is disabled")
	}
	if !strings.Contains(bundle.PeerConfig, "\nnoipdefault\n") {
		t.Errorf("Expected peer config to obtain the dynamic local IPv4 address from the ISP")
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
	if !strings.Contains(out, "dhcp-leasefile=/var/lib/minimalrouter/dnsmasq.leases") {
		t.Errorf("Expected dnsmasq leases to survive appliance reboot")
	}
	if strings.Contains(out, "dhcp-leasefile=/run/") {
		t.Errorf("DHCP lease database must not live on the volatile /run filesystem")
	}
}

func TestGenerateDnsmasqStaticRecords(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DNS.Records = []config.DNSRecord{
		{Name: "immich.home.arpa", IP: "10.20.30.10"},
		{Name: "nas.home.arpa", IP: "192.168.1.2"},
	}
	out, err := GenerateDnsmasq(&cfg)
	if err != nil {
		t.Fatalf("GenerateDnsmasq: %v", err)
	}
	for _, want := range []string{
		"host-record=immich.home.arpa,10.20.30.10",
		"host-record=nas.home.arpa,192.168.1.2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dnsmasq config missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateWireGuardClientRuntime(t *testing.T) {
	cfg := config.WGClientConfig{
		Enabled:             true,
		Interface:           "wg1",
		PrivateKey:          "WXK/gT9H1IPzj59FYyi7AERtHnpOqjR9nlUBFzYXjUU=",
		Address:             "10.7.0.2/32",
		PublicKey:           "DTSyebsPi8mscQzOPRpiarNste8XHvViiVVNpnZQ7AY=",
		Endpoint:            "office.example.com:51820",
		AllowedIPs:          []string{"10.7.0.0/24", "10.7.1.0/24"},
		PersistentKeepalive: 25,
	}
	out, err := GenerateWireGuardClientRuntime(&cfg)
	if err != nil {
		t.Fatalf("GenerateWireGuardClientRuntime: %v", err)
	}
	for _, want := range []string{
		"[Interface]",
		"PrivateKey = WXK/gT9H1IPzj59FYyi7AERtHnpOqjR9nlUBFzYXjUU=", // gitleaks:allow -- synthetic test fixture
		"[Peer]",
		"PublicKey = DTSyebsPi8mscQzOPRpiarNste8XHvViiVVNpnZQ7AY=",
		"AllowedIPs = 10.7.0.0/24, 10.7.1.0/24",
		"Endpoint = office.example.com:51820",
		"PersistentKeepalive = 25",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("client config missing %q:\n%s", want, out)
		}
	}

	cfg.AllowedIPs = []string{"10.7.0.0/24", "10.7.0.0/24"}
	if _, err := GenerateWireGuardClientRuntime(&cfg); err == nil {
		t.Error("duplicate allowed network must be rejected")
	}
	cfg.AllowedIPs = nil
	if _, err := GenerateWireGuardClientRuntime(&cfg); err == nil {
		t.Error("empty allowed networks must be rejected")
	}

	// An explicit zero keepalive means "disabled" and must reach wg verbatim,
	// never be coerced to the 25-second default (which would keep a tunnel
	// alive the administrator explicitly turned off).
	cfg.AllowedIPs = []string{"10.7.0.0/24"}
	cfg.PersistentKeepalive = 0
	out, err = GenerateWireGuardClientRuntime(&cfg)
	if err != nil {
		t.Fatalf("GenerateWireGuardClientRuntime(keepalive=0): %v", err)
	}
	if !strings.Contains(out, "PersistentKeepalive = 0") {
		t.Errorf("keepalive 0 must be emitted verbatim:\n%s", out)
	}
}

func TestGenerateNftablesWireGuardClient(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WGClient = config.WGClientConfig{
		Enabled:    true,
		Interface:  "wg1",
		Address:    "10.7.0.2/32",
		Endpoint:   "office.example.com:51820",
		AllowedIPs: []string{"10.7.0.0/24"},
		PrivateKey: "WXK/gT9H1IPzj59FYyi7AERtHnpOqjR9nlUBFzYXjUU=", // gitleaks:allow -- synthetic test fixture
		PublicKey:  "DTSyebsPi8mscQzOPRpiarNste8XHvViiVVNpnZQ7AY=",
	}
	out, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatalf("GenerateNftables: %v", err)
	}
	for _, want := range []string{
		`iifname "wg1" ip saddr != { 10.7.0.0/24 } drop`,
		`iifname "wg1" ct state new drop`,
		`iifname "eth1" oifname "wg1" accept`,
		`oifname "ppp*" udp dport 51820 accept`,
		`oifname "wg1" masquerade`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("nftables output missing %q", want)
		}
	}
	if strings.Contains(out, `iifname "wg1" ip saddr != { 10.7.0.0/24 } drop`) == false {
		t.Error("wg1 anti-spoof missing")
	}
}
