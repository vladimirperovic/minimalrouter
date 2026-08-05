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

func TestScenarioSafetyRejectsPublicWGClientRouteCapture(t *testing.T) {
	cfg := scenarioWGClientConfig()
	for _, route := range []string{"203.0.113.0/24", "8.8.8.0/24", "100.64.0.0/10"} {
		cfg.WGClient.AllowedIPs = []string{route}
		if err := cfg.ValidateScenarioSafety(); err == nil {
			t.Fatalf("site-to-site wg1 accepted non-RFC1918 route %s", route)
		}
	}
}

func TestScenarioSafetyAcceptsRFC1918SiteNetworks(t *testing.T) {
	cfg := scenarioWGClientConfig()
	cfg.WGClient.AllowedIPs = []string{"10.50.0.0/16", "172.20.1.0/24", "192.168.200.0/24"}
	if err := cfg.ValidateScenarioSafety(); err != nil {
		t.Fatalf("valid private site networks rejected: %v", err)
	}
}

func TestScenarioSafetyRejectsMalformedDNSLabels(t *testing.T) {
	for _, domain := range []string{"home..arpa", "-home.arpa", "home-.arpa", ".home.arpa", "home.arpa."} {
		cfg := DefaultConfig()
		cfg.System.Domain = domain
		if err := cfg.ValidateScenarioSafety(); err == nil {
			t.Fatalf("malformed system domain %q was accepted", domain)
		}
	}
	cfg := DefaultConfig()
	cfg.DNS.Records = []DNSRecord{{Name: "immich..home.arpa", IP: "192.168.1.20"}}
	if err := cfg.ValidateScenarioSafety(); err == nil {
		t.Fatal("malformed static DNS hostname was accepted")
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
