package config

import (
	"testing"
)

func TestValidationDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	err := cfg.Validate()
	if err != nil {
		t.Fatalf("DefaultConfig should be valid, got: %v", err)
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
