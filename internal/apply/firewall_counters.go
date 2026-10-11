package apply

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

var ruleCounterName = regexp.MustCompile(`^mr_rule_[a-f0-9]{24}$`)

// ParseFirewallCounters projects owned counters and base-chain policy metadata.
// Raw rules and set elements are never returned to the management process.
func ParseFirewallCounters(raw, boot string) (*FirewallCounters, error) {
	var payload struct {
		Nftables []struct {
			Chain *struct {
				Family   string `json:"family"`
				Table    string `json:"table"`
				Name     string `json:"name"`
				Hook     string `json:"hook"`
				Type     string `json:"type"`
				Priority int    `json:"prio"`
				Policy   string `json:"policy"`
			} `json:"chain"`
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
	result := &FirewallCounters{ObservedAt: time.Now().UTC(), Policies: map[string]string{}, RulePackets: map[string]uint64{}}
	counters := map[string]uint64{}
	handles := map[string]uint64{}
	for _, entry := range payload.Nftables {
		if c := entry.Chain; c != nil && c.Family == "inet" && c.Table == "minimalrouter" && c.Hook == c.Name && c.Type == "filter" && c.Priority == 0 && (c.Name == "input" || c.Name == "forward" || c.Name == "output") {
			result.Policies[c.Name] = c.Policy
		}
		if t := entry.Table; t != nil && t.Family == "inet" && t.Name == "minimalrouter" {
			tableHandle = t.Handle
		}
		c := entry.Counter
		if c == nil || c.Family != "inet" || c.Table != "minimalrouter" {
			continue
		}
		if strings.HasPrefix(c.Name, "mr_revision_") {
			if n, err := strconv.ParseUint(strings.TrimPrefix(c.Name, "mr_revision_"), 10, 64); err == nil {
				if result.LoadedRevision != nil {
					return nil, fmt.Errorf("ambiguous revision")
				}
				v := config.Revision(n)
				result.LoadedRevision = &v
			}
		}
		if ruleCounterName.MatchString(c.Name) && len(result.RulePackets) < 1024 {
			result.RulePackets[c.Name] = c.Packets
		}
		if c.Name != "mr_seen" && c.Name != "mr_accepted" {
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
	result.Seen, result.Accepted = counters["mr_seen"], counters["mr_accepted"]
	result.Generation = fmt.Sprintf("%s:%d:%d:%d", boot, tableHandle, handles["mr_seen"], handles["mr_accepted"])
	return result, nil
}
