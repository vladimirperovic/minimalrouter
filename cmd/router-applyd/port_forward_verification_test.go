package main

import "testing"

func TestPortForwardRulesActiveRequiresBothConcreteProtocols(t *testing.T) {
	ruleset := `table inet minimalrouter {
	chain prerouting {
		iifname "wg0" tcp dport 8443 dnat to 192.168.1.50:443
		iifname "wg0" udp dport 8443 dnat to 192.168.1.50:443
	}
}`
	if !portForwardRulesActive(ruleset, "wg0", "both", 8443, "192.168.1.50", 443) {
		t.Fatal("valid TCP+UDP tunnel port forward was rejected")
	}
}

func TestPortForwardRulesActiveRejectsPartialBothRule(t *testing.T) {
	ruleset := `iifname "wg0" tcp dport 8443 dnat to 192.168.1.50:443`
	if portForwardRulesActive(ruleset, "wg0", "both", 8443, "192.168.1.50", 443) {
		t.Fatal("protocol=both was accepted without the UDP rule")
	}
}

func TestPortForwardRulesActiveAcceptsSingleProtocol(t *testing.T) {
	ruleset := `iifname "wg7" udp dport 5353 dnat to 192.168.1.60:5353`
	if !portForwardRulesActive(ruleset, "wg7", "udp", 5353, "192.168.1.60", 5353) {
		t.Fatal("valid UDP tunnel port forward was rejected")
	}
}

func TestPortForwardRulesActiveRejectsUnknownProtocol(t *testing.T) {
	ruleset := `iifname "wg0" sctp dport 8443 dnat to 192.168.1.50:443`
	if portForwardRulesActive(ruleset, "wg0", "sctp", 8443, "192.168.1.50", 443) {
		t.Fatal("unknown port-forward protocol was accepted")
	}
}
