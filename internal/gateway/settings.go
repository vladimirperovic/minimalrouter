package gateway

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// Settings controls bounded, read-only WAN quality checks. It is deliberately
// stored outside the canonical router configuration because it cannot change
// packet forwarding or privileged system state.
type Settings struct {
	Enabled         bool     `json:"enabled"`
	Targets         []string `json:"targets"`
	IntervalSeconds int      `json:"interval_seconds"`
}

func DefaultSettings() Settings {
	return Settings{Enabled: true, Targets: []string{"1.1.1.1", "8.8.8.8"}, IntervalSeconds: 30}
}

func (s Settings) Interval() time.Duration {
	if s.IntervalSeconds < 15 || s.IntervalSeconds > 300 {
		return 30 * time.Second
	}
	return time.Duration(s.IntervalSeconds) * time.Second
}

func (s Settings) Validate() error {
	if len(s.Targets) != 2 {
		return fmt.Errorf("targets must contain exactly two public IPv4 addresses")
	}
	seen := make(map[string]struct{}, len(s.Targets))
	for index, target := range s.Targets {
		trimmed := strings.TrimSpace(target)
		ip := net.ParseIP(trimmed)
		if target != trimmed || ip == nil || ip.To4() == nil || strings.Contains(trimmed, ":") {
			return fmt.Errorf("target %d must be a normalized dotted-quad IPv4 address", index+1)
		}
		ip = ip.To4()
		if !publicMonitoringIPv4(ip) {
			return fmt.Errorf("target %d must be a public unicast IPv4 address", index+1)
		}
		key := ip.String()
		if _, exists := seen[key]; exists {
			return fmt.Errorf("monitoring targets must be different")
		}
		seen[key] = struct{}{}
	}
	if s.IntervalSeconds < 15 || s.IntervalSeconds > 300 {
		return fmt.Errorf("interval_seconds must be between 15 and 300")
	}
	return nil
}

func publicMonitoringIPv4(ip net.IP) bool {
	ip = ip.To4()
	if ip == nil {
		return false
	}
	reserved := []struct {
		network net.IP
		mask    net.IPMask
	}{
		{net.IPv4(0, 0, 0, 0), net.CIDRMask(8, 32)},
		{net.IPv4(10, 0, 0, 0), net.CIDRMask(8, 32)},
		{net.IPv4(100, 64, 0, 0), net.CIDRMask(10, 32)},
		{net.IPv4(127, 0, 0, 0), net.CIDRMask(8, 32)},
		{net.IPv4(169, 254, 0, 0), net.CIDRMask(16, 32)},
		{net.IPv4(172, 16, 0, 0), net.CIDRMask(12, 32)},
		{net.IPv4(192, 0, 0, 0), net.CIDRMask(24, 32)},
		{net.IPv4(192, 0, 2, 0), net.CIDRMask(24, 32)},
		{net.IPv4(192, 168, 0, 0), net.CIDRMask(16, 32)},
		{net.IPv4(198, 18, 0, 0), net.CIDRMask(15, 32)},
		{net.IPv4(198, 51, 100, 0), net.CIDRMask(24, 32)},
		{net.IPv4(203, 0, 113, 0), net.CIDRMask(24, 32)},
		{net.IPv4(224, 0, 0, 0), net.CIDRMask(4, 32)},
		{net.IPv4(240, 0, 0, 0), net.CIDRMask(4, 32)},
	}
	for _, block := range reserved {
		if (&net.IPNet{IP: block.network, Mask: block.mask}).Contains(ip) {
			return false
		}
	}
	return true
}
