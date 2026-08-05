package config

import "testing"

const scenarioWGKeyA = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
const scenarioWGKeyB = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA="

func scenarioWGClientConfig() SystemConfig {
	cfg := DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "test-user"
	cfg.WAN.Password = "test-password-long-enough"
	cfg.WGClient.Enabled = true
	cfg.WGClient.Interface = "wg1"
	cfg.WGClient.PrivateKey = scenarioWGKeyA
	cfg.WGClient.PublicKey = scenarioWGKeyB
	cfg.WGClient.Endpoint = "office.example.com:51820"
	cfg.WGClient.Address = "10.7.0.2/32"
	cfg.WGClient.AllowedIPs = []string{"10.9.0.0/24"}
	return cfg
}

func TestScenarioSafetyRequiresWGClientAddress(t *testing.T) {
	cfg := scenarioWGClientConfig()
	cfg.WGClient.Address = ""
	if err := cfg.ValidateScenarioSafety(); err == nil {
		t.Fatal("enabled wg1 without a local /32 address passed scenario safety")
	}
}

func TestScenarioSafetyAcceptsDeterministicWGClientAddress(t *testing.T) {
	cfg := scenarioWGClientConfig()
	if err := cfg.ValidateScenarioSafety(); err != nil {
		t.Fatalf("valid wg1 scenario rejected: %v", err)
	}
}

func TestScenarioSafetyRejectsCustomInputDeny(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Firewall.CustomRules = []FirewallRule{{
		ID: "kill-admin", Name: "Block dashboard", Action: "deny", Direction: "input",
		Protocol: "tcp", DstPort: cfg.System.HTTPSPort, Enabled: true,
	}}
	if err := cfg.ValidateScenarioSafety(); err == nil {
		t.Fatal("custom input deny capable of severing management was accepted")
	}
}

func TestScenarioSafetyKeepsForwardPolicyAvailable(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Firewall.CustomRules = []FirewallRule{{
		ID: "block-egress", Name: "Block device", Action: "deny", Direction: "forward",
		Protocol: "tcp", SrcIP: "192.168.1.50", DstPort: 443, Enabled: true,
	}}
	if err := cfg.ValidateScenarioSafety(); err != nil {
		t.Fatalf("safe forward policy was rejected: %v", err)
	}
}
