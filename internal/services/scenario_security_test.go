package services

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func scenarioProxyConfig(t *testing.T) config.SystemConfig {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "test-user"
	cfg.WAN.Password = "test-password-long-enough"
	cfg.LAN.Interface = "eth1"
	cfg.SquidProxy.Enabled = true
	cfg.SquidProxy.Port = 3128
	cfg.SquidProxy.Username = "proxyadmin"
	cfg.SquidProxy.Password = "proxy-password-long-enough"
	cfg.WireGuard.Enabled = true
	cfg.WireGuard.Interface = "wg0"
	cfg.WireGuard.PrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	cfg.WireGuard.Address = "10.8.0.1/24"
	cfg.Firewall.ExtraLANs = []config.ExtraLANConfig{{
		ID: "immich", Name: "Immich", Interface: "eth2", CIDR: "10.20.30.0/24",
		RouterAddress: "10.20.30.1/24", DstIP: "10.20.30.10", DstPort: 2283,
		AllowFrom: []string{"192.168.1.0/24"}, Enabled: true,
	}}
	return cfg
}

func TestSquidRejectsPrivateAndConfiguredRouterZones(t *testing.T) {
	cfg := scenarioProxyConfig(t)
	out, err := GenerateSquidConfig(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"acl blocked_dst dst ",
		"10.0.0.0/8",
		"100.64.0.0/10",
		"127.0.0.0/8",
		"192.168.0.0/16",
		"10.8.0.0/24",
		"10.20.30.0/24",
		"http_access deny blocked_dst",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Squid isolation is missing %q", want)
		}
	}
	if strings.Index(out, "http_access deny blocked_dst") > strings.Index(out, "http_access allow localnet authenticated") {
		t.Fatal("destination isolation must precede the authenticated allow")
	}
}

func TestNftablesSquidEgressIsWANOnlyAndPrivateDeniesPrecedeEstablished(t *testing.T) {
	cfg := scenarioProxyConfig(t)
	rules, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`meta skuid squid oifname "eth1" drop`,
		`meta skuid squid oifname "eth2" drop`,
		`meta skuid squid oifname "wg0" drop`,
		`meta skuid squid oifname "eth0" tcp dport { 80, 443 } accept`,
		`meta skuid squid oifname "ppp*" tcp dport { 80, 443 } accept`,
	} {
		if !strings.Contains(rules, want) {
			t.Fatalf("nftables proxy isolation is missing %q", want)
		}
	}
	output := rules[strings.Index(rules, "chain output {"):]
	deny := strings.Index(output, `meta skuid squid oifname "eth2" drop`)
	established := strings.Index(output, "ct state established,related accept")
	if deny < 0 || established < 0 || deny > established {
		t.Fatal("Squid private-zone deny must precede established/related acceptance")
	}
	if strings.Contains(rules, "meta skuid squid tcp dport { 80, 443 } accept") {
		t.Fatal("unscoped Squid web egress rule reappeared")
	}
}

func TestNftablesGeneratorRejectsManagementKillerRule(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Firewall.CustomRules = []config.FirewallRule{{
		ID: "deny-admin", Name: "deny admin", Direction: "input", Action: "deny",
		Protocol: "tcp", DstPort: cfg.System.HTTPSPort, Enabled: true,
	}}
	if _, err := GenerateNftables(&cfg); err == nil {
		t.Fatal("nftables generator accepted a custom input deny that can sever management")
	}
}

func TestDnsmasqPersistsLeasesAndListensOnWGServer(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WireGuard.Enabled = true
	cfg.WireGuard.Interface = "wg0"
	cfg.WireGuard.PrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	cfg.WireGuard.Address = "10.8.0.1/24"
	out, err := GenerateDnsmasq(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "interface=wg0") || !strings.Contains(out, "listen-address=127.0.0.1,192.168.1.1,10.8.0.1") {
		t.Fatal("dnsmasq is not bound to authenticated wg0 clients")
	}
	if !strings.Contains(out, "dhcp-leasefile=/var/lib/minimalrouter/dnsmasq.leases") {
		t.Fatal("DHCP leases are not persisted outside /run")
	}
}
