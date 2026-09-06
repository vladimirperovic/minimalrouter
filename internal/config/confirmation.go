package config

import (
	"bytes"
	"encoding/json"
)

// EqualSection compares the persisted/API representation of configuration
// sections. In particular, nil and empty omitempty collections represent the
// same setting after a JSON round trip, and must not invent a runtime change.
func EqualSection(a, b any) bool {
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	return leftErr == nil && rightErr == nil && bytes.Equal(left, right)
}

// RequiresConfirmation classifies changes that can disrupt operator access.
// It is a pure policy shared by preview, management, and the privileged helper;
// callers must still independently validate the candidate and transition.
func RequiresConfirmation(current, candidate SystemConfig) bool {
	wireGuardManagementChanged := (current.System.ManagementAccess == "wireguard_only" || candidate.System.ManagementAccess == "wireguard_only") && !EqualSection(current.WireGuard, candidate.WireGuard)
	wifiChanged := !EqualSection(current.WiFi, candidate.WiFi) && (current.WiFi.Enabled || candidate.WiFi.Enabled)
	return current.LAN.IPAddress != candidate.LAN.IPAddress || current.LAN.CIDR != candidate.LAN.CIDR ||
		current.System.ManagementAccess != candidate.System.ManagementAccess || wifiChanged || wireGuardManagementChanged ||
		!EqualSection(current.WGClient, candidate.WGClient) || !EqualSection(current.TrustedNetworks, candidate.TrustedNetworks)
}
