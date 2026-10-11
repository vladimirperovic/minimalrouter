package services

import (
	"fmt"
	"strings"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// DescribeNetworkChanges contains operator-facing deltas without credentials.
func DescribeNetworkChanges(current, next config.SystemConfig) []string {
	changes := []string{}
	field := func(name string, a, b any) {
		if fmt.Sprint(a) != fmt.Sprint(b) {
			changes = append(changes, fmt.Sprintf("%s: %v → %v", name, a, b))
		}
	}
	field("LAN gateway", current.LAN.IPAddress, next.LAN.IPAddress)
	field("LAN interface", current.LAN.Interface, next.LAN.Interface)
	field("DHCP enabled", current.DHCP.Enabled, next.DHCP.Enabled)
	field("Pool start", current.DHCP.RangeStart, next.DHCP.RangeStart)
	field("Pool end", current.DHCP.RangeEnd, next.DHCP.RangeEnd)
	field("Lease duration", current.DHCP.LeaseTime, next.DHCP.LeaseTime)
	field("Upstream DNS", strings.Join(current.DHCP.DNSServers, ", "), strings.Join(next.DHCP.DNSServers, ", "))
	field("WAN enabled", current.WAN.Enabled, next.WAN.Enabled)
	field("WAN interface", current.WAN.Interface, next.WAN.Interface)
	field("WAN MTU", current.WAN.MTU, next.WAN.MTU)
	if current.WAN.Username != next.WAN.Username {
		changes = append(changes, "PPPoE username: changed")
	}
	if current.WAN.Password != next.WAN.Password {
		changes = append(changes, "PPPoE password: changed (hidden)")
	}
	oldLeases := map[string]config.StaticLease{}
	for _, lease := range current.DHCP.StaticLeases {
		oldLeases[lease.ID] = lease
	}
	for _, lease := range next.DHCP.StaticLeases {
		before, exists := oldLeases[lease.ID]
		if !exists {
			changes = append(changes, fmt.Sprintf("Reservation added: %s · %s → %s", lease.Hostname, lease.MAC, lease.IPAddress))
		} else if before != lease {
			changes = append(changes, fmt.Sprintf("Reservation changed: %s / %s / %s → %s / %s / %s", before.Hostname, before.MAC, before.IPAddress, lease.Hostname, lease.MAC, lease.IPAddress))
		}
		delete(oldLeases, lease.ID)
	}
	for _, lease := range current.DHCP.StaticLeases {
		if _, exists := oldLeases[lease.ID]; exists {
			changes = append(changes, "Reservation removed: "+lease.Hostname+" · "+lease.IPAddress)
		}
	}
	oldRecords := map[string]string{}
	for _, record := range current.DNS.Records {
		oldRecords[record.Name] = record.IP
	}
	for _, record := range next.DNS.Records {
		before, exists := oldRecords[record.Name]
		if !exists {
			changes = append(changes, "Local DNS added: "+record.Name+" → "+record.IP)
		} else {
			field("Local DNS "+record.Name, before, record.IP)
		}
		delete(oldRecords, record.Name)
	}
	for _, record := range current.DNS.Records {
		if _, exists := oldRecords[record.Name]; exists {
			changes = append(changes, "Local DNS removed: "+record.Name+" → "+record.IP)
		}
	}
	if len(changes) > 50 {
		return append(changes[:50], fmt.Sprintf("%d additional network changes; review the full candidate before applying.", len(changes)-50))
	}
	return changes
}
