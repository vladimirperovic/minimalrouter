package main

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
)

var firewallTelemetry struct {
	sync.Mutex
	at    time.Time
	value *apply.FirewallCounters
}

// All arguments are fixed. Authenticated local callers cannot select a table,
// command, file, or request raw rule output through this read-only operation.
func firewallCounters(req apply.ApplyRequest) apply.ApplyResponse {
	firewallTelemetry.Lock()
	defer firewallTelemetry.Unlock()
	if time.Since(firewallTelemetry.at) < 15*time.Second && firewallTelemetry.value == nil {
		return failure(req.ID, "Firewall counters unavailable", false)
	}
	if firewallTelemetry.value == nil || time.Since(firewallTelemetry.at) > 15*time.Second {
		firewallTelemetry.at = time.Now()
		firewallTelemetry.value = nil
		// Terse output omits potentially large DNS destination sets.
		out, err := runCommandOutput(5*time.Second, "/usr/sbin/nft", "-a", "-j", "-t", "list", "table", "inet", "minimalrouter")
		if err != nil {
			return failure(req.ID, "Firewall counters unavailable", false)
		}
		boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
		if err != nil {
			return failure(req.ID, "Firewall counter generation unavailable", false)
		}
		value, err := apply.ParseFirewallCounters(out, strings.TrimSpace(string(boot)))
		if err != nil {
			return failure(req.ID, "Firewall counters not initialized", false)
		}
		firewallTelemetry.value = value
		firewallTelemetry.at = time.Now()
	}
	return apply.ApplyResponse{ID: req.ID, Success: true, Verified: true, Timestamp: time.Now().Unix(), FirewallCounters: firewallTelemetry.value}
}
