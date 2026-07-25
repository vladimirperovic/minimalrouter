package config

import (
	"strings"
	"testing"
)

func TestValidationDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	err := cfg.Validate()
	if err != nil {
		t.Fatalf("DefaultConfig should be valid, got: %v", err)
	}
}

func TestValidationRejectsGeneratedConfigInjection(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*SystemConfig)
	}{
		{"interface newline", func(cfg *SystemConfig) { cfg.WAN.Interface = "eth0\nflush ruleset" }},
		{"hostname directive", func(cfg *SystemConfig) { cfg.System.Hostname = "router\nserver=evil" }},
		{"PPPoE quote", func(cfg *SystemConfig) {
			cfg.WAN.Enabled = true
			cfg.WAN.Username = "user\"\nnoauth"
			cfg.WAN.Password = strings.Repeat("x", 15)
		}},
		{"rule name newline", func(cfg *SystemConfig) {
			cfg.Firewall.CustomRules = []FirewallRule{{
				Name: "allow\naccept", Action: "allow", Direction: "forward",
				Protocol: "tcp", DstPort: 80, Enabled: true,
			}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected unsafe generated-config input to be rejected")
			}
		})
	}
}

func TestValidationInterfaceBoundaryCollision(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WAN.Interface = "eth0"
	cfg.LAN.Interface = "eth0"

	err := cfg.Validate()
	if err == nil {
		t.Fatalf("Expected error when WAN and LAN interfaces are identical, got nil")
	}
}

func TestValidationInvalidIP(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LAN.IPAddress = "999.999.999.999"

	err := cfg.Validate()
	if err == nil {
		t.Fatalf("Expected error for invalid LAN IP address, got nil")
	}
}

func TestValidationPortForwardRange(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Firewall.PortForwards = []PortForwardRule{
		{
			ID:           "pf1",
			Name:         "Invalid Port Test",
			Protocol:     "tcp",
			ExternalPort: 70000,
			InternalIP:   "192.168.1.50",
			InternalPort: 80,
			Enabled:      true,
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatalf("Expected error for external port > 65535, got nil")
	}
}

func TestValidationRejectsEveryEnabledWANPortForward(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "isp-user"
	cfg.WAN.Password = "isp-password"
	cfg.Firewall.PortForwards = []PortForwardRule{{
		ID: "pf1", Name: "Forbidden Web Server", Protocol: "tcp",
		ExternalPort: 443, InternalIP: "192.168.1.50", InternalPort: 443, Enabled: true,
	}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "WireGuard is the only allowed external entry point") {
		t.Fatalf("expected WireGuard-only WAN ingress rejection, got %v", err)
	}
}
