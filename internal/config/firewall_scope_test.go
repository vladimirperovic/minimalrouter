package config

import "testing"

func TestFirewallScopeAndWholeSourceSubnetValidation(t *testing.T) {
	for _, tc := range []struct {
		scope, src, preset, direction string
		valid                         bool
	}{
		{"wan", "192.168.1.64/26", "dns", "forward", true},
		{"", "192.168.1.1", "", "forward", true},
		{"all", "192.168.1.0/24", "", "forward", true},
		{"all", "192.168.0.0/16", "", "forward", false},
		{"all", "192.168.1.0/16", "", "forward", false},
		{"private", "", "", "forward", false},
		{"wan", "", "", "input", false},
		{"wan", "", "bad ID", "forward", false},
	} {
		cfg := DefaultConfig()
		cfg.Firewall.CustomRules = []FirewallRule{{ID: "one", Name: "Example", Action: "allow", Direction: tc.direction, Protocol: "tcp", DstPort: 53, Enabled: true, Scope: tc.scope, SrcIP: tc.src, PresetID: tc.preset}}
		err := cfg.Validate()
		if (err == nil) != tc.valid {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}
