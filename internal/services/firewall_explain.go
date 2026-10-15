package services

import (
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

type FirewallFlow struct {
	SourceIP      string `json:"source_ip"`
	DestinationIP string `json:"destination_ip"`
	Protocol      string `json:"protocol"`
	Port          int    `json:"port"`
}

type FirewallExplanation struct {
	Revision        config.Revision `json:"revision"`
	CheckedAt       time.Time       `json:"checked_at"`
	Basis           string          `json:"basis"`
	Verdict         string          `json:"verdict"`
	Reason          string          `json:"reason"`
	RuleID          string          `json:"rule_id,omitempty"`
	SourceZone      string          `json:"source_zone"`
	DestinationZone string          `json:"destination_zone"`
	Limitations     []string        `json:"limitations"`
}

func inNetwork(address, cidr string) bool {
	_, n, err := net.ParseCIDR(cidr)
	return err == nil && n.Contains(net.ParseIP(address))
}

func firewallZone(cfg config.SystemConfig, ip string) string {
	if ip == cfg.LAN.IPAddress || (cfg.WireGuard.Enabled && strings.Split(cfg.WireGuard.Address, "/")[0] == ip) {
		return "router"
	}
	for _, lan := range cfg.Firewall.ExtraLANs {
		if lan.Enabled && strings.Split(lan.RouterAddress, "/")[0] == ip {
			return "router"
		}
	}
	if inNetwork(ip, cfg.LAN.CIDR) {
		return "lan"
	}
	if cfg.WireGuard.Enabled && inNetwork(ip, cfg.WireGuard.Address) {
		return "wireguard"
	}
	for _, lan := range cfg.Firewall.ExtraLANs {
		if lan.Enabled && inNetwork(ip, lan.CIDR) {
			return "segment:" + lan.ID
		}
	}
	if cfg.WGClient.Enabled {
		for _, cidr := range cfg.WGClient.AllowedIPs {
			if inNetwork(ip, cidr) {
				return "tunnel"
			}
		}
	}
	if net.ParseIP(ip).IsGlobalUnicast() && !net.ParseIP(ip).IsPrivate() {
		return "wan"
	}
	return "unknown"
}

// ExplainFirewallFlow evaluates NEW IPv4 flows under the configured topology.
// Dynamic DNS sets, live routes, conntrack and rate limits are deliberately not
// guessed. This is a policy explanation, never a reachability or packet probe.
func ExplainFirewallFlow(cfg config.SystemConfig, flow FirewallFlow) (FirewallExplanation, error) {
	result := FirewallExplanation{Revision: cfg.Revision, CheckedAt: time.Now().UTC(), Basis: "configured_policy", Verdict: "unknown", Limitations: []string{
		"New IPv4 flow; source interface inferred from configured subnets, not observed.",
		"Not a packet test. Live routes, conntrack, rate limits, application access and other firewall tables are not verified.",
	}}
	for _, value := range []string{flow.SourceIP, flow.DestinationIP} {
		if ip := net.ParseIP(value); ip == nil || ip.To4() == nil {
			return result, fmt.Errorf("source_ip and destination_ip must be IPv4 addresses")
		}
	}
	if flow.Protocol != "tcp" && flow.Protocol != "udp" && flow.Protocol != "icmp" {
		return result, fmt.Errorf("protocol must be tcp, udp or icmp")
	}
	if (flow.Protocol == "icmp" && flow.Port != 0) || (flow.Protocol != "icmp" && (flow.Port < 1 || flow.Port > 65535)) {
		return result, fmt.Errorf("port must be 1-65535 for TCP/UDP and zero for ICMP")
	}
	result.SourceZone, result.DestinationZone = firewallZone(cfg, flow.SourceIP), firewallZone(cfg, flow.DestinationIP)
	dnat := ""
	// Prerouting rewrites the destination before input/forward policy. Continue
	// through that policy rather than treating a DNAT match as an early accept.
	if result.SourceZone == "wireguard" {
		for _, pf := range cfg.Firewall.PortForwards {
			if pf.Enabled && pf.ExternalPort == flow.Port && (pf.Protocol == flow.Protocol || pf.Protocol == "both") {
				dnat = fmt.Sprintf("Tunnel DNAT maps to %s:%d. ", pf.InternalIP, pf.InternalPort)
				flow.DestinationIP, flow.Port = pf.InternalIP, pf.InternalPort
				result.DestinationZone = firewallZone(cfg, flow.DestinationIP)
				break
			}
		}
	}
	done := func(verdict, reason string) (FirewallExplanation, error) {
		result.Verdict = verdict
		result.Reason = dnat + reason
		return result, nil
	}
	if result.SourceZone == "unknown" || result.DestinationZone == "unknown" || result.SourceZone == "router" {
		return done("unknown", "This address needs live routing/interface context outside the supported configured topology.")
	}
	if result.SourceZone == result.DestinationZone && (result.SourceZone == "lan" || strings.HasPrefix(result.SourceZone, "segment:")) {
		return done("not_routed", "Devices in the same configured subnet normally communicate directly; router forwarding rules do not protect that path.")
	}
	if result.SourceZone == "wan" {
		return done("unknown", "WAN anti-spoof routing checks and WireGuard endpoint rate limits require live ingress context. New non-WireGuard WAN traffic is denied by configured policy.")
	}
	if result.DestinationZone == "router" {
		if result.SourceZone == "tunnel" {
			return done("deny", "The outbound tunnel cannot initiate connections to the router.")
		}
		if strings.HasPrefix(result.SourceZone, "segment:") {
			if flow.Protocol == "icmp" {
				return done("allow", "Isolated segments may ping the router.")
			}
			return done("deny", "Isolated segments cannot reach router services.")
		}
		if flow.Port == 53 && flow.Protocol != "icmp" {
			return done("allow", "Router DNS is permitted from LAN and authenticated WireGuard; listener availability is not tested.")
		}
		if flow.Protocol == "tcp" && (flow.Port == 22 || flow.Port == cfg.System.HTTPSPort) && (result.SourceZone == "wireguard" || cfg.System.ManagementAccess != "wireguard_only") {
			return done("allow", "Management port is allowed by packet policy; trusted networks and authentication still apply.")
		}
		if result.SourceZone == "lan" {
			if flow.Protocol == "icmp" || (flow.Protocol == "udp" && flow.Port == 67) {
				return done("allow", "Essential LAN router service is permitted.")
			}
			proxyPort := cfg.SquidProxy.Port
			if proxyPort == 0 {
				proxyPort = 3128
			}
			if cfg.SquidProxy.Enabled && flow.Protocol == "tcp" && flow.Port == proxyPort {
				return done("allow", "LAN access to the configured proxy listener is permitted.")
			}
			for _, rule := range cfg.Firewall.CustomRules {
				if rule.Enabled && rule.Direction == "input" && rule.Action == "allow" && firewallRuleMatches(rule, flow) {
					result.RuleID = rule.ID
					return done("allow", "Matching custom router-input allowance.")
				}
			}
		}
		return done("deny", "No router-input allowance matches this new flow.")
	}
	// Device schedules precede custom denies and may depend on dynamic nft sets.
	if len(activeManagedServices(&cfg)) > 0 {
		for _, profile := range cfg.AdGuard.DeviceProfiles {
			if profile.Enabled {
				for _, ip := range profile.IPAddresses {
					if ip == flow.SourceIP {
						if flow.Protocol != "icmp" && (flow.Port == 53 || flow.Port == 853) {
							return done("deny", "Device schedules block direct DNS/DoT before custom rules.")
						}
						return done("unknown", "This device has a schedule. Its dynamic DNS destination sets and router-time enforcement must be inspected before deciding.")
					}
				}
			}
		}
	}
	if result.SourceZone == "lan" {
		for _, rule := range cfg.Firewall.CustomRules {
			if rule.Enabled && rule.Direction == "forward" && rule.Action == "deny" && firewallRuleMatches(rule, flow) && (config.FirewallRuleScope(rule) == "all" || (result.DestinationZone == "wan" && cfg.WAN.Enabled)) {
				result.RuleID = rule.ID
				return done("deny", "Matching custom deny runs before established connections and all custom allows.")
			}
		}
	}
	if cfg.SquidProxy.Enabled {
		for _, ip := range cfg.SquidProxy.RestrictedIPs {
			if ip.Enabled && ip.IPAddress == flow.SourceIP {
				return done("deny", "Proxy restriction blocks direct forwarded traffic from this address.")
			}
		}
	}
	if result.SourceZone == "tunnel" {
		return done("deny", "The outbound tunnel may only return traffic; new remote initiations are denied.")
	}
	if strings.HasPrefix(result.SourceZone, "segment:") {
		return done("deny", "Isolated segments cannot initiate routed connections.")
	}
	if strings.HasPrefix(result.DestinationZone, "segment:") {
		for _, lan := range cfg.Firewall.ExtraLANs {
			if lan.Enabled && flow.DestinationIP == lan.DstIP && flow.Port == lan.DstPort && (flow.Protocol == lan.Protocol || (lan.Protocol == "" && flow.Protocol == "tcp")) {
				for _, src := range lan.AllowFrom {
					if inNetwork(flow.SourceIP, src) {
						return done("allow", "The isolated-segment service ACL permits this source, destination and port.")
					}
				}
			}
		}
		return done("deny", "The isolated-segment service ACL does not allow this flow.")
	}
	if result.DestinationZone == "wan" && cfg.WAN.Enabled && (result.SourceZone == "lan" || result.SourceZone == "wireguard") {
		if result.SourceZone == "lan" {
			for _, rule := range cfg.Firewall.CustomRules {
				if rule.Enabled && rule.Direction == "forward" && rule.Action == "allow" && firewallRuleMatches(rule, flow) {
					result.RuleID = rule.ID
					return done("allow", "Matching custom WAN allowance, after earlier restrictions. Default LAN-to-WAN egress also permits this path.")
				}
			}
		}
		return done("allow", "Default configured egress allows this source toward WAN, after earlier restrictions.")
	}
	if result.SourceZone == "wireguard" && result.DestinationZone == "lan" {
		return done("allow", "Authenticated WireGuard clients may reach the LAN; DNAT is not an access-control list.")
	}
	if result.DestinationZone == "tunnel" && (result.SourceZone == "lan" || result.SourceZone == "wireguard") {
		return done("allow", "Trusted LAN/WireGuard sources may initiate toward the configured outbound tunnel.")
	}
	return done("deny", "No forwarding allowance matches this new flow.")
}

func firewallRuleMatches(rule config.FirewallRule, flow FirewallFlow) bool {
	if rule.SrcIP != "" && rule.SrcIP != flow.SourceIP && !inNetwork(flow.SourceIP, rule.SrcIP) {
		return false
	}
	if len(rule.DstIPs) > 0 && !firewallRuleMatchesDestination(rule, flow.DestinationIP) {
		return false
	}
	return rule.Protocol == "any" || (rule.Protocol == flow.Protocol && (rule.Protocol == "icmp" || rule.DstPort == flow.Port || slices.Contains(rule.DstPorts, flow.Port)))
}

func firewallRuleMatchesDestination(rule config.FirewallRule, destination string) bool {
	addr, err := netip.ParseAddr(destination)
	if err != nil {
		return false
	}
	for _, value := range rule.DstIPs {
		if prefix, ok := config.ParseFirewallDestination(value); ok && prefix.Contains(addr.Unmap()) {
			return true
		}
	}
	return false
}
