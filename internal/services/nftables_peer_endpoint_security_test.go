package services

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// A configured WireGuard peer endpoint is not a trusted WAN host. In
// particular it may be a shared-NAT address, so knowing that source IP must
// never bypass the default-deny forward policy. The only pre-authentication
// traffic it may receive special handling for is UDP to the WireGuard listen
// port itself.
func TestKnownWireGuardPeerEndpointIsNeverBroadlyTrusted(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "test"
	cfg.WAN.Password = "long-enough-test-password"
	cfg.WireGuard.Enabled = true
	cfg.WireGuard.PrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	cfg.WireGuard.Peers = []config.WireGuardPeer{{
		ID: "admin", Name: "Admin",
		PublicKey:  "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA=",
		AllowedIPs: []string{"10.8.0.2/32"},
		Endpoint:   "10.99.0.42:51820",
		Enabled:    true,
	}}

	rules, err := GenerateNftables(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	forwardStart := strings.Index(rules, "chain forward {")
	outputStart := strings.Index(rules, "chain output {")
	if forwardStart < 0 || outputStart <= forwardStart {
		t.Fatal("could not isolate forward chain")
	}
	forward := rules[forwardStart:outputStart]
	if strings.Contains(forward, "10.99.0.42/32") {
		t.Fatalf("WireGuard peer endpoint leaked into the forward trust policy:\n%s", forward)
	}

	foundScopedWGAccept := false
	for _, line := range strings.Split(rules, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "10.99.0.42/32") || !strings.Contains(line, " accept") {
			continue
		}
		if !strings.Contains(line, "udp dport 51820") {
			t.Fatalf("peer endpoint received a broad WAN accept: %s", line)
		}
		foundScopedWGAccept = true
	}
	if !foundScopedWGAccept {
		t.Fatal("known peer endpoint lost its narrowly scoped WireGuard UDP allowance")
	}
}
