package services

import (
	"bytes"
	"fmt"
	"net"
	"strings"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

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

	// Input Chain
	buf.WriteString("  chain input {\n")
	buf.WriteString("    type filter hook input priority filter; policy drop;\n\n")
	buf.WriteString("    # Allow loopback\n")
	buf.WriteString("    iifname \"lo\" accept\n\n")

	if cfg.WAN.Interface != "" {
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
	writeWGClientForwardRules(&buf, cfg)
	buf.WriteString("  }\n\n")

	// Output Chain
	buf.WriteString("  chain output {\n")
	buf.WriteString("    type filter hook output priority filter; policy drop;\n\n")
	buf.WriteString("    # The appliance itself has a narrow egress allowlist\n")
	buf.WriteString("    oifname \"lo\" accept\n")
	if cfg.WAN.Interface != "" {
		buf.WriteString(fmt.Sprintf("    # Never leak private, loopback, CGNAT, or multicast source addresses to WAN (%s)\n", cfg.WAN.Interface))
		buf.WriteString(fmt.Sprintf("    oifname \"%s\" ip saddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.0/8, 0.0.0.0/8, 100.64.0.0/10, 224.0.0.0/4 } drop\n", cfg.WAN.Interface))
		buf.WriteString("    oifname \"ppp*\" ip saddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.0/8, 0.0.0.0/8, 100.64.0.0/10, 224.0.0.0/4 } drop\n")
	}
	buf.WriteString("    ct state invalid drop\n")

	// A pre-existing Squid connection to a private segment must be cut when
	// proxy isolation is enabled/changed; these UID+zone denies deliberately
	// precede established/related acceptance.
	if cfg.SquidProxy.Enabled {
		if cfg.LAN.Interface != "" {
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
