package services

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// nft parses `icmp` as the start of a header expression and expects a field
// after it, so a bare keyword is a syntax error the ruleset only fails on at
// preflight. Validation accepts protocol "icmp", so the generator has to emit
// the network-layer qualifier it already uses everywhere else.
func TestCustomICMPRuleIsEmittedWithItsProtocolQualifier(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Firewall.CustomRules = []config.FirewallRule{{
		Name: "Block ping out", Enabled: true, Action: "deny",
		Direction: "forward", Protocol: "icmp",
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("an ICMP custom rule must be valid: %v", err)
	}

	rules, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, line := range strings.Split(rules, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "icmp") || !strings.HasSuffix(line, " drop") {
			continue
		}
		if !strings.HasPrefix(line, `iifname "`) {
			continue
		}
		found = true
		if !strings.Contains(line, "ip protocol icmp") {
			t.Errorf("bare protocol keyword is not valid nft syntax: %s", line)
		}
	}
	if !found {
		t.Fatal("the ICMP custom rule was not emitted at all")
	}
}

// TCP and UDP carry a port, which already satisfies nft's grammar; the
// qualifier change must not disturb them.
func TestCustomPortRulesKeepTheirExistingShape(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Firewall.CustomRules = []config.FirewallRule{{
		Name: "Block SMB out", Enabled: true, Action: "deny",
		Direction: "forward", Protocol: "tcp", DstPort: 445,
	}}
	rules, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rules, "tcp dport 445 drop") {
		t.Fatal("the TCP custom rule lost its expected shape")
	}
}
