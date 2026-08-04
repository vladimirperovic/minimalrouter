package main

import (
	"encoding/base64"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func helperWireGuardConfig() config.WireGuardConfig {
	privateKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	peerKeyBytes := make([]byte, 32)
	peerKeyBytes[0] = 1
	return config.WireGuardConfig{
		Enabled:    true,
		Interface:  "wg0",
		PrivateKey: privateKey,
		ListenPort: 51820,
		Address:    "10.8.0.1/24",
		Peers: []config.WireGuardPeer{{
			ID:         "phone",
			Name:       "Phone",
			PublicKey:  base64.StdEncoding.EncodeToString(peerKeyBytes),
			AllowedIPs: []string{"10.8.0.2/32"},
			Enabled:    true,
		}},
	}
}

func TestConfirmationModeAllowsWireGuardOnlyControlPlaneChange(t *testing.T) {
	previous := config.DefaultConfig()
	previous.System.ManagementAccess = "wireguard_only"
	previous.WireGuard = helperWireGuardConfig()
	candidate := previous
	candidate.WireGuard.ListenPort++

	if !confirmationModeAllowed(&previous, candidate) {
		t.Fatal("privileged helper must accept provisional WireGuard-only management changes")
	}
}

func TestConfirmationModeRejectsOrdinaryWireGuardMaintenanceOnLAN(t *testing.T) {
	previous := config.DefaultConfig()
	previous.WireGuard = helperWireGuardConfig()
	candidate := previous
	candidate.WireGuard.ListenPort++

	if confirmationModeAllowed(&previous, candidate) {
		t.Fatal("LAN-managed WireGuard maintenance must not enter confirmation mode")
	}
}

func TestConfirmationModeRejectsUnknownPreviousState(t *testing.T) {
	if confirmationModeAllowed(nil, config.DefaultConfig()) {
		t.Fatal("confirmation mode must fail closed without a known previous state")
	}
}

func TestConfirmationModeRejectsLANInterfaceReplacement(t *testing.T) {
	previous := config.DefaultConfig()
	candidate := previous
	candidate.LAN.Interface = "eth2"
	candidate.LAN.IPAddress = "10.25.0.1"
	candidate.LAN.CIDR = "10.25.0.1/24"

	if confirmationModeAllowed(&previous, candidate) {
		t.Fatal("provisional mode cannot replace the rollback LAN interface")
	}
}

func TestConfirmationModeAllowsWGClientTunnelChange(t *testing.T) {
	// The outbound tunnel (wg1) is confirmation-required (mirrored in the
	// engine's requiresConfirmation) and does not touch the management path:
	// a provisional apply must accept it so the 90-second confirm window runs.
	previous := config.DefaultConfig()
	candidate := previous
	candidate.WGClient.Enabled = true
	candidate.WGClient.PrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	candidate.WGClient.PublicKey = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA="
	candidate.WGClient.Address = "10.7.0.2/32"
	candidate.WGClient.Endpoint = "office.example.com:51820"
	candidate.WGClient.AllowedIPs = []string{"10.9.0.0/24"}

	if !confirmationModeAllowed(&previous, candidate) {
		t.Fatal("confirmation mode must accept outbound tunnel changes")
	}
}
