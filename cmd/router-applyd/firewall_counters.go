package main

import (
	"encoding/json"
	"fmt"
	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"os"
	"strings"
	"sync"
	"time"
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
		out, err := runCommandOutput(5*time.Second, "/usr/sbin/nft", "-a", "-j", "list", "table", "inet", "minimalrouter")
		if err != nil {
			return failure(req.ID, "Firewall counters unavailable", false)
		}
		boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
		if err != nil {
			return failure(req.ID, "Firewall counter generation unavailable", false)
		}
		value, err := parseFirewallCounters(out, strings.TrimSpace(string(boot)))
		if err != nil {
			return failure(req.ID, "Firewall counters not initialized", false)
		}
		firewallTelemetry.value = value
		firewallTelemetry.at = time.Now()
	}
	return apply.ApplyResponse{ID: req.ID, Success: true, Verified: true, Timestamp: time.Now().Unix(), FirewallCounters: firewallTelemetry.value}
}

func parseFirewallCounters(raw, boot string) (*apply.FirewallCounters, error) {
	var payload struct {
		Nftables []struct {
			Table *struct {
				Family string `json:"family"`
				Name   string `json:"name"`
				Handle uint64 `json:"handle"`
			} `json:"table"`
			Counter *struct {
				Family  string `json:"family"`
				Table   string `json:"table"`
				Name    string `json:"name"`
				Handle  uint64 `json:"handle"`
				Packets uint64 `json:"packets"`
			} `json:"counter"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, err
	}
	var tableHandle uint64
	counters := map[string]uint64{}
	handles := map[string]uint64{}
	for _, entry := range payload.Nftables {
		if t := entry.Table; t != nil && t.Family == "inet" && t.Name == "minimalrouter" {
			tableHandle = t.Handle
		}
		c := entry.Counter
		if c == nil || c.Family != "inet" || c.Table != "minimalrouter" || (c.Name != "mr_seen" && c.Name != "mr_accepted") {
			continue
		}
		if _, ok := counters[c.Name]; ok {
			return nil, fmt.Errorf("duplicate counter")
		}
		counters[c.Name] = c.Packets
		handles[c.Name] = c.Handle
	}
	if boot == "" || tableHandle == 0 || len(counters) != 2 || handles["mr_seen"] == 0 || handles["mr_accepted"] == 0 {
		return nil, fmt.Errorf("missing counters or generation")
	}
	return &apply.FirewallCounters{Seen: counters["mr_seen"], Accepted: counters["mr_accepted"], Generation: fmt.Sprintf("%s:%d:%d:%d", boot, tableHandle, handles["mr_seen"], handles["mr_accepted"])}, nil
}
