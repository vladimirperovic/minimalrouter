package apply

import (
	"fmt"
	"strings"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/telemetry"
)

func inspectLANChange(current, candidate config.SystemConfig) ([]string, error) {
	if current.LAN.IPAddress == candidate.LAN.IPAddress && config.EqualSection(current.DHCP.StaticLeases, candidate.DHCP.StaticLeases) {
		return nil, nil
	}
	return inspectLANObservation(current, candidate, telemetry.CurrentDHCPLeaseSnapshot())
}

// Observations can reject a known collision, but can never prove an IP is free.
// This shared guard covers API, MCP and restores at the transaction boundary.
func inspectLANObservation(current, candidate config.SystemConfig, snapshot telemetry.DHCPLeaseSnapshot) ([]string, error) {
	previousByID := map[string]config.StaticLease{}
	for _, reservation := range current.DHCP.StaticLeases {
		previousByID[reservation.ID] = reservation
	}
	changedByIP := map[string]string{}
	for _, reservation := range candidate.DHCP.StaticLeases {
		previous, exists := previousByID[reservation.ID]
		if !exists || previous.IPAddress != reservation.IPAddress || !strings.EqualFold(previous.MAC, reservation.MAC) {
			changedByIP[reservation.IPAddress] = reservation.MAC
		}
	}
	for _, lease := range snapshot.Leases {
		if candidate.LAN.IPAddress != current.LAN.IPAddress && candidate.LAN.IPAddress == lease.IPAddress {
			return nil, fmt.Errorf("new LAN gateway is currently leased to %s", lease.MAC)
		}
		if mac, changed := changedByIP[lease.IPAddress]; changed && !strings.EqualFold(mac, lease.MAC) {
			return nil, fmt.Errorf("reserved address %s is currently leased to %s", lease.IPAddress, lease.MAC)
		}
	}
	if !snapshot.Available || snapshot.Truncated || snapshot.RejectedEntries > 0 {
		return []string{"DHCP observation is unavailable or incomplete; check for address conflicts before applying."}, nil
	}
	return []string{"Only known DHCP collisions were checked. Manually configured devices may not be observed."}, nil
}
