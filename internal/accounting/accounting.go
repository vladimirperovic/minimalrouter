// Package accounting turns the per-host nftables byte counters into bounded
// monthly totals per LAN device.
//
// Design constraints that shaped this package:
//
//   - Only aggregate byte counts per LAN address are stored. No ports, no
//     destinations, no hostnames beyond what DHCP already knows, no payload.
//     Enabling accounting must not turn the router into a household traffic log.
//   - The `inet minimalrouter` table is deleted and recreated on every apply, so
//     the kernel counters restart at zero regularly. Any decrease is treated as
//     a reset, never as negative traffic.
//   - routerd is unprivileged. It reads the counters through two exact
//     doas-allowlisted `nft -j list set` argument vectors and nothing else.
package accounting

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"
)

// Counter is one host's cumulative byte count as currently held by the kernel.
type Counter struct {
	Address string
	Bytes   uint64
}

// DeviceUsage is the API-facing monthly total for one device.
type DeviceUsage struct {
	Address     string `json:"address"`
	Hostname    string `json:"hostname,omitempty"`
	MAC         string `json:"mac,omitempty"`
	RXBytes     uint64 `json:"rx_bytes"`
	TXBytes     uint64 `json:"tx_bytes"`
	TotalBytes  uint64 `json:"total_bytes"`
	LastSeenUTC int64  `json:"last_seen_epoch,omitempty"`
}

// MonthUsage is one calendar month of accounting, newest first in API output.
type MonthUsage struct {
	Month      string        `json:"month"` // YYYY-MM
	TotalBytes uint64        `json:"total_bytes"`
	Devices    []DeviceUsage `json:"devices"`
}

// Snapshot is the full accounting response.
type Snapshot struct {
	Available bool         `json:"available"`
	Enabled   bool         `json:"enabled"`
	Months    []MonthUsage `json:"months"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// nftSetDocument mirrors just enough of `nft -j list set` to read element
// counters. Unknown fields are ignored so an nft version bump that adds keys
// cannot break accounting.
type nftSetDocument struct {
	Nftables []struct {
		Set *struct {
			Name  string            `json:"name"`
			Table string            `json:"table"`
			Elem  []json.RawMessage `json:"elem"`
		} `json:"set"`
	} `json:"nftables"`
}

type nftElement struct {
	Elem *struct {
		Val     json.RawMessage `json:"val"`
		Counter *struct {
			Bytes uint64 `json:"bytes"`
		} `json:"counter"`
	} `json:"elem"`
}

// ParseCounters reads the JSON produced by `nft -j list set inet minimalrouter <name>`.
//
// A set that does not exist yet (accounting just enabled, table not yet
// reloaded) is not an error: it simply yields no counters.
func ParseCounters(raw string) ([]Counter, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	var document nftSetDocument
	if err := json.Unmarshal([]byte(trimmed), &document); err != nil {
		return nil, fmt.Errorf("decode nft set output: %w", err)
	}
	var counters []Counter
	for _, entry := range document.Nftables {
		if entry.Set == nil {
			continue
		}
		for _, rawElement := range entry.Set.Elem {
			address, bytes, ok := parseElement(rawElement)
			if !ok {
				continue
			}
			counters = append(counters, Counter{Address: address, Bytes: bytes})
		}
	}
	return counters, nil
}

// parseElement tolerates both element shapes nft emits: a bare address string
// for a plain set, and the {"elem": {...}} wrapper used once the element
// carries a counter or timeout.
func parseElement(raw json.RawMessage) (string, uint64, bool) {
	var wrapper nftElement
	if err := json.Unmarshal(raw, &wrapper); err == nil && wrapper.Elem != nil {
		address, ok := decodeAddress(wrapper.Elem.Val)
		if !ok {
			return "", 0, false
		}
		if wrapper.Elem.Counter == nil {
			return address, 0, true
		}
		return address, wrapper.Elem.Counter.Bytes, true
	}
	address, ok := decodeAddress(raw)
	if !ok {
		return "", 0, false
	}
	return address, 0, true
}

func decodeAddress(raw json.RawMessage) (string, bool) {
	var address string
	if err := json.Unmarshal(raw, &address); err != nil {
		return "", false
	}
	address = strings.TrimSpace(address)
	if net.ParseIP(address) == nil {
		return "", false
	}
	return address, true
}

// Delta returns how many bytes to add for a host given the previously observed
// raw counter.
//
// The kernel counter is monotonic only between table reloads. Every apply
// deletes and recreates `inet minimalrouter`, so a value lower than the last
// observation means the counter restarted and the current value is itself the
// delta. Treating that as zero would silently lose a month of traffic on a busy
// router that gets configured often.
func Delta(previous, current uint64) uint64 {
	if current >= previous {
		return current - previous
	}
	return current
}

// MonthKey is the bucket identifier: calendar month in UTC.
func MonthKey(at time.Time) string {
	return at.UTC().Format("2006-01")
}
