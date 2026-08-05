package apply

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func migratedLANCandidate(current config.SystemConfig) config.SystemConfig {
	candidate := current.DeepCopy()
	candidate.LAN.IPAddress = "10.44.0.1"
	candidate.LAN.CIDR = "10.44.0.1/24"
	candidate.LAN.Netmask = "255.255.255.0"
	candidate.DHCP.RangeStart = "10.44.0.100"
	candidate.DHCP.RangeEnd = "10.44.0.200"
	return candidate
}

func TestLANSubnetMigrationRequiresPreTrustedNewSubnet(t *testing.T) {
	current := config.DefaultConfig()
	candidate := migratedLANCandidate(current)
	candidate.TrustedNetworks = append(candidate.TrustedNetworks, "10.44.0.0/24")

	err := validateTransitionSafety(current, candidate)
	if err == nil || !strings.Contains(err.Error(), "separate confirmed transaction") {
		t.Fatalf("direct cross-subnet migration was not rejected with two-step guidance: %v", err)
	}
}

func TestLANSubnetMigrationAcceptedAfterTrustPreparation(t *testing.T) {
	current := config.DefaultConfig()
	current.TrustedNetworks = append(current.TrustedNetworks, "10.44.0.0/24")
	candidate := migratedLANCandidate(current)
	if err := validateTransitionSafety(current, candidate); err != nil {
		t.Fatalf("prepared LAN migration was rejected: %v", err)
	}
}

func TestLANSubnetMigrationMustRetainNewTrustAfterCommit(t *testing.T) {
	current := config.DefaultConfig()
	current.TrustedNetworks = append(current.TrustedNetworks, "10.44.0.0/24")
	candidate := migratedLANCandidate(current)
	candidate.TrustedNetworks = []string{"192.168.1.0/24"}
	if err := validateTransitionSafety(current, candidate); err == nil {
		t.Fatal("candidate that drops management trust for its new LAN subnet was accepted")
	}
}

func TestGatewayAddressMoveWithinSameSubnetDoesNotNeedTwoStepTrust(t *testing.T) {
	current := config.DefaultConfig()
	candidate := current.DeepCopy()
	candidate.LAN.IPAddress = "192.168.1.254"
	candidate.LAN.CIDR = "192.168.1.254/24"
	if err := validateTransitionSafety(current, candidate); err != nil {
		t.Fatalf("same-subnet gateway address change was treated as cross-subnet migration: %v", err)
	}
}
