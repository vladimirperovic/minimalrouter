package services

import (
	"bytes"
	"fmt"
	"net"
	"strings"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// wgPeerEndpointNets returns literal IPv4 addresses of enabled WireGuard
// server peer endpoints. They are used only to keep the WireGuard UDP socket
// reachable when a legitimate peer lives behind an ISP/private WAN address.
// A peer endpoint address is never a trust identity: it may be shared NAT and
// must never receive a broad input or forward ACCEPT.
func wgPeerEndpointNets(cfg *config.SystemConfig) []string {
	if !cfg.WireGuard.Enabled {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, peer := range cfg.WireGuard.Peers {
		if !peer.Enabled {
			continue
		}
		host, _, err := net.SplitHostPort(peer.Endpoint)
		if err != nil {
			host = peer.Endpoint
		}
		ip := net.ParseIP(host)
		if ip == nil || ip.To4() == nil {
			continue
		}
		cidr := ip.String() + "/32"
		if !seen[cidr] {
			seen[cidr] = true
			out = append(out, cidr)
		}
	}
	return out
}

// writeKnownWGPeerEndpointInputRules creates the narrow exception needed when
// a configured WireGuard peer endpoint is itself in a source range that the
// WAN anti-spoof policy normally drops (for example a private ISP-side lab).
// The exception is deliberately limited to UDP on the WireGuard listen port;
// the source IP can never bypass normal router services or the forward chain.
func writeKnownWGPeerEndpointInputRules(buf *bytes.Buffer, cfg *config.SystemConfig) {
	endpoints := wgPeerEndpointNets(cfg)
	if len(endpoints) == 0 || !cfg.WireGuard.Enabled {
		return
	}
	sources := strings.Join(endpoints, ", ")
	port := cfg.WireGuard.ListenPort
	buf.WriteString("    # Known WireGuard peer endpoints: UDP socket only, never general trust\n")
	if cfg.WAN.Interface != "" {
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" ip saddr { %s } ct state invalid drop\n", cfg.WAN.Interface, sources))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" ip saddr { %s } udp dport %d ct state new meter wg_known_wan_rate { ip saddr timeout 10s limit rate over 20/second burst 40 packets } drop\n", cfg.WAN.Interface, sources, port))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" ip saddr { %s } udp dport %d accept\n", cfg.WAN.Interface, sources, port))
	}
	buf.WriteString(fmt.Sprintf("    iifname \"ppp*\" ip saddr { %s } ct state invalid drop\n", sources))
	buf.WriteString(fmt.Sprintf("    iifname \"ppp*\" ip saddr { %s } udp dport %d ct state new meter wg_known_ppp_rate { ip saddr timeout 10s limit rate over 20/second burst 40 packets } drop\n", sources, port))
	buf.WriteString(fmt.Sprintf("    iifname \"ppp*\" ip saddr { %s } udp dport %d accept\n\n", sources, port))
}

// lanBroadcastAddress computes the IPv4 broadcast address of a CIDR, or ""
// when the prefix is not a valid IPv4 network.
func lanBroadcastAddress(cidr string) string {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}
	ipv4 := ipNet.IP.To4()
	if ipv4 == nil {
		return ""
	}
	broadcast := make(net.IP, len(ipv4))
	for i := range ipv4 {
		broadcast[i] = ipv4[i] | ^ipNet.Mask[i]
	}
	return broadcast.String()
}

func writeCustomRules(buf *bytes.Buffer, cfg *config.SystemConfig, direction, action string) {
	for _, rule := range cfg.Firewall.CustomRules {
		if !rule.Enabled || rule.Direction != direction || rule.Action != action {
			continue
		}
		match := []string{fmt.Sprintf("iifname \"%s\"", cfg.LAN.Interface)}
		if rule.SrcIP != "" {
			match = append(match, "ip saddr "+rule.SrcIP)
		}
		if rule.Protocol != "any" {
			match = append(match, rule.Protocol)
		}
		if rule.Protocol == "tcp" || rule.Protocol == "udp" {
			match = append(match, fmt.Sprintf("dport %d", rule.DstPort))
		}
		if direction == "forward" && action == "allow" {
			// Custom forward allows are strictly LAN -> WAN egress. They must
			// never match traffic toward an extra LAN or a tunnel, or a generic
			// rule could silently override segment isolation policies.
			if !cfg.WAN.Enabled {
				continue
			}
			match = append(match, "oifname { "+cfg.WAN.Interface+", ppp* }")
		}
		verdict := "drop"
		if action == "allow" {
			verdict = "accept"
		}
		buf.WriteString(fmt.Sprintf("    # Custom LAN %s %s rule: %s\n", direction, action, sanitizeComment(rule.Name)))
		buf.WriteString(fmt.Sprintf("    %s %s\n", strings.Join(match, " "), verdict))
	}
}

// sanitizeComment strips line breaks and control characters from user text
// before it is embedded in generated firewall comments. Validation already
// rejects control characters, but the generator must stay safe even if a
// future caller skips validation.
func sanitizeComment(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	for _, r := range value {
		if r < 0x20 && r != ' ' {
			return strings.Map(func(c rune) rune {
				if c < 0x20 && c != ' ' {
					return '?'
				}
				return c
			}, value)
		}
	}
	return value
}

// extraLANSourceInterface maps an AllowFrom CIDR to the router interface that
// carries it: any subnet of the LAN segment or the WireGuard tunnel, down to a
// single-device /32. Anything outside a trusted source zone is silently
// skipped (validation rejects such configs).
func extraLANSourceInterface(cfg *config.SystemConfig, srcCIDR string) string {
	_, srcNet, err := net.ParseCIDR(srcCIDR)
	if err != nil {
		return ""
	}
	srcOnes, _ := srcNet.Mask.Size()
	if _, lanNet, err := net.ParseCIDR(cfg.LAN.CIDR); err == nil {
		if zoneOnes, _ := lanNet.Mask.Size(); lanNet.Contains(srcNet.IP) && srcOnes >= zoneOnes {
			return cfg.RuntimeLANInterface()
		}
	}
	if cfg.WireGuard.Enabled {
		if _, wgNetwork, err := net.ParseCIDR(cfg.WireGuard.Address); err == nil {
			if zoneOnes, _ := wgNetwork.Mask.Size(); wgNetwork.Contains(srcNet.IP) && srcOnes >= zoneOnes {
				return cfg.WireGuard.Interface
			}
		}
	}
	return ""
}

// writeWGClientInputRules confines the outbound tunnel (wg1) to established
// traffic: the remote site can never initiate toward the router itself.
func writeWGClientInputRules(buf *bytes.Buffer, cfg *config.SystemConfig) {
	if !cfg.WGClient.Enabled {
		return
	}
	iface := wgClientInterface(cfg)
	allowed := make([]string, 0, len(cfg.WGClient.AllowedIPs))
	for _, entry := range cfg.WGClient.AllowedIPs {
		if _, network, err := net.ParseCIDR(strings.TrimSpace(entry)); err == nil && network.IP.To4() != nil {
			allowed = append(allowed, network.String())
		}
	}
	spoof := "drop"
	if len(allowed) > 0 {
		spoof = fmt.Sprintf("ip saddr != { %s } drop", strings.Join(allowed, ", "))
	}
	buf.WriteString(fmt.Sprintf("    # WireGuard client %s: established-only (remote site cannot initiate)\n", iface))
	buf.WriteString(fmt.Sprintf("    iifname \"%s\" %s\n", iface, spoof))
	buf.WriteString(fmt.Sprintf("    iifname \"%s\" ct state new drop\n\n", iface))
}

// writeWGClientForwardRules lets trusted home networks (LAN, wg0) dial out
// through the tunnel; remote initiations are dropped while responses ride the
// global established rule. Isolated extra LANs are deliberately NOT included:
// granting them tunnel egress would break their isolation model.
func writeWGClientForwardRules(buf *bytes.Buffer, cfg *config.SystemConfig) {
	if !cfg.WGClient.Enabled {
		return
	}
	iface := wgClientInterface(cfg)
	buf.WriteString(fmt.Sprintf("    # WireGuard client %s: trusted home networks may dial out, remote site may only respond\n", iface))
	forwardSources := []string{cfg.LAN.Interface}
	if cfg.WireGuard.Enabled && cfg.WireGuard.Interface != "" {
		forwardSources = append(forwardSources, cfg.WireGuard.Interface)
	}
	for _, src := range forwardSources {
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" oifname \"%s\" accept\n", src, iface))
	}
	buf.WriteString(fmt.Sprintf("    iifname \"%s\" ct state new drop\n\n", iface))
}

// writeWGClientOutputRules allows the encapsulated WireGuard stream to the
// remote endpoint on any WAN path.
func writeWGClientOutputRules(buf *bytes.Buffer, cfg *config.SystemConfig) {
	if !cfg.WGClient.Enabled {
		return
	}
	_, portText, err := net.SplitHostPort(cfg.WGClient.Endpoint)
	if err != nil || portText == "" {
		return
	}
	if cfg.WAN.Interface != "" {
		buf.WriteString(fmt.Sprintf("    oifname \"%s\" udp dport %s accept\n", cfg.WAN.Interface, portText))
	}
	buf.WriteString(fmt.Sprintf("    oifname \"ppp*\" udp dport %s accept\n", portText))
}

func wgClientInterface(cfg *config.SystemConfig) string {
	if cfg.WGClient.Interface != "" {
		return cfg.WGClient.Interface
	}
	return "wg1"
}

// writeWGClientMasquerade hides the home source behind the tunnel address so
// the remote site only ever sees the client's own tunnel IP.
func writeWGClientMasquerade(buf *bytes.Buffer, cfg *config.SystemConfig) {
	if !cfg.WGClient.Enabled {
		return
	}
	buf.WriteString(fmt.Sprintf("    oifname \"%s\" masquerade\n", wgClientInterface(cfg)))
}

// writeExtraLANInputRules protects the router itself from the isolated
// segment: source anti-spoofing plus the documented minimum (ICMP ping to the
// router). No router service is reachable from an extra LAN.
func writeExtraLANInputRules(buf *bytes.Buffer, cfg *config.SystemConfig) {
	for _, lan := range cfg.Firewall.ExtraLANs {
		if !lan.Enabled || lan.Interface == "" || lan.CIDR == "" {
			continue
		}
		buf.WriteString(fmt.Sprintf("    # Extra LAN %s (%s): isolated segment, router hosts no services here\n", lan.Interface, sanitizeComment(lan.Name)))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" ip saddr != { %s } drop\n", lan.Interface, lan.CIDR))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" ip protocol icmp accept\n", lan.Interface))
	}
}

// writeExtraLANForwardRules allows exactly the configured AllowFrom networks
// toward the single service DstIP:DstPort on the segment. The default policy
// and the absence of any generic egress rule keep the segment isolated: no
// WAN, LAN, wg0 or wg1 egress is possible from it, and replies to allowed
// initiations ride the global established rule.
func writeExtraLANForwardRules(buf *bytes.Buffer, cfg *config.SystemConfig) {
	for _, lan := range cfg.Firewall.ExtraLANs {
		if !lan.Enabled || lan.Interface == "" || lan.CIDR == "" || lan.DstIP == "" {
			continue
		}
		proto := lan.Protocol
		if proto == "" {
			proto = "tcp"
		}
		buf.WriteString(fmt.Sprintf("    # Extra LAN %s (%s): isolated segment, only %s sources reach %s:%d on %s\n",
			lan.Interface, sanitizeComment(lan.Name), lan.CIDR, lan.DstIP, lan.DstPort, lan.Interface))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" ip saddr != %s drop\n", lan.Interface, lan.CIDR))
		for _, src := range lan.AllowFrom {
			iface := extraLANSourceInterface(cfg, src)
			if iface == "" {
				continue
			}
			buf.WriteString(fmt.Sprintf("    iifname \"%s\" ip saddr %s ip daddr %s %s dport %d oifname \"%s\" accept\n",
				iface, src, lan.DstIP, proto, lan.DstPort, lan.Interface))
		}
	}
}

// GenerateNftables renders an atomic, deterministic nftables configuration string.
// AccountingSetRX and AccountingSetTX are dynamic nftables sets keyed by LAN
// host address, each carrying a byte counter. They are read (never written) by
// routerd through two exact doas-allowlisted `nft -j list set` commands.
//
// The table is deleted and recreated on every apply, so these counters restart
// from zero whenever configuration changes. The accounting collector treats any
// decrease as a reset rather than as negative traffic.
const (
	AccountingSetRX = "acct_rx"
	AccountingSetTX = "acct_tx"
)

// writeAccountingSets declares the per-host byte counters. Size is bounded so a
// hostile or misconfigured LAN cannot grow kernel memory without limit, and the
// timeout reclaims entries for devices that have left the network.
func writeAccountingSets(buf *bytes.Buffer, cfg *config.SystemConfig) {
	if !cfg.Accounting.Enabled || cfg.LAN.Interface == "" {
		return
	}
	for _, name := range []string{AccountingSetRX, AccountingSetTX} {
		buf.WriteString(fmt.Sprintf("  set %s {\n", name))
		buf.WriteString("    type ipv4_addr\n")
		buf.WriteString("    size 512\n")
		buf.WriteString("    flags dynamic,timeout\n")
		buf.WriteString("    timeout 7d\n")
		buf.WriteString("    counter\n")
		buf.WriteString("  }\n\n")
	}
}

// writeAccountingRules counts forwarded bytes per LAN host. The rules are pure
// counters with no verdict, placed after the anti-spoof drops so only traffic
// that already passed policy is measured. Download is keyed by destination
// address, upload by source address.
func writeAccountingRules(buf *bytes.Buffer, cfg *config.SystemConfig) {
	if !cfg.Accounting.Enabled || cfg.LAN.Interface == "" {
		return
	}
	buf.WriteString("    # Per-device byte accounting (counters only, no verdict)\n")
	buf.WriteString(fmt.Sprintf("    iifname \"%s\" update @%s { ip saddr }\n", cfg.LAN.Interface, AccountingSetTX))
	buf.WriteString(fmt.Sprintf("    oifname \"%s\" update @%s { ip daddr }\n\n", cfg.LAN.Interface, AccountingSetRX))
}

// enabledTunnelPortForwards returns the port forwards that are reachable over
// the WireGuard management tunnel. WAN-scoped exposure remains unsupported, so
// nothing here can ever produce a rule bound to a WAN or ppp interface.
func enabledTunnelPortForwards(cfg *config.SystemConfig) []config.PortForwardRule {
	if !cfg.WireGuard.Enabled {
		return nil
	}
	var out []config.PortForwardRule
	for _, rule := range cfg.Firewall.PortForwards {
		if rule.Enabled {
			out = append(out, rule)
		}
	}
	return out
}

// writeTunnelPortForwards emits the prerouting DNAT chain. Every rule is bound
// to the WireGuard server interface, which is the appliance's only permitted
// external entry point; a forward arriving on WAN or ppp0 matches nothing here
// and is dropped by the default WAN policy exactly as before.
func writeTunnelPortForwards(buf *bytes.Buffer, cfg *config.SystemConfig) {
	rules := enabledTunnelPortForwards(cfg)
	if len(rules) == 0 {
		return
	}
	wgInterface := cfg.WireGuard.Interface
	if wgInterface == "" {
		wgInterface = "wg0"
	}
	buf.WriteString("  chain prerouting {\n")
	buf.WriteString("    type nat hook prerouting priority dstnat; policy accept;\n\n")
	buf.WriteString("    # Port forwards are reachable only over the WireGuard tunnel, never the WAN.\n")
	for _, rule := range rules {
		for _, protocol := range portForwardProtocols(rule.Protocol) {
			buf.WriteString(fmt.Sprintf("    iifname \"%s\" %s dport %d dnat ip to %s:%d\n",
				wgInterface, protocol, rule.ExternalPort, rule.InternalIP, rule.InternalPort))
		}
	}
	buf.WriteString("  }\n\n")
}

// writeTunnelPortForwardForward opens the matching forward path. DNAT rewrites
// the destination before the forward chain runs, so the accept must match the
// translated address rather than the original port.
func writeTunnelPortForwardForward(buf *bytes.Buffer, cfg *config.SystemConfig) {
	rules := enabledTunnelPortForwards(cfg)
	if len(rules) == 0 {
		return
	}
	wgInterface := cfg.WireGuard.Interface
	if wgInterface == "" {
		wgInterface = "wg0"
	}
	buf.WriteString("    # Tunnel port forwards: allow the DNAT-translated destination\n")
	for _, rule := range rules {
		for _, protocol := range portForwardProtocols(rule.Protocol) {
			buf.WriteString(fmt.Sprintf("    iifname \"%s\" oifname \"%s\" ip daddr %s %s dport %d ct state new accept\n",
				wgInterface, cfg.LAN.Interface, rule.InternalIP, protocol, rule.InternalPort))
		}
	}
	buf.WriteString("\n")
}

func portForwardProtocols(protocol string) []string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "both":
		return []string{"tcp", "udp"}
	case "udp":
		return []string{"udp"}
	default:
		return []string{"tcp"}
	}
}

func GenerateNftables(cfg *config.SystemConfig) (string, error) {
	// Scenario-level policy is enforced in the generator as a second trust
	// boundary. Even if a future control-plane path forgets its own guard, the
	// privileged helper regenerating nftables cannot install a management-
	// lockout rule or other explicitly unsupported topology.
	if err := cfg.ValidateScenarioSafety(); err != nil {
		return "", fmt.Errorf("scenario safety: %w", err)
	}

	runtimeCfg := *cfg
	runtimeCfg.LAN.Interface = cfg.RuntimeLANInterface()
	cfg = &runtimeCfg

	var buf bytes.Buffer

	buf.WriteString("# Generated by Minimal Router OS — do not edit manually\n")
	buf.WriteString("# Table: inet minimalrouter\n\n")

	// router-applyd wraps this owned table in an atomic delete-and-create batch.
	buf.WriteString("table inet minimalrouter {\n")
	writeDeviceProfileObjects(&buf, cfg)
	writeAccountingSets(&buf, cfg)

	// Input Chain
	buf.WriteString("  chain input {\n")
	buf.WriteString("    type filter hook input priority filter; policy drop;\n\n")
	buf.WriteString("    # Allow loopback\n")
	buf.WriteString("    iifname \"lo\" accept\n\n")

	if cfg.WAN.Interface != "" {
		writeKnownWGPeerEndpointInputRules(&buf, cfg)
		buf.WriteString("    # Drop spoofed/reserved WAN sources before connection tracking accepts\n")
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" ip saddr { 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.0.0.0/24, 192.0.2.0/24, 192.168.0.0/16, 198.18.0.0/15, 198.51.100.0/24, 203.0.113.0/24, 224.0.0.0/3 } drop\n", cfg.WAN.Interface))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" fib saddr . iif oif missing drop\n", cfg.WAN.Interface))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" meta nfproto ipv6 drop\n", cfg.WAN.Interface))
		buf.WriteString("    iifname \"ppp*\" ip saddr { 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.0.0.0/24, 192.0.2.0/24, 192.168.0.0/16, 198.18.0.0/15, 198.51.100.0/24, 203.0.113.0/24, 224.0.0.0/3 } drop\n")
		buf.WriteString("    iifname \"ppp*\" fib saddr . iif oif missing drop\n")
		buf.WriteString("    iifname \"ppp*\" meta nfproto ipv6 drop\n\n")
	}
	if cfg.LAN.Interface != "" {
		buf.WriteString(fmt.Sprintf("    # LAN source anti-spoofing (0.0.0.0 is needed for DHCP discovery)\n    iifname \"%s\" ip saddr != { 0.0.0.0, %s } drop\n\n", cfg.LAN.Interface, cfg.LAN.CIDR))
	}

	buf.WriteString("    # Reject invalid packets before service accepts\n")
	buf.WriteString("    ct state invalid drop\n")
	writeCustomRules(&buf, cfg, "input", "deny")
	buf.WriteString("    # Allow established and related connections\n")
	buf.WriteString("    ct state established,related accept\n\n")

	if cfg.WireGuard.Enabled {
		_, wgNetwork, _ := net.ParseCIDR(cfg.WireGuard.Address)
		buf.WriteString("    # WireGuard endpoint with a per-source flood guard\n")
		if cfg.WAN.Interface != "" {
			buf.WriteString(fmt.Sprintf("    iifname \"%s\" udp dport %d ct state new meter wg_wan_rate { ip saddr timeout 10s limit rate over 20/second burst 40 packets } drop\n", cfg.WAN.Interface, cfg.WireGuard.ListenPort))
			buf.WriteString(fmt.Sprintf("    iifname \"%s\" udp dport %d accept\n", cfg.WAN.Interface, cfg.WireGuard.ListenPort))
		}
		buf.WriteString(fmt.Sprintf("    iifname \"ppp*\" udp dport %d ct state new meter wg_ppp_rate { ip saddr timeout 10s limit rate over 20/second burst 40 packets } drop\n", cfg.WireGuard.ListenPort))
		buf.WriteString(fmt.Sprintf("    iifname \"ppp*\" udp dport %d accept\n", cfg.WireGuard.ListenPort))
		buf.WriteString("    # Authenticated tunnel services\n")
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" ip saddr != %s drop\n", cfg.WireGuard.Interface, wgNetwork.String()))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" tcp dport %d accept\n", cfg.WireGuard.Interface, cfg.System.HTTPSPort))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" udp dport 53 accept\n", cfg.WireGuard.Interface))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" tcp dport 53 accept\n\n", cfg.WireGuard.Interface))
	}

	writeWGClientInputRules(&buf, cfg)

	if cfg.SquidProxy.Enabled {
		port := cfg.SquidProxy.Port
		if port == 0 {
			port = 3128
		}
		if cfg.LAN.Interface != "" {
			buf.WriteString(fmt.Sprintf("    # Allow Squid Proxy from LAN (%s, port %d)\n", cfg.LAN.Interface, port))
			buf.WriteString(fmt.Sprintf("    iifname \"%s\" tcp dport %d accept\n\n", cfg.LAN.Interface, port))
		}
	}

	if cfg.LAN.Interface != "" {
		buf.WriteString(fmt.Sprintf("    # Allow essential network services from LAN (%s) only\n", cfg.LAN.Interface))
		if cfg.System.ManagementAccess != "wireguard_only" {
			buf.WriteString(fmt.Sprintf("    iifname \"%s\" tcp dport %d ct state new limit rate over 30/second burst 60 packets drop\n", cfg.LAN.Interface, cfg.System.HTTPSPort))
			buf.WriteString(fmt.Sprintf("    iifname \"%s\" tcp dport %d accept\n", cfg.LAN.Interface, cfg.System.HTTPSPort))
		} else {
			buf.WriteString("    # Management HTTPS is intentionally unavailable on LAN; use WireGuard\n")
		}
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" udp dport { 53, 67 } accept\n", cfg.LAN.Interface))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" tcp dport 53 accept\n", cfg.LAN.Interface))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" ip protocol icmp accept\n\n", cfg.LAN.Interface))
	}
	writeExtraLANInputRules(&buf, cfg)
	if cfg.WireGuard.Enabled {
		_, wgNetwork, _ := net.ParseCIDR(cfg.WireGuard.Address)
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" ip saddr != %s drop\n\n", cfg.WireGuard.Interface, wgNetwork.String()))
	}

	writeCustomRules(&buf, cfg, "input", "allow")
	buf.WriteString("    # Reject WAN unsolicited input\n")
	buf.WriteString("    drop\n")
	buf.WriteString("  }\n\n")

	// Forward Chain
	buf.WriteString("  chain forward {\n")
	buf.WriteString("    type filter hook forward priority filter; policy drop;\n\n")
	if cfg.WAN.Interface != "" {
		// A configured WireGuard peer endpoint is never a forward trust
		// identity. WAN traffic still passes the normal anti-spoof and
		// established/default-deny policy regardless of source address.
		buf.WriteString("    # WAN anti-spoofing precedes state acceptance\n")
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" ip saddr { 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.0.0.0/24, 192.0.2.0/24, 192.168.0.0/16, 198.18.0.0/15, 198.51.100.0/24, 203.0.113.0/24, 224.0.0.0/3 } drop\n", cfg.WAN.Interface))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" fib saddr . iif oif missing drop\n", cfg.WAN.Interface))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" meta nfproto ipv6 drop\n", cfg.WAN.Interface))
		buf.WriteString("    iifname \"ppp*\" ip saddr { 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.0.0.0/24, 192.0.2.0/24, 192.168.0.0/16, 198.18.0.0/15, 198.51.100.0/24, 203.0.113.0/24, 224.0.0.0/3 } drop\n")
		buf.WriteString("    iifname \"ppp*\" fib saddr . iif oif missing drop\n")
		buf.WriteString("    iifname \"ppp*\" meta nfproto ipv6 drop\n\n")
	}
	if cfg.LAN.Interface != "" {
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" ip saddr != %s drop\n\n", cfg.LAN.Interface, cfg.LAN.CIDR))
	}
	buf.WriteString("    # Reject invalid before established state\n")
	buf.WriteString("    ct state invalid drop\n")
	if len(activeManagedServices(cfg)) > 0 {
		buf.WriteString("    # Device schedules run before established acceptance so expired streams are cut\n")
		buf.WriteString("    jump device_profiles\n")
	}
	writeCustomRules(&buf, cfg, "forward", "deny")

	if cfg.SquidProxy.Enabled && len(cfg.SquidProxy.RestrictedIPs) > 0 {
		buf.WriteString("    # Block direct WAN traffic for Restricted IP Alias (Must use Squid Proxy)\n")
		for _, item := range cfg.SquidProxy.RestrictedIPs {
			ip := strings.TrimSpace(item.IPAddress)
			if ip != "" && item.Enabled {
				buf.WriteString(fmt.Sprintf("    ip saddr %s drop\n", ip))
			}
		}
		buf.WriteString("\n")
	}

	// Counters must precede every accept. accept is a terminal verdict for the
	// chain, so counters placed after the accept rules only ever saw traffic on
	// its way to the policy drop -- which reported near-zero usage for every
	// device while looking like a working feature. Invalid packets are already
	// dropped above, so nothing counted here is junk.
	writeAccountingRules(&buf, cfg)
	buf.WriteString("    # Allow established/related\n")
	buf.WriteString("    ct state established,related accept\n\n")
	buf.WriteString("    # TCP MSS clamping prevents PPPoE fragmentation stalls\n")
	buf.WriteString("    tcp flags syn tcp option maxseg size set rt mtu\n\n")
	writeCustomRules(&buf, cfg, "forward", "allow")
	writeExtraLANForwardRules(&buf, cfg)

	if cfg.WAN.Enabled && cfg.LAN.Interface != "" {
		buf.WriteString(fmt.Sprintf("    # Allow LAN (%s) out to WAN\n", cfg.LAN.Interface))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" oifname \"%s\" accept\n", cfg.LAN.Interface, cfg.WAN.Interface))
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" oifname \"ppp*\" accept\n\n", cfg.LAN.Interface))
	}
	if cfg.WireGuard.Enabled {
		buf.WriteString("    # Authenticated WireGuard clients may reach LAN and optional full-tunnel WAN\n")
		buf.WriteString(fmt.Sprintf("    iifname \"%s\" oifname \"%s\" accept\n", cfg.WireGuard.Interface, cfg.LAN.Interface))
		if cfg.WAN.Enabled {
			buf.WriteString(fmt.Sprintf("    iifname \"%s\" oifname \"%s\" accept\n", cfg.WireGuard.Interface, cfg.WAN.Interface))
			buf.WriteString(fmt.Sprintf("    iifname \"%s\" oifname \"ppp*\" accept\n", cfg.WireGuard.Interface))
		}
		buf.WriteString("\n")
	}
	writeTunnelPortForwardForward(&buf, cfg)
	writeWGClientForwardRules(&buf, cfg)
	buf.WriteString("  }\n\n")

	// Output Chain
	buf.WriteString("  chain output {\n")
	buf.WriteString("    type filter hook output priority filter; policy drop;\n\n")
	buf.WriteString("    # The appliance itself has a narrow egress allowlist\n")
	buf.WriteString("    oifname \"lo\" accept\n")
	if cfg.WAN.Interface != "" {
		buf.WriteString("    # Never leak source addresses that are not assigned to the appliance itself.\n")
		buf.WriteString("    # fib-based (rather than a static private-range list) so an ISP-assigned\n")
		buf.WriteString("    # private or CGNAT WAN address (10.x, 100.64.0.0/10) remains routable.\n")
		buf.WriteString("    # NOTE: fib saddr . iif oif is not supported in the output hook (kernel\n")
		buf.WriteString("    # rejects it), hence fib saddr type != local here; forward chain uses\n")
		buf.WriteString("    # fib saddr . iif oif which is valid there.\n")
		buf.WriteString(fmt.Sprintf("    oifname \"%s\" fib saddr type != local drop\n", cfg.WAN.Interface))
		buf.WriteString("    oifname \"ppp*\" fib saddr type != local drop\n")
	}
	buf.WriteString("    ct state invalid drop\n")

	// A pre-existing Squid connection to a private segment must be cut when
	// proxy isolation is enabled/changed; these UID+zone denies deliberately
	// precede established/related acceptance.
	if cfg.SquidProxy.Enabled {
		if cfg.LAN.Interface != "" {
			// Squid's listener is on the LAN interface. Its response packets
			// therefore leave with the proxy port as their source port, but
			// still carry the squid UID. Allow only those replies before the
			// broad UID/private-zone deny below; arbitrary Squid egress to the
			// LAN remains blocked.
			buf.WriteString(fmt.Sprintf("    meta skuid squid oifname \"%s\" tcp sport %d accept\n", cfg.LAN.Interface, cfg.SquidProxy.Port))
			buf.WriteString(fmt.Sprintf("    meta skuid squid oifname \"%s\" drop\n", cfg.LAN.Interface))
		}
		if cfg.WireGuard.Enabled && cfg.WireGuard.Interface != "" {
			buf.WriteString(fmt.Sprintf("    meta skuid squid oifname \"%s\" drop\n", cfg.WireGuard.Interface))
		}
		if cfg.WGClient.Enabled {
			buf.WriteString(fmt.Sprintf("    meta skuid squid oifname \"%s\" drop\n", wgClientInterface(cfg)))
		}
		for _, lan := range cfg.Firewall.ExtraLANs {
			if lan.Enabled && lan.Interface != "" {
				buf.WriteString(fmt.Sprintf("    meta skuid squid oifname \"%s\" drop\n", lan.Interface))
			}
		}
	}

	buf.WriteString("    ct state established,related accept\n")
	buf.WriteString(fmt.Sprintf("    oifname \"%s\" udp sport { 53, 67 } accept\n", cfg.LAN.Interface))
	if cfg.System.ManagementAccess == "wireguard_only" {
		buf.WriteString(fmt.Sprintf("    oifname \"%s\" tcp sport 53 accept\n", cfg.LAN.Interface))
	} else {
		buf.WriteString(fmt.Sprintf("    oifname \"%s\" tcp sport { 53, %d } accept\n", cfg.LAN.Interface, cfg.System.HTTPSPort))
	}
	if cfg.WireGuard.Enabled {
		buf.WriteString(fmt.Sprintf("    oifname \"%s\" udp sport { 53, %d } accept\n", cfg.WireGuard.Interface, cfg.WireGuard.ListenPort))
		buf.WriteString(fmt.Sprintf("    oifname \"%s\" tcp sport { 53, %d } accept\n", cfg.WireGuard.Interface, cfg.System.HTTPSPort))
	}
	writeWGClientOutputRules(&buf, cfg)
	if len(cfg.DHCP.DNSServers) > 0 {
		buf.WriteString(fmt.Sprintf("    ip daddr { %s } udp dport 53 accept\n", strings.Join(cfg.DHCP.DNSServers, ", ")))
		buf.WriteString(fmt.Sprintf("    ip daddr { %s } tcp dport 53 accept\n", strings.Join(cfg.DHCP.DNSServers, ", ")))
	}
	buf.WriteString("    udp dport 123 accept\n")
	buf.WriteString("    ip protocol icmp accept\n\n")
	// routerd status/update/TOTP helper calls use ordinary HTTPS but remain
	// confined to WAN interfaces. DDNS adds scoped root/inadyn HTTPS egrel;
	// enabling the feature never opens generic HTTPS from arbitrary UIDs.
	buf.WriteString("    # The unprivileged management daemon may use HTTPS only through WAN paths\n")
	if cfg.WAN.Interface != "" {
		buf.WriteString(fmt.Sprintf("    meta skuid routerd oifname \"%s\" tcp dport 443 accept\n", cfg.WAN.Interface))
	}
	buf.WriteString("    meta skuid routerd oifname \"ppp*\" tcp dport 443 accept\n")
	if cfg.LAN.Interface != "" {
		if broadcast := lanBroadcastAddress(cfg.LAN.CIDR); broadcast != "" {
			buf.WriteString("    # Wake-on-LAN magic packets may only leave via the local LAN segment\n")
			buf.WriteString(fmt.Sprintf("    meta skuid routerd oifname \"%s\" ip daddr %s udp dport 9 accept\n", cfg.LAN.Interface, broadcast))
		}
	}
	if cfg.Cloudflare.DDNSEnabled {
		buf.WriteString("    # DDNS HTTPS is confined to WAN paths\n")
		if cfg.WAN.Interface != "" {
			buf.WriteString(fmt.Sprintf("    meta skuid root oifname \"%s\" tcp dport 443 accept\n", cfg.WAN.Interface))
			buf.WriteString(fmt.Sprintf("    meta skuid inadyn oifname \"%s\" tcp dport 443 accept\n", cfg.WAN.Interface))
		}
		buf.WriteString("    meta skuid root oifname \"ppp*\" tcp dport 443 accept\n")
		buf.WriteString("    meta skuid inadyn oifname \"ppp*\" tcp dport 443 accept\n")
	}
	if cfg.SquidProxy.Enabled {
		buf.WriteString("    # Squid is an Internet-only egress proxy; no private/tunnel interface is a valid destination\n")
		if cfg.WAN.Interface != "" {
			buf.WriteString(fmt.Sprintf("    meta skuid squid oifname \"%s\" tcp dport { 80, 443 } accept\n", cfg.WAN.Interface))
		}
		buf.WriteString("    meta skuid squid oifname \"ppp*\" tcp dport { 80, 443 } accept\n")
	}
	buf.WriteString("    counter drop\n")
	buf.WriteString("  }\n\n")

	// Prerouting (tunnel-scoped DNAT)
	writeTunnelPortForwards(&buf, cfg)

	// Postrouting (Masquerade NAT)
	buf.WriteString("  chain postrouting {\n")
	buf.WriteString("    type nat hook postrouting priority srcnat; policy accept;\n\n")
	buf.WriteString("    # Masquerade outgoing LAN traffic on WAN interfaces (ppp0 / ethernet WAN)\n")
	if cfg.WAN.Enabled {
		buf.WriteString("    oifname \"ppp*\" masquerade\n")
		buf.WriteString(fmt.Sprintf("    oifname \"%s\" masquerade\n", cfg.WAN.Interface))
	}
	writeWGClientMasquerade(&buf, cfg)
	buf.WriteString("  }\n")

	buf.WriteString("}\n")

	return buf.String(), nil
}
