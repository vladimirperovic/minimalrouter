package services

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// publicResolverList is the dashboard's "Block public DNS resolvers by
// address" preset source. The appliance has no copy of it: the dashboard
// writes it into two custom rules. These tests load the same file so a list
// edit the appliance would refuse, or render differently, fails here.
type publicResolverList struct {
	Ports     []int `json:"ports"`
	Providers []struct {
		Name      string   `json:"name"`
		Addresses []string `json:"addresses"`
	} `json:"providers"`
}

func loadPublicResolverList(t *testing.T) publicResolverList {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "lib", "publicResolvers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var list publicResolverList
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	return list
}

// publicResolverRules mirrors applyPresets in web/src/lib/firewall.ts.
func publicResolverRules(list publicResolverList) []config.FirewallRule {
	var addresses []string
	for _, provider := range list.Providers {
		addresses = append(addresses, provider.Addresses...)
	}
	rules := []config.FirewallRule{}
	for _, protocol := range []string{"tcp", "udp"} {
		rules = append(rules, config.FirewallRule{
			ID: "preset-public-resolvers-" + protocol, PresetID: "public-resolvers",
			Name: "Block public DNS resolvers by address (preset)", Action: "deny",
			Direction: "forward", Protocol: protocol, DstIPs: addresses,
			DstPorts: list.Ports, Scope: "wan", Enabled: true,
		})
	}
	return rules
}

func publicResolverConfig(t *testing.T) (config.SystemConfig, publicResolverList) {
	t.Helper()
	list := loadPublicResolverList(t)
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "user@isp.example"
	cfg.WAN.Password = "test-only-password"
	// The router's own upstream is one of the blocked resolvers, which is the
	// common case (the default is Cloudflare).
	cfg.DHCP.DNSServers = []string{"1.1.1.1", "8.8.8.8"}
	cfg.Firewall.CustomRules = publicResolverRules(list)
	return cfg, list
}

// nftChains maps each chain name in the generated table to its body lines.
func nftChains(ruleset string) map[string][]string {
	chains := map[string][]string{}
	current := ""
	for _, line := range strings.Split(ruleset, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "chain ") && strings.HasSuffix(trimmed, "{") {
			current = strings.Fields(trimmed)[1]
			continue
		}
		if trimmed == "}" {
			current = ""
			continue
		}
		if current != "" && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			chains[current] = append(chains[current], trimmed)
		}
	}
	return chains
}

func TestPublicResolverListIsAcceptedByTheAppliance(t *testing.T) {
	cfg, list := publicResolverConfig(t)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the dashboard's resolver list must pass appliance validation: %v", err)
	}
	if want := []int{443, 853, 5353, 8853}; !slices.Equal(list.Ports, want) {
		t.Fatalf("ports = %v, want %v", list.Ports, want)
	}
	seen := map[string]bool{}
	for _, provider := range list.Providers {
		if provider.Name == "" || len(provider.Addresses) == 0 {
			t.Fatalf("provider entry without a name or addresses: %+v", provider)
		}
		for _, address := range provider.Addresses {
			if seen[address] {
				t.Fatalf("%s is listed twice", address)
			}
			seen[address] = true
		}
	}
	for _, required := range []string{"1.1.1.1", "1.0.0.1", "8.8.8.8", "8.8.4.4", "9.9.9.9", "149.112.112.112", "208.67.222.222", "208.67.220.220", "94.140.14.14", "94.140.15.15", "45.90.28.0/24", "45.90.30.0/24"} {
		if !seen[required] {
			t.Errorf("%s is missing from the resolver list", required)
		}
	}
}

func TestPublicResolverRulesDropOnlyForwardedLANTraffic(t *testing.T) {
	cfg, _ := publicResolverConfig(t)
	ruleset, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	chains := nftChains(ruleset)
	ipv4, ipv6 := config.FirewallRuleDestinations(cfg.Firewall.CustomRules[0])
	if len(ipv4) == 0 || len(ipv6) == 0 {
		t.Fatal("the list must carry both address families")
	}
	lan := `iifname "` + cfg.LAN.Interface + `"`
	wan := "oifname { " + cfg.WAN.Interface + ", ppp* }"
	ports := "dport { 443, 853, 5353, 8853 }"

	established := -1
	for i, line := range chains["forward"] {
		if line == "ct state established,related accept" {
			established = i
			break
		}
	}
	if established < 0 {
		t.Fatal("forward chain lost its established/related acceptance")
	}
	for _, rule := range cfg.Firewall.CustomRules {
		counter := FirewallRuleCounter(rule)
		want := []string{
			lan + " ip daddr { " + strings.Join(ipv4, ", ") + " } " + rule.Protocol + " " + ports + " " + wan + " counter name " + counter + " drop",
			lan + " ip6 daddr { " + strings.Join(ipv6, ", ") + " } " + rule.Protocol + " " + ports + " " + wan + " counter name " + counter + " drop",
		}
		for _, line := range want {
			index := slices.Index(chains["forward"], line)
			if index < 0 {
				t.Fatalf("forward chain is missing\n%s\nin\n%s", line, ruleset)
			}
			// Denies precede established acceptance, so a DoH session that was
			// open before the change is cut as well.
			if index > established {
				t.Fatalf("resolver deny runs after established acceptance: %s", line)
			}
		}
		if !strings.Contains(ruleset, "counter "+counter+" { }") {
			t.Fatalf("counter %s is not declared", counter)
		}
	}

	// Router-originated traffic never traverses the forward hook. Its upstream
	// DNS to 1.1.1.1/8.8.8.8 stays allowed and no other chain mentions the
	// resolver list.
	for name, lines := range chains {
		for _, line := range lines {
			if name == "forward" {
				if strings.Contains(line, "daddr") && strings.Contains(line, "1.1.1.1") && !strings.HasPrefix(line, lan+" ") {
					t.Fatalf("resolver deny is not limited to LAN ingress: %s", line)
				}
				continue
			}
			if strings.Contains(line, "45.90.28.0/24") || strings.Contains(line, "2620:fe::fe") || strings.Contains(line, "dport { 443, 853, 5353, 8853 }") {
				t.Fatalf("chain %s carries a resolver rule: %s", name, line)
			}
		}
	}
	for _, line := range []string{"ip daddr { 1.1.1.1, 8.8.8.8 } udp dport 53 accept", "ip daddr { 1.1.1.1, 8.8.8.8 } tcp dport 53 accept"} {
		if slices.Index(chains["output"], line) < 0 {
			t.Fatalf("router upstream DNS is no longer allowed: %s\n%s", line, strings.Join(chains["output"], "\n"))
		}
	}
}

func TestPublicResolverRulesExplainLANFlows(t *testing.T) {
	cfg, _ := publicResolverConfig(t)
	for _, tc := range []struct {
		name, dst, proto string
		port             int
		verdict, rule    string
	}{
		{"DoH by IP literal", "1.1.1.1", "tcp", 443, "deny", "preset-public-resolvers-tcp"},
		{"DNS over HTTP/3", "8.8.8.8", "udp", 443, "deny", "preset-public-resolvers-udp"},
		{"DNS over QUIC", "94.140.14.14", "udp", 853, "deny", "preset-public-resolvers-udp"},
		{"alternate-port DNS", "208.67.222.222", "udp", 5353, "deny", "preset-public-resolvers-udp"},
		{"alternate DoQ port", "9.9.9.9", "udp", 8853, "deny", "preset-public-resolvers-udp"},
		{"NextDNS profile address", "45.90.28.17", "tcp", 853, "deny", "preset-public-resolvers-tcp"},
		{"port outside the set", "1.1.1.1", "tcp", 80, "allow", ""},
		{"unlisted destination", "93.184.216.34", "tcp", 443, "allow", ""},
		{"neighbour of a listed host", "1.1.1.4", "tcp", 443, "allow", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExplainFirewallFlow(cfg, FirewallFlow{SourceIP: "192.168.1.50", DestinationIP: tc.dst, Protocol: tc.proto, Port: tc.port})
			if err != nil || got.Verdict != tc.verdict || got.RuleID != tc.rule {
				t.Fatalf("got %+v, %v; want %s by %q", got, err, tc.verdict, tc.rule)
			}
		})
	}
}

func TestDestinationRuleRendering(t *testing.T) {
	base := config.DefaultConfig()
	lan := `iifname "` + base.LAN.Interface + `"`
	for _, tc := range []struct {
		name string
		rule config.FirewallRule
		want []string
		deny []string
	}{
		{
			name: "single IPv4 destination and port",
			rule: config.FirewallRule{ID: "a", Name: "a", Action: "deny", Direction: "forward", Protocol: "tcp", DstPort: 443, DstIPs: []string{"1.1.1.1"}, Enabled: true},
			want: []string{lan + " ip daddr 1.1.1.1 tcp dport 443 counter name "},
			deny: []string{"ip6 daddr"},
		},
		{
			name: "source stays first and IPv6 is impossible",
			rule: config.FirewallRule{ID: "b", Name: "b", Action: "deny", Direction: "forward", Protocol: "udp", SrcIP: "192.168.1.50", DstPorts: []int{53, 5353}, DstIPs: []string{"8.8.8.8", "8.8.4.4"}, Enabled: true},
			want: []string{lan + " ip saddr 192.168.1.50 ip daddr { 8.8.8.8, 8.8.4.4 } udp dport { 53, 5353 } counter name "},
			deny: []string{"ip6 daddr"},
		},
		{
			name: "any protocol renders both families",
			rule: config.FirewallRule{ID: "c", Name: "c", Action: "deny", Direction: "forward", Protocol: "any", DstIPs: []string{"45.90.28.0/24", "2a07:a8c0::/64"}, Enabled: true},
			want: []string{lan + " ip daddr 45.90.28.0/24 counter name ", lan + " ip6 daddr 2a07:a8c0::/64 counter name "},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base.DeepCopy()
			cfg.Firewall.CustomRules = []config.FirewallRule{tc.rule}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			ruleset, err := GenerateNftables(&cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(ruleset, want+FirewallRuleCounter(tc.rule)+" drop") {
					t.Fatalf("missing %q in\n%s", want, ruleset)
				}
			}
			for _, deny := range tc.deny {
				if strings.Contains(ruleset, deny) {
					t.Fatalf("unexpected %q in\n%s", deny, ruleset)
				}
			}
		})
	}
}

// The generator writes destinations verbatim, and dropping a bad value would
// leave a deny that matches every destination. It must refuse instead, even
// when a caller skipped validation.
func TestGeneratorRefusesUnvalidatedDestinations(t *testing.T) {
	for _, rule := range []config.FirewallRule{
		{ID: "x", Name: "x", Action: "deny", Direction: "forward", Protocol: "tcp", DstPort: 443, DstIPs: []string{"1.1.1.1 } accept"}, Enabled: true},
		{ID: "y", Name: "y", Action: "deny", Direction: "forward", Protocol: "tcp", DstPort: 443, DstIPs: []string{"0.0.0.0/0"}, Enabled: true},
		{ID: "z", Name: "z", Action: "deny", Direction: "forward", Protocol: "tcp", DstPorts: []int{443, 70000}, Enabled: true},
	} {
		cfg := config.DefaultConfig()
		cfg.Firewall.CustomRules = []config.FirewallRule{rule}
		if out, err := GenerateNftables(&cfg); err == nil {
			t.Fatalf("rendered %+v:\n%s", rule, out)
		}
	}
}

// Adding optional fields must not rename the counters of existing rules: the
// dashboard pairs counters with rules by name across an update. The value is
// what origin/main produced before dst_ips/dst_ports existed.
func TestExistingRuleCounterIdentityIsUnchanged(t *testing.T) {
	rule := config.FirewallRule{ID: "preset-dns-udp", Name: "x", PresetID: "dns", Action: "deny", Direction: "forward", Protocol: "udp", DstPort: 53, Enabled: true, Scope: "wan"}
	if got := FirewallRuleCounter(rule); got != "mr_rule_1ab57d82cfff094f8c5426ab" {
		t.Fatalf("counter identity changed: %s", got)
	}
}

func TestFirewallPreviewDescribesDestinationRules(t *testing.T) {
	before := config.DefaultConfig()
	after := before.DeepCopy()
	after.Firewall.CustomRules = publicResolverRules(loadPublicResolverList(t))
	changes := strings.Join(DescribeFirewallChanges(before.Firewall, after.Firewall), "\n")
	count := strconv.Itoa(len(after.Firewall.CustomRules[0].DstIPs))
	for _, want := range []string{"Add Block public DNS resolvers by address (preset): deny forward tcp/443,853,5353,8853 from  to " + count + " destinations, scope wan", "udp/443,853,5353,8853"} {
		if !strings.Contains(changes, want) {
			t.Fatalf("missing %q in\n%s", want, changes)
		}
	}
}

// TestCustomRuleDestinationsLive loads the rendered resolver rules into a real
// nftables table. It needs nft and a disposable network namespace (see
// .github/workflows/deep-validation.yml); the rest of the generated ruleset
// references local users, so only the rule lines are loaded.
func TestCustomRuleDestinationsLive(t *testing.T) {
	if os.Getenv("MINIMALROUTER_NFT_FILTER_LAB") != "1" {
		t.Skip("requires nft and a disposable network namespace")
	}
	cfg, _ := publicResolverConfig(t)
	ruleset, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	var table strings.Builder
	table.WriteString("table inet mr_resolver_lab {\n")
	for _, rule := range cfg.Firewall.CustomRules {
		table.WriteString("  counter " + FirewallRuleCounter(rule) + " { }\n")
	}
	table.WriteString("  chain forward {\n    type filter hook forward priority filter; policy accept;\n")
	for _, line := range nftChains(ruleset)["forward"] {
		if strings.Contains(line, "daddr") && strings.Contains(line, "counter name mr_rule_") {
			table.WriteString("    " + line + "\n")
		}
	}
	table.WriteString("  }\n}\n")
	path := filepath.Join(t.TempDir(), "resolvers.nft")
	if err := os.WriteFile(path, []byte(table.String()), 0600); err != nil {
		t.Fatal(err)
	}
	defer exec.Command("nft", "delete", "table", "inet", "mr_resolver_lab").Run()
	if out, err := exec.Command("nft", "-f", path).CombinedOutput(); err != nil {
		t.Fatalf("nft rejected the rendered rules: %v\n%s\n%s", err, out, table.String())
	}
	out, err := exec.Command("nft", "list", "chain", "inet", "mr_resolver_lab", "forward").CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	for _, want := range []string{"1.1.1.1", "45.90.28.0/24", "2620:fe::fe", "2a07:a8c0::/64", "5353", "8853"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("loaded chain lacks %s:\n%s", want, out)
		}
	}
	t.Log(string(out))
}
