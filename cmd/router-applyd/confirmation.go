package main

import (
	"reflect"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// confirmationModeAllowed limits provisional configuration to changes that can
// affect the administrator's path back to the appliance. The previous LAN
// interface must remain present so rollback always has a known local path.
func confirmationModeAllowed(previous *config.SystemConfig, candidate config.SystemConfig) bool {
	if previous == nil || previous.LAN.Interface != candidate.LAN.Interface {
		return false
	}
	lanChanged := previous.LAN.IPAddress != candidate.LAN.IPAddress ||
		previous.LAN.CIDR != candidate.LAN.CIDR
	managementChanged := previous.System.ManagementAccess != candidate.System.ManagementAccess
	topologyChanged := previous.WiFi.Enabled != candidate.WiFi.Enabled ||
		previous.WiFi.Interface != candidate.WiFi.Interface
	wireGuardManagementChanged :=
		(previous.System.ManagementAccess == "wireguard_only" || candidate.System.ManagementAccess == "wireguard_only") &&
		!reflect.DeepEqual(previous.WireGuard, candidate.WireGuard)
	return lanChanged || managementChanged || topologyChanged || wireGuardManagementChanged
}
