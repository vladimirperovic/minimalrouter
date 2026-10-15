package services

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func FirewallWarnings(cfg config.SystemConfig) []string {
	out := []string{}
	for i, r := range cfg.Firewall.CustomRules {
		if !r.Enabled {
			continue
		}
		if r.Direction == "forward" && r.Action == "allow" {
			out = append(out, fmt.Sprintf("%s: WAN egress is already allowed by default; an allow cannot override an earlier deny or device schedule.", r.Name))
		}
		if r.Direction == "forward" && config.FirewallRuleScope(r) == "all" {
			out = append(out, fmt.Sprintf("%s: applies to every routed destination from LAN, including tunnels and isolated segments.", r.Name))
		}
		for _, other := range cfg.Firewall.CustomRules[:i] {
			if other.Enabled && other.Direction == r.Direction && other.Protocol == r.Protocol && other.SrcIP == r.SrcIP && other.DstPort == r.DstPort && slices.Equal(other.DstPorts, r.DstPorts) && slices.Equal(other.DstIPs, r.DstIPs) && config.FirewallRuleScope(other) == config.FirewallRuleScope(r) {
				out = append(out, fmt.Sprintf("%s and %s overlap: denies are evaluated before allows; identical matches may be redundant.", other.Name, r.Name))
				break
			}
		}
		if len(out) >= 30 {
			out = append(out, "Additional warnings omitted; review all configured rules.")
			break
		}
	}
	return out
}

func DescribeFirewallChanges(before, after config.FirewallConfig) []string {
	changes := []string{}
	describe := func(r config.FirewallRule) string {
		ports := strconv.Itoa(r.DstPort)
		if len(r.DstPorts) > 0 {
			ports = ""
			for i, port := range r.DstPorts {
				if i > 0 {
					ports += ","
				}
				ports += strconv.Itoa(port)
			}
		}
		to := ""
		if len(r.DstIPs) > 0 {
			to = fmt.Sprintf(" to %d destinations", len(r.DstIPs))
		}
		return fmt.Sprintf("%s: %s %s %s/%s from %s%s, scope %s, enabled=%t", r.Name, r.Action, r.Direction, r.Protocol, ports, r.SrcIP, to, config.FirewallRuleScope(r), r.Enabled)
	}
	old := map[string]config.FirewallRule{}
	for _, r := range before.CustomRules {
		old[r.ID] = r
	}
	for _, r := range after.CustomRules {
		if prev, ok := old[r.ID]; !ok {
			changes = append(changes, "Add "+describe(r))
		} else if !config.EqualSection(prev, r) {
			changes = append(changes, "Change "+describe(prev)+" → "+describe(r))
		}
		delete(old, r.ID)
	}
	for _, r := range before.CustomRules {
		if _, ok := old[r.ID]; ok {
			changes = append(changes, "Remove "+describe(r))
		}
	}
	pf := map[string]config.PortForwardRule{}
	for _, r := range before.PortForwards {
		pf[r.ID] = r
	}
	for _, r := range after.PortForwards {
		prev, ok := pf[r.ID]
		label := "Add"
		if ok {
			label = "Change"
		}
		if !ok || !config.EqualSection(prev, r) {
			changes = append(changes, fmt.Sprintf("%s tunnel forward %s: %s/%d → %s:%d, enabled=%t", label, r.Name, r.Protocol, r.ExternalPort, r.InternalIP, r.InternalPort, r.Enabled))
		}
		delete(pf, r.ID)
	}
	for _, r := range before.PortForwards {
		if _, ok := pf[r.ID]; ok {
			changes = append(changes, "Remove tunnel forward "+r.Name)
		}
	}
	if !config.EqualSection(before.ExtraLANs, after.ExtraLANs) {
		changes = append(changes, "Isolated-segment access policy changes; review source networks and destination services.")
	}
	if len(changes) > 50 {
		changes = append(changes[:50], "Additional changes omitted; reduce the batch for a complete review.")
	}
	return changes
}
