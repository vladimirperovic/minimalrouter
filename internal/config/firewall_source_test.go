package config

import (
	"strings"
	"testing"
)

// The dashboard rule editor offers "an IPv4 address or CIDR" and the nftables
// generator emits either as a valid `ip saddr` match, so validation must accept
// both. It previously took only a bare address, which made the editor promise a
// value the appliance then refused on every save.
func TestCustomRuleSourceAcceptsAddressAndCIDR(t *testing.T) {
	for _, source := range []string{"192.168.1.50", "192.168.1.0/24", "192.168.1.64/26"} {
		cfg := DefaultConfig()
		cfg.Firewall.CustomRules = []FirewallRule{{
			Name: "Guest printer", Enabled: true, Action: "deny",
			Direction: "forward", Protocol: "tcp", SrcIP: source, DstPort: 9100,
		}}
		if err := cfg.Validate(); err != nil {
			t.Errorf("source %q must be accepted: %v", source, err)
		}
	}
}

func TestCustomRuleSourceStaysInsideTheLAN(t *testing.T) {
	for _, source := range []string{"10.9.9.9", "10.9.0.0/16", "not-an-address", "192.168.1.0/33"} {
		cfg := DefaultConfig()
		cfg.Firewall.CustomRules = []FirewallRule{{
			Name: "Outside", Enabled: true, Action: "deny",
			Direction: "forward", Protocol: "tcp", SrcIP: source, DstPort: 9100,
		}}
		err := cfg.Validate()
		if err == nil {
			t.Errorf("source %q must be rejected", source)
			continue
		}
		if !strings.Contains(err.Error(), "src_ip") {
			t.Errorf("source %q was rejected for the wrong reason: %v", source, err)
		}
	}
}
