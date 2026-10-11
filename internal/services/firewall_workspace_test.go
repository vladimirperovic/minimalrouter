package services

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestFirewallScopedDenyAndCounterIdentity(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = true
	r := config.FirewallRule{ID: "test", Name: "DNS deny", Action: "deny", Direction: "forward", Protocol: "tcp", DstPort: 53, Enabled: true, Scope: "wan"}
	cfg.Firewall.CustomRules = []config.FirewallRule{r}
	output, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	name := FirewallRuleCounter(r)
	if !strings.Contains(output, "tcp dport 53 oifname { "+cfg.WAN.Interface+", ppp* } counter name "+name+" drop") {
		t.Fatal("WAN restriction missing")
	}
	renamed := r
	renamed.Name = "Renamed"
	renamed.PresetID = "dns"
	if FirewallRuleCounter(renamed) != name {
		t.Fatal("display metadata changed match identity")
	}
	r.Scope = ""
	cfg.Firewall.CustomRules[0] = r
	legacy, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(legacy, "tcp dport 53 counter name "+FirewallRuleCounter(r)+" drop") || FirewallRuleCounter(r) == name {
		t.Fatal("legacy all-zone scope changed or counter identity reused")
	}
	cfg.Firewall.CustomRules[0].Scope = "wan"
	cfg.WAN.Enabled = false
	disabled, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(disabled, "counter "+name+" ") || strings.Contains(disabled, "counter name "+name) {
		t.Fatal("WAN rule emitted without WAN")
	}
}

func TestFirewallFlowMatchesPolicyBoundaries(t *testing.T) {
	base := config.DefaultConfig()
	base.WAN.Enabled = true
	base.WireGuard.Enabled = true
	base.WGClient.Enabled = true
	base.WGClient.AllowedIPs = []string{"172.20.0.0/24"}
	base.Firewall.CustomRules = []config.FirewallRule{{ID: "dns", Name: "DNS deny", Action: "deny", Direction: "forward", Protocol: "tcp", DstPort: 53, Enabled: true, Scope: "wan"}}
	for _, tc := range []struct {
		name, src, dst, proto string
		port                  int
		verdict, rule         string
	}{
		{"WAN deny", "192.168.1.50", "1.1.1.1", "tcp", 53, "deny", "dns"},
		{"private tunnel unaffected", "192.168.1.50", "172.20.0.8", "tcp", 53, "allow", ""},
		{"router DNS unaffected", "192.168.1.50", "192.168.1.1", "tcp", 53, "allow", ""},
		{"LAN direct", "192.168.1.50", "192.168.1.30", "tcp", 53, "not_routed", ""},
		{"WAN HTTPS", "192.168.1.50", "1.1.1.1", "tcp", 443, "allow", ""},
		{"remote initiation", "172.20.0.8", "192.168.1.30", "tcp", 443, "deny", ""},
		{"unknown route", "192.168.1.50", "172.21.0.8", "tcp", 443, "unknown", ""},
		{"WAN source", "8.8.8.8", "1.1.1.1", "tcp", 443, "unknown", ""},
		{"WireGuard peers are routed", "10.8.0.2", "10.8.0.3", "tcp", 443, "deny", ""},
		{"LAN management", "192.168.1.50", "192.168.1.1", "tcp", 8443, "allow", ""},
		{"WG DNS", "10.8.0.2", "192.168.1.1", "udp", 53, "allow", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := ExplainFirewallFlow(base, FirewallFlow{tc.src, tc.dst, tc.proto, tc.port})
			if err != nil || v.Verdict != tc.verdict || v.RuleID != tc.rule || v.Basis != "configured_policy" || len(v.Limitations) == 0 {
				t.Fatalf("%+v %v", v, err)
			}
		})
	}
	base.Firewall.CustomRules[0].Scope = ""
	v, _ := ExplainFirewallFlow(base, FirewallFlow{"192.168.1.50", "172.20.0.8", "tcp", 53})
	if v.Verdict != "deny" {
		t.Fatal("legacy tunnel deny lost")
	}
	base.System.ManagementAccess = "wireguard_only"
	v, _ = ExplainFirewallFlow(base, FirewallFlow{"192.168.1.50", "192.168.1.1", "tcp", 8443})
	if v.Verdict != "deny" {
		t.Fatal("LAN management allowed in WG-only mode")
	}
	base.AdGuard.Enabled = true
	base.AdGuard.DeviceProfiles = []config.DeviceProfile{{ID: "kids", Enabled: true, IPAddresses: []string{"192.168.1.50"}, Services: []string{"youtube"}}}
	v, _ = ExplainFirewallFlow(base, FirewallFlow{"192.168.1.50", "1.1.1.1", "tcp", 443})
	if v.Verdict != "unknown" {
		t.Fatal("guessed dynamic schedule")
	}
	for _, flow := range []FirewallFlow{{"::1", "1.1.1.1", "tcp", 443}, {"192.168.1.50", "1.1.1.1", "tcp", 0}, {"192.168.1.50", "1.1.1.1", "icmp", 53}, {"192.168.1.50", "1.1.1.1", "any", 0}} {
		if _, err := ExplainFirewallFlow(base, flow); err == nil {
			t.Fatalf("accepted invalid flow %+v", flow)
		}
	}
}

func TestFirewallPreviewReportsMatchChangesAndWarnings(t *testing.T) {
	before := config.DefaultConfig()
	r := config.FirewallRule{ID: "one", Name: "Example", Action: "deny", Direction: "forward", Protocol: "tcp", DstPort: 53, Enabled: true}
	before.Firewall.CustomRules = []config.FirewallRule{r}
	after := before.DeepCopy()
	after.Firewall.CustomRules[0].Scope = "wan"
	after.Firewall.CustomRules[0].DstPort = 853
	changes := strings.Join(DescribeFirewallChanges(before.Firewall, after.Firewall), "\n")
	for _, want := range []string{"tcp/53", "tcp/853", "scope all", "scope wan", "Change"} {
		if !strings.Contains(changes, want) {
			t.Fatalf("missing %s in %s", want, changes)
		}
	}
	if len(FirewallWarnings(before)) == 0 || len(FirewallWarnings(after)) != 0 {
		t.Fatal("scope warning missing or stale")
	}
}

func TestFirewallFlowCustomAllowanceAndTranslatedDestination(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WireGuard.Enabled = true
	cfg.Firewall.CustomRules = []config.FirewallRule{{ID: "https", Name: "HTTPS", Direction: "forward", Action: "allow", Protocol: "tcp", DstPort: 443, Enabled: true}}
	result, err := ExplainFirewallFlow(cfg, FirewallFlow{"192.168.1.50", "1.1.1.1", "tcp", 443})
	if err != nil || result.Verdict != "allow" || result.RuleID != "https" {
		t.Fatalf("custom allowance not explained: %+v, %v", result, err)
	}
	cfg.Firewall.PortForwards = []config.PortForwardRule{{ID: "web", Name: "Web", Protocol: "tcp", ExternalPort: 8080, InternalIP: "192.168.1.30", InternalPort: 80, Enabled: true}}
	// The generated DNAT match is based on ingress and port, including when
	// the original destination has no configured route.
	for _, destination := range []string{"10.8.0.1", "172.21.0.8"} {
		result, err = ExplainFirewallFlow(cfg, FirewallFlow{"10.8.0.2", destination, "tcp", 8080})
		if err != nil || result.Verdict != "allow" || result.DestinationZone != "lan" || !strings.Contains(result.Reason, "192.168.1.30:80") {
			t.Fatalf("DNAT was not followed through forwarding: %+v, %v", result, err)
		}
	}
	result, _ = ExplainFirewallFlow(cfg, FirewallFlow{"192.168.1.50", "10.8.0.1", "tcp", 8080})
	if result.Verdict != "deny" || strings.Contains(result.Reason, "DNAT") {
		t.Fatalf("tunnel DNAT leaked to LAN ingress: %+v", result)
	}
}
