package config

import (
	"fmt"
	"net"
	"net/netip"
)

// MaxFirewallRuleDestinations and MaxFirewallRuleDestinationPorts bound one
// rule's destination sets. They keep a single rule reviewable and keep the
// rendered nftables anonymous sets small.
const (
	MaxFirewallRuleDestinations     = 128
	MaxFirewallRuleDestinationPorts = 16
)

func networkContainsNetwork(parent, child *net.IPNet) bool {
	p, pb := parent.Mask.Size()
	c, cb := child.Mask.Size()
	return pb == cb && c >= p && parent.Contains(child.IP)
}

// FirewallRuleScope keeps old all-zone denies unchanged. Forward allows have
// always been WAN-only, including configurations created before Scope existed.
func FirewallRuleScope(rule FirewallRule) string {
	if rule.Direction == "input" {
		return "router"
	}
	if rule.Action == "allow" || rule.Scope == "wan" {
		return "wan"
	}
	return "all"
}

// ParseFirewallDestination accepts one custom-rule destination: a global
// unicast IPv4/IPv6 address or network in canonical text form, such as
// "1.1.1.1", "45.90.28.0/24" or "2620:fe::fe". A bare address becomes a
// single-host prefix. Canonical text is required because the value is written
// verbatim into the nftables ruleset and compared verbatim by the dashboard.
func ParseFirewallDestination(value string) (netip.Prefix, bool) {
	var prefix netip.Prefix
	if addr, err := netip.ParseAddr(value); err == nil {
		// PrefixFrom drops a zone, so it must be refused here.
		if addr.String() != value || addr.Zone() != "" {
			return netip.Prefix{}, false
		}
		prefix = netip.PrefixFrom(addr, addr.BitLen())
	} else if parsed, err := netip.ParsePrefix(value); err == nil && parsed.Masked() == parsed && parsed.String() == value {
		prefix = parsed
	} else {
		return netip.Prefix{}, false
	}
	addr := prefix.Addr()
	if addr.Is4In6() || !addr.IsGlobalUnicast() {
		return netip.Prefix{}, false
	}
	return prefix, true
}

// FirewallRuleDestinations splits a rule's validated destinations by address
// family. nftables needs one `ip daddr` and one `ip6 daddr` match, so a rule
// with both families renders as two lines sharing one counter.
func FirewallRuleDestinations(rule FirewallRule) (ipv4, ipv6 []string) {
	for _, value := range rule.DstIPs {
		prefix, ok := ParseFirewallDestination(value)
		if !ok {
			continue
		}
		if prefix.Addr().Is4() {
			ipv4 = append(ipv4, value)
		} else {
			ipv6 = append(ipv6, value)
		}
	}
	return ipv4, ipv6
}

func validateFirewallRulePorts(errs *ValidationErrors, index int, ports []int) {
	field := fmt.Sprintf("firewall.custom_rules[%d].dst_ports", index)
	if len(ports) > MaxFirewallRuleDestinationPorts {
		appendFieldError(errs, field, fmt.Sprintf("may list at most %d ports", MaxFirewallRuleDestinationPorts))
		return
	}
	seen := make(map[int]bool, len(ports))
	for _, port := range ports {
		if port < 1 || port > 65535 {
			appendFieldError(errs, field, "every port must be between 1 and 65535")
			return
		}
		if seen[port] {
			appendFieldError(errs, field, "must not repeat a port")
			return
		}
		seen[port] = true
	}
}

// validateFirewallRuleDestinations keeps destination-scoped rules to what the
// generator renders safely: forward rules only (an input rule's destination
// is the router itself), canonical non-overlapping networks (nftables rejects
// overlapping intervals in one set, which would fail the whole apply at
// preflight), and no IPv6 entry that an IPv4-only match would silently drop.
func validateFirewallRuleDestinations(errs *ValidationErrors, index int, rule FirewallRule) {
	field := fmt.Sprintf("firewall.custom_rules[%d].dst_ips", index)
	if rule.Direction != "forward" {
		appendFieldError(errs, field, "destinations are only valid for forward rules")
		return
	}
	if len(rule.DstIPs) > MaxFirewallRuleDestinations {
		appendFieldError(errs, field, fmt.Sprintf("may list at most %d destinations", MaxFirewallRuleDestinations))
		return
	}
	prefixes := make([]netip.Prefix, 0, len(rule.DstIPs))
	hasIPv6 := false
	for _, value := range rule.DstIPs {
		prefix, ok := ParseFirewallDestination(value)
		if !ok {
			appendFieldError(errs, field, fmt.Sprintf("%q must be a public IPv4/IPv6 address or network in canonical form", value))
			return
		}
		for _, other := range prefixes {
			if other.Overlaps(prefix) {
				appendFieldError(errs, field, fmt.Sprintf("%s overlaps %s", value, other))
				return
			}
		}
		prefixes = append(prefixes, prefix)
		hasIPv6 = hasIPv6 || prefix.Addr().Is6()
	}
	if hasIPv6 && rule.SrcIP != "" {
		appendFieldError(errs, field, "IPv6 destinations cannot be combined with an IPv4 source")
	}
	if hasIPv6 && rule.Protocol == "icmp" {
		appendFieldError(errs, field, "ICMP rules match IPv4 only; remove the IPv6 destinations")
	}
}
