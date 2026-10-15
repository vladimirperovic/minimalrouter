package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func destinationRule() FirewallRule {
	return FirewallRule{
		ID: "r", Name: "Resolvers", Action: "deny", Direction: "forward", Protocol: "tcp",
		DstIPs: []string{"1.1.1.1", "45.90.28.0/24", "2620:fe::fe", "2a07:a8c0::/64"}, DstPorts: []int{443, 853},
		Scope: "wan", Enabled: true,
	}
}

func validateRule(rule FirewallRule) error {
	cfg := DefaultConfig()
	cfg.Firewall.CustomRules = []FirewallRule{rule}
	return cfg.Validate()
}

func TestDestinationRulesAccepted(t *testing.T) {
	for name, mutate := range map[string]func(*FirewallRule){
		"addresses, networks and both families": func(*FirewallRule) {},
		"single destination port":               func(r *FirewallRule) { r.DstPorts, r.DstPort = nil, 443 },
		"UDP":                                   func(r *FirewallRule) { r.Protocol = "udp" },
		"any protocol":                          func(r *FirewallRule) { r.Protocol, r.DstPorts = "any", nil },
		"ICMP to IPv4":                          func(r *FirewallRule) { r.Protocol, r.DstPorts, r.DstIPs = "icmp", nil, []string{"8.8.8.8"} },
		"IPv4 with a LAN source":                func(r *FirewallRule) { r.SrcIP, r.DstIPs = "192.168.1.50", []string{"8.8.8.8"} },
		"multiple ports without destinations":   func(r *FirewallRule) { r.DstIPs = nil },
		"all routed networks":                   func(r *FirewallRule) { r.Scope = "" },
	} {
		rule := destinationRule()
		mutate(&rule)
		if err := validateRule(rule); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDestinationRulesRejected(t *testing.T) {
	tooManyAddresses := make([]string, MaxFirewallRuleDestinations+1)
	for i := range tooManyAddresses {
		tooManyAddresses[i] = fmt.Sprintf("203.0.%d.%d", i/250, i%250+1)
	}
	tooManyPorts := make([]int, MaxFirewallRuleDestinationPorts+1)
	for i := range tooManyPorts {
		tooManyPorts[i] = 1000 + i
	}
	for _, tc := range []struct {
		name, field string
		mutate      func(*FirewallRule)
	}{
		{"input rule", "dst_ips", func(r *FirewallRule) { r.Direction, r.Action, r.Scope = "input", "allow", "" }},
		{"leading zero", "dst_ips", func(r *FirewallRule) { r.DstIPs = []string{"001.1.1.1"} }},
		{"host bits in a network", "dst_ips", func(r *FirewallRule) { r.DstIPs = []string{"45.90.28.5/24"} }},
		{"uncompressed IPv6", "dst_ips", func(r *FirewallRule) { r.DstIPs = []string{"2001:4860:4860:0:0:0:0:8888"} }},
		{"IPv4-mapped IPv6", "dst_ips", func(r *FirewallRule) { r.DstIPs = []string{"::ffff:1.1.1.1"} }},
		{"zone", "dst_ips", func(r *FirewallRule) { r.DstIPs = []string{"2620:fe::fe%eth0"} }},
		{"loopback", "dst_ips", func(r *FirewallRule) { r.DstIPs = []string{"127.0.0.1"} }},
		{"everything", "dst_ips", func(r *FirewallRule) { r.DstIPs = []string{"0.0.0.0/0"} }},
		{"link-local", "dst_ips", func(r *FirewallRule) { r.DstIPs = []string{"fe80::1"} }},
		{"multicast", "dst_ips", func(r *FirewallRule) { r.DstIPs = []string{"224.0.0.251"} }},
		{"hostname", "dst_ips", func(r *FirewallRule) { r.DstIPs = []string{"dns.google"} }},
		{"injection", "dst_ips", func(r *FirewallRule) { r.DstIPs = []string{"1.1.1.1 } accept"} }},
		{"duplicate", "dst_ips", func(r *FirewallRule) { r.DstIPs = []string{"1.1.1.1", "1.1.1.1"} }},
		{"overlap", "dst_ips", func(r *FirewallRule) { r.DstIPs = []string{"45.90.28.0/24", "45.90.28.9"} }},
		{"too many destinations", "dst_ips", func(r *FirewallRule) { r.DstIPs = tooManyAddresses }},
		{"IPv6 with an IPv4 source", "dst_ips", func(r *FirewallRule) { r.SrcIP = "192.168.1.50" }},
		{"IPv6 with ICMP", "dst_ips", func(r *FirewallRule) { r.Protocol, r.DstPorts = "icmp", nil }},
		{"port and port set", "dst_ports", func(r *FirewallRule) { r.DstPort = 443 }},
		{"port set for any protocol", "dst_ports", func(r *FirewallRule) { r.Protocol = "any" }},
		{"port out of range", "dst_ports", func(r *FirewallRule) { r.DstPorts = []int{443, 65536} }},
		{"port zero", "dst_ports", func(r *FirewallRule) { r.DstPorts = []int{0} }},
		{"repeated port", "dst_ports", func(r *FirewallRule) { r.DstPorts = []int{443, 443} }},
		{"too many ports", "dst_ports", func(r *FirewallRule) { r.DstPorts = tooManyPorts }},
		{"no port at all", "dst_port", func(r *FirewallRule) { r.DstPorts = nil }},
	} {
		rule := destinationRule()
		tc.mutate(&rule)
		err := validateRule(rule)
		if err == nil || !strings.Contains(err.Error(), "custom_rules[0]."+tc.field) {
			t.Errorf("%s: want a %s error, got %v", tc.name, tc.field, err)
		}
	}
}

// A release that predates dst_ports sees only dst_port, which is absent, so
// it refuses the rule instead of rendering "drop TCP to every destination".
// The same holds for this release reading the rule without its new fields.
func TestDestinationRuleWithoutItsNewFieldsIsRefusedNotWidened(t *testing.T) {
	encoded, err := json.Marshal(destinationRule())
	if err != nil {
		t.Fatal(err)
	}
	var legacy struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Action    string `json:"action"`
		Direction string `json:"direction"`
		Protocol  string `json:"protocol"`
		DstPort   int    `json:"dst_port,omitempty"`
		Enabled   bool   `json:"enabled"`
		Scope     string `json:"scope,omitempty"`
	}
	if err := json.Unmarshal(encoded, &legacy); err != nil {
		t.Fatal(err)
	}
	rule := FirewallRule{ID: legacy.ID, Name: legacy.Name, Action: legacy.Action, Direction: legacy.Direction, Protocol: legacy.Protocol, DstPort: legacy.DstPort, Enabled: legacy.Enabled, Scope: legacy.Scope}
	if err := validateRule(rule); err == nil || !strings.Contains(err.Error(), "dst_port") {
		t.Fatalf("the stripped rule must be refused for its missing port, got %v", err)
	}
}

func TestLegacyRuleEncodingIsUnchanged(t *testing.T) {
	rule := FirewallRule{ID: "preset-dns-udp", Name: "Block direct external DNS (preset)", Action: "deny", Direction: "forward", Protocol: "udp", DstPort: 53, Enabled: true, Scope: "wan", PresetID: "dns"}
	encoded, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"preset-dns-udp","name":"Block direct external DNS (preset)","action":"deny","direction":"forward","protocol":"udp","dst_port":53,"enabled":true,"scope":"wan","preset_id":"dns"}`
	if string(encoded) != want {
		t.Fatalf("legacy rule encoding changed\nwant %s\ngot  %s", want, encoded)
	}
}

func TestDeepCopyDetachesRuleDestinations(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Firewall.CustomRules = []FirewallRule{destinationRule(), {ID: "plain", Name: "Plain", Action: "deny", Direction: "forward", Protocol: "tcp", DstPort: 25, Enabled: true}}
	copied := cfg.DeepCopy()
	copied.Firewall.CustomRules[0].DstIPs[0] = "9.9.9.9"
	copied.Firewall.CustomRules[0].DstPorts[0] = 1
	if cfg.Firewall.CustomRules[0].DstIPs[0] != "1.1.1.1" || cfg.Firewall.CustomRules[0].DstPorts[0] != 443 {
		t.Fatal("DeepCopy shares destination slices with its source")
	}
	if copied.Firewall.CustomRules[1].DstIPs != nil || copied.Firewall.CustomRules[1].DstPorts != nil {
		t.Fatal("DeepCopy turned absent destinations into empty slices")
	}
}
