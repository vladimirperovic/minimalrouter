package main

import (
	"strings"
	"testing"
)

func TestEmergencyFirewallDropsInputAndForwarding(t *testing.T) {
	for _, required := range []string{
		"type filter hook input priority filter; policy drop;",
		"type filter hook forward priority filter; policy drop;",
		"type filter hook output priority filter; policy accept;",
		"iifname \"lo\" accept",
		"ct state established,related accept",
	} {
		if !strings.Contains(emergencyNftables, required) {
			t.Fatalf("emergency firewall is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"tcp dport 8443 accept",
		"udp dport 51820 accept",
		"iifname \"ppp0\" accept",
		"policy accept;\n    }\n    chain forward",
	} {
		if strings.Contains(emergencyNftables, forbidden) {
			t.Fatalf("emergency firewall unexpectedly exposes remote ingress via %q", forbidden)
		}
	}
}
