package services

import (
	"fmt"
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

func TestNftablesExtraLANIsolatesSegment(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "test"
	cfg.WAN.Password = "long-enough-test-password"
	cfg.LAN.CIDR = "192.168.1.1/24"
	cfg.LAN.Interface = "eth0"
	cfg.WireGuard.Enabled = true
	cfg.WireGuard.PrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	cfg.WireGuard.Address = "10.6.0.1/24"
	cfg.Firewall.ExtraLANs = []config.ExtraLANConfig{{
		ID: "immich", Name: "Immich", Interface: "eth2", CIDR: "10.20.30.0/24",
		DstIP: "10.20.30.10", DstPort: 2283, AllowFrom: []string{"192.168.1.1/24", "10.6.0.0/24"}, Enabled: true,
	}}
	rules, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`iifname "eth2" ip saddr != { 10.20.30.0/24 } drop`,
		`iifname "eth2" ip protocol icmp accept`,
		`iifname "eth0" ip saddr 192.168.1.1/24 ip daddr 10.20.30.10 tcp dport 2283 oifname "eth2" accept`,
		`iifname "wg0" ip saddr 10.6.0.0/24 ip daddr 10.20.30.10 tcp dport 2283 oifname "eth2" accept`,
	} {
		if !strings.Contains(rules, expected) {
			t.Fatalf("extra LAN rule is missing %q", expected)
		}
	}
	// The isolated segment must have no egress path at all: input keeps the
	// router ICMP minimum, forward rules only allow the explicit LAN/wg0
	// service initiations toward the segment.
	for _, forbidden := range []string{
		`oifname "ppp*" ip saddr 10.20.30`,
		`iifname "eth2" oifname "eth0"`,
		`iifname "eth2" oifname "ppp*"`,
		`iifname "eth2" oifname "wg0"`,
		`iifname "eth2" oifname "wg1"`,
	} {
		if strings.Contains(rules, forbidden) {
			t.Fatalf("extra LAN leaks egress: %s", forbidden)
		}
	}
	// Every forward-chain rule for the segment must carry the oifname guard so
	// a DstIP mistake can never open a path into a different network.
	inForward := false
	for _, line := range strings.Split(rules, "\n") {
		line = strings.TrimSpace(line)
		if line == "chain forward {" {
			inForward = true
			continue
		}
		if strings.HasPrefix(line, "}") {
			inForward = false
			continue
		}
		if !inForward || !strings.HasPrefix(line, "iifname") || !strings.Contains(line, "10.20.30.10") {
			continue
		}
		if !strings.Contains(line, `oifname "eth2"`) {
			t.Fatalf("extra LAN forward rule lacks oifname guard: %s", line)
		}
	}
}

// TestExtraLANCannotInitiateEgress proves the generated ruleset gives an
// isolated segment no initiation path toward LAN, WAN, wg0 or wg1 for TCP,
// UDP or ICMP: any rule that could accept a NEW packet leaving the segment is
// a regression.
func TestExtraLANCannotInitiateEgress(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "test"
	cfg.WAN.Password = "long-enough-test-password"
	cfg.LAN.CIDR = "192.168.1.1/24"
	cfg.LAN.Interface = "eth0"
	cfg.WireGuard.Enabled = true
	cfg.WireGuard.PrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	cfg.WireGuard.Address = "10.6.0.1/24"
	cfg.WGClient.Enabled = true
	cfg.WGClient.Interface = "wg1"
	cfg.WGClient.PrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	cfg.WGClient.PublicKey = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA="
	cfg.WGClient.Address = "10.7.0.2/32"
	cfg.WGClient.Endpoint = "office.example.com:51820"
	cfg.WGClient.AllowedIPs = []string{"10.9.0.0/24"}
	cfg.Firewall.ExtraLANs = []config.ExtraLANConfig{{
		ID: "media", Name: "Media", Interface: "eth2", CIDR: "10.20.30.0/24",
		DstIP: "10.20.30.10", DstPort: 2283, AllowFrom: []string{"192.168.1.1/24"}, Enabled: true,
	}}
	rules, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	destinations := []string{`oifname "eth0"`, `oifname "ppp*"`, `oifname "wg0"`, `oifname "wg1"`, `oifname "br-lan"`}
	protocols := []string{"tcp", "udp", "icmp"}
	for _, dst := range destinations {
		for _, proto := range protocols {
			line := fmt.Sprintf(`iifname "eth2" ip protocol %s %s`, proto, dst)
			if strings.Contains(rules, line) {
				t.Fatalf("extra LAN can initiate %s toward %s: %s", proto, dst, line)
			}
		}
	}
	// The only accept rules that match a packet arriving on the isolated
	// interface must be the ICMP input minimum and replies riding the global
	// established rule.
	for _, line := range strings.Split(rules, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, `iifname "eth2"`) || !strings.Contains(line, " accept") {
			continue
		}
		if strings.Contains(line, "ct state established") {
			continue
		}
		if strings.Contains(line, "ip protocol icmp") && !strings.Contains(line, "dport") {
			continue
		}
		t.Fatalf("extra LAN interface has an unsolicited accept: %s", line)
	}
}

// TestEnablingWGClientDoesNotGrantExtraLANEgress is the regression test for
// the removed automatic extra-LAN -> wg1 forward source list: enabling wg1
// must not change the isolation guarantees of an enabled extra LAN.
func TestEnablingWGClientDoesNotGrantExtraLANEgress(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.LAN.CIDR = "192.168.1.1/24"
	cfg.LAN.Interface = "eth0"
	cfg.Firewall.ExtraLANs = []config.ExtraLANConfig{{
		ID: "media", Name: "Media", Interface: "eth2", CIDR: "10.20.30.0/24",
		DstIP: "10.20.30.10", DstPort: 2283, AllowFrom: []string{"192.168.1.1/24"}, Enabled: true,
	}}

	base, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	cfg.WGClient.Enabled = true
	cfg.WGClient.Interface = "wg1"
	cfg.WGClient.PrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	cfg.WGClient.PublicKey = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA="
	cfg.WGClient.Address = "10.7.0.2/32"
	cfg.WGClient.Endpoint = "office.example.com:51820"
	cfg.WGClient.AllowedIPs = []string{"10.9.0.0/24"}
	withWG1, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(withWG1, `iifname "eth2" oifname "wg1"`) {
		t.Fatal("extra LAN interface is allowed to dial out through wg1")
	}
	for _, line := range strings.Split(withWG1, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, `iifname "eth2"`) || !strings.Contains(line, " accept") {
			continue
		}
		if strings.Contains(line, "ct state established") {
			continue
		}
		if strings.Contains(line, "ip protocol icmp") && !strings.Contains(line, "dport") {
			continue
		}
		t.Fatalf("extra LAN gained an unsolicited accept after enabling wg1: %s", line)
	}
	_ = base
}

// TestExtraLANNameInjectionCannotEscapeComment proves user-controlled extra
// LAN names can never produce additional nftables syntax: validation rejects
// control characters, and the generator additionally strips them from
// comments as defense in depth.
func TestExtraLANNameInjectionCannotEscapeComment(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Firewall.ExtraLANs = []config.ExtraLANConfig{{
		ID: "x", Name: "media", Interface: "eth2", CIDR: "10.20.30.0/24",
		RouterAddress: "10.20.30.1/24", DstIP: "10.20.30.10", DstPort: 2283,
		AllowFrom: []string{cfg.LAN.CIDR}, Enabled: true,
	}}

	for _, attack := range []string{
		"media\nadd rule inet minimalrouter output accept",
		"media\r\nadd rule inet minimalrouter output accept",
		"media\tadd chain inet minimalrouter evil",
		"media\" ; add rule inet minimalrouter output accept",
		"# comment injection\n",
	} {
		cfg.Firewall.ExtraLANs[0].Name = attack
		if err := cfg.Validate(); err == nil {
			t.Fatalf("validation accepted an injected extra LAN name %q", attack)
		}
		// The generator must be safe even when called with an unvalidated name:
		// control characters are neutralized so the name can never leave its
		// comment, and any line that still echoes the injected fragment must be
		// a comment line.
		rules, genErr := GenerateNftables(&cfg)
		if genErr != nil {
			t.Fatalf("generator rejected config with name %q: %v", attack, genErr)
		}
		if strings.ContainsRune(rules, '\r') {
			t.Fatalf("injected name %q left a raw carriage return in the ruleset", attack)
		}
		for _, line := range strings.Split(rules, "\n") {
			if strings.Contains(line, "add rule inet minimalrouter output accept") ||
				strings.Contains(line, "add chain inet minimalrouter evil") {
				if !strings.HasPrefix(strings.TrimSpace(line), "#") {
					t.Fatalf("injected name %q escaped its comment: %s", attack, line)
				}
			}
		}
	}

	// A very long name must be rejected by validation.
	cfg.Firewall.ExtraLANs[0].Name = strings.Repeat("a", 65)
	if err := cfg.Validate(); err == nil {
		t.Fatal("validation accepted an oversized extra LAN name")
	}

	// A legitimately safe name must still generate the expected comment.
	cfg.Firewall.ExtraLANs[0].Name = "media (isolated)"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("safe extra LAN name rejected: %v", err)
	}
	rules, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rules, "# Extra LAN eth2 (media (isolated)):") {
		t.Fatal("safe extra LAN name did not produce its comment")
	}
}

// TestSquidRestrictedIPsPrecedeEstablishedAccept proves a device already
// holding an active direct flow is cut immediately when its RestrictedIP
// policy is enabled: the deny rule must appear before the established/related
// acceptance in the forward chain.
func TestSquidRestrictedIPsPrecedeEstablishedAccept(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "test"
	cfg.WAN.Password = "long-enough-test-password"
	cfg.SquidProxy.Enabled = true
	cfg.SquidProxy.Password = "super-secret-password-123"
	cfg.SquidProxy.RestrictedIPs = []config.RestrictedIPItem{
		{Hostname: "console", IPAddress: "192.168.1.42", Enabled: true},
	}
	rules, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rules, "ip saddr 192.168.1.42 drop") {
		t.Fatal("restricted IP deny rule is missing")
	}
	// Scope the ordering check to the forward chain: the input chain carries
	// its own established/related acceptance before the forward chain appears.
	forward := rules[strings.Index(rules, "chain forward {"):]
	restrictedIndex := strings.Index(forward, "ip saddr 192.168.1.42 drop")
	establishedIndex := strings.Index(forward, "ct state established,related accept")
	if restrictedIndex < 0 || establishedIndex < 0 {
		t.Fatal("expected rules are missing")
	}
	if restrictedIndex > establishedIndex {
		t.Fatal("restricted IP deny rule must precede the established/related accept")
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

	for _, expected := range []string{
		`iifname "eth0" udp dport 51820 accept`,
		`iifname "ppp*" udp dport 51820 accept`,
		`meter wg_wan_rate { ip saddr timeout 10s`,
		`meter wg_ppp_rate { ip saddr timeout 10s`,
	} {
		if !strings.Contains(rules, expected) {
			t.Fatalf("WireGuard WAN rule is missing %q", expected)
		}
	}

	for _, line := range strings.Split(rules, "\n") {
		line = strings.TrimSpace(line)
		isWANRule := strings.Contains(line, `iifname "eth0"`) || strings.Contains(line, `iifname "ppp*"`)
		if !isWANRule || !strings.Contains(line, " accept") || !strings.Contains(line, "dport") {
			continue
		}
		if !strings.Contains(line, "udp dport 51820") {
			t.Fatalf("WAN exposes a non-WireGuard service: %s", line)
		}
	}

	for _, forbidden := range []string{
		`iifname "eth0" tcp dport`,
		`iifname "ppp*" tcp dport`,
		"dnat to",
	} {
		if strings.Contains(rules, forbidden) {
			t.Fatalf("WAN exposes a non-WireGuard entry point: %s", forbidden)
		}
	}
}
