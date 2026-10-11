package apply

import (
	"fmt"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/network"
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
	currentGateway := network.IPv4Identity(current.LAN.IPAddress)
	candidateGateway := network.IPv4Identity(candidate.LAN.IPAddress)
	previousByID := map[string]config.StaticLease{}
	for _, reservation := range current.DHCP.StaticLeases {
		previousByID[reservation.ID] = reservation
	}
	changedByIP := map[string]string{}
	for _, reservation := range candidate.DHCP.StaticLeases {
		previous, exists := previousByID[reservation.ID]
		ip, mac := network.IPv4Identity(reservation.IPAddress), network.MACIdentity(reservation.MAC)
		if !exists || network.IPv4Identity(previous.IPAddress) != ip || network.MACIdentity(previous.MAC) != mac {
			changedByIP[ip] = mac
		}
	}
	for _, lease := range snapshot.Leases {
		leaseIP := network.IPv4Identity(lease.IPAddress)
		if candidateGateway != currentGateway && candidateGateway == leaseIP {
			return nil, fmt.Errorf("new LAN gateway is currently leased to %s", lease.MAC)
		}
		if mac, changed := changedByIP[leaseIP]; changed && mac != network.MACIdentity(lease.MAC) {
			return nil, fmt.Errorf("reserved address %s is currently leased to %s", lease.IPAddress, lease.MAC)
		}
	}
	if !snapshot.Available || snapshot.Truncated || snapshot.RejectedEntries > 0 {
		return []string{"DHCP observation is unavailable or incomplete; check for address conflicts before applying."}, nil
	}
	return []string{"Only known DHCP collisions were checked. Manually configured devices may not be observed."}, nil
}
