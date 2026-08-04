package config

import (
	"encoding/base64"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var (
	interfaceNamePattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,14}$`)
	hostnamePattern              = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	domainPattern                = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	safeNamePattern              = regexp.MustCompile(`^[\pL\pN][\pL\pN ._()/-]{0,63}$`)
	credentialNamePattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	cloudflareTokenPattern       = regexp.MustCompile(`^[A-Za-z0-9_-]{20,256}$`)
	wireGuardEndpointHostPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
)

func hasUnsafeControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func validInterfaceName(value string) bool {
	return interfaceNamePattern.MatchString(value)
}

func parseIPv4(value string) net.IP {
	ip := net.ParseIP(value)
	if ip == nil {
		return nil
	}
	return ip.To4()
}

func compareIPv4(a, b net.IP) int {
	ai, _ := strconv.ParseUint(fmt.Sprintf("%d%03d%03d%03d", a[0], a[1], a[2], a[3]), 10, 64)
	bi, _ := strconv.ParseUint(fmt.Sprintf("%d%03d%03d%03d", b[0], b[1], b[2], b[3]), 10, 64)
	switch {
	case ai < bi:
		return -1
	case ai > bi:
		return 1
	default:
		return 0
	}
}

func appendFieldError(errs *ValidationErrors, field, message string) {
	*errs = append(*errs, ValidationError{Field: field, Message: message})
}

// networksOverlap reports whether two IPv4/IPv6 networks share any address.
// Both networks must be canonical *net.IPNet values (parse via net.ParseCIDR).
func networksOverlap(a, b *net.IPNet) bool {
	return a.Contains(b.IP) || b.Contains(a.IP)
}

func validWireGuardKey(value string) bool {
	decoded, err := base64.StdEncoding.DecodeString(value)
	return err == nil && len(decoded) == 32
}

// ValidationError contains field-specific error messages.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidationErrors is a list of ValidationErrors implementing error interface.
type ValidationErrors []ValidationError

func (ve ValidationErrors) Error() string {
	var msgs []string
	for _, err := range ve {
		msgs = append(msgs, err.Error())
	}
	return strings.Join(msgs, "; ")
}

// Validate checks the complete SystemConfig for syntax and cross-field invariant errors.
func (c *SystemConfig) Validate() error {
	var errs ValidationErrors

	if !hostnamePattern.MatchString(c.System.Hostname) {
		appendFieldError(&errs, "system.hostname", "must be a valid RFC-compatible hostname label")
	}
	if !domainPattern.MatchString(c.System.Domain) || hasUnsafeControl(c.System.Domain) {
		appendFieldError(&errs, "system.domain", "must be a valid local DNS domain")
	}
	if c.System.HTTPSPort < 1024 || c.System.HTTPSPort > 65535 {
		appendFieldError(&errs, "system.https_port", "must be an unprivileged port between 1024 and 65535")
	}
	if !c.System.HTTPSEnabled {
		appendFieldError(&errs, "system.https_enabled", "management HTTPS cannot be disabled")
	}
	if c.System.ManagementAccess != "" &&
		c.System.ManagementAccess != "lan_and_wireguard" &&
		c.System.ManagementAccess != "wireguard_only" {
		appendFieldError(&errs, "system.management_access", "must be lan_and_wireguard or wireguard_only")
	}
	if c.System.ManagementAccess == "wireguard_only" {
		if !c.WireGuard.Enabled {
			appendFieldError(&errs, "system.management_access", "requires WireGuard to be enabled")
		}
		enabledPeers := 0
		for _, peer := range c.WireGuard.Peers {
			if peer.Enabled {
				enabledPeers++
			}
		}
		if enabledPeers == 0 {
			appendFieldError(&errs, "system.management_access", "requires at least one enabled WireGuard peer")
		}
	}

	if !validInterfaceName(c.WAN.Interface) {
		appendFieldError(&errs, "wan.interface", "must be a valid Linux interface name of at most 15 characters")
	}
	if !validInterfaceName(c.LAN.Interface) {
		appendFieldError(&errs, "lan.interface", "must be a valid Linux interface name of at most 15 characters")
	}
	if c.WAN.Interface == c.LAN.Interface {
		appendFieldError(&errs, "wan.interface", "WAN interface cannot be the same as LAN interface")
	}

	if c.WAN.MTU < 1280 || c.WAN.MTU > 1500 {
		appendFieldError(&errs, "wan.mtu", "must be between 1280 and 1500")
	}
	if hasUnsafeControl(c.WAN.Username) || len(c.WAN.Username) > 255 {
		appendFieldError(&errs, "wan.username", "contains forbidden control characters or is too long")
	}
	if hasUnsafeControl(c.WAN.Password) || len(c.WAN.Password) > 1024 {
		appendFieldError(&errs, "wan.password", "contains forbidden control characters or is too long")
	}
	if c.WAN.Enabled {
		if strings.TrimSpace(c.WAN.Username) == "" {
			appendFieldError(&errs, "wan.username", "is required when PPPoE is enabled")
		}
		if c.WAN.Password == "" || c.WAN.Password == "[REDACTED]" {
			appendFieldError(&errs, "wan.password", "a new PPPoE password is required when none is stored")
		}
	}

	lanIP := parseIPv4(c.LAN.IPAddress)
	if lanIP == nil {
		appendFieldError(&errs, "lan.ip_address", "must be a valid IPv4 address")
	}

	var lanNetwork *net.IPNet
	if c.LAN.CIDR != "" {
		cidrIP, network, err := net.ParseCIDR(c.LAN.CIDR)
		if err != nil {
			appendFieldError(&errs, "lan.cidr", "must be valid IPv4 CIDR notation")
		} else if cidrIP.To4() == nil {
			appendFieldError(&errs, "lan.cidr", "must contain an IPv4 address")
		} else {
			lanNetwork = network
			if lanIP != nil && !network.Contains(lanIP) {
				appendFieldError(&errs, "lan.cidr", "must contain the configured LAN address")
			}
			if lanIP != nil && !cidrIP.Equal(lanIP) {
				appendFieldError(&errs, "lan.cidr", "address must exactly match lan.ip_address")
			}
			expectedMask := net.IP(network.Mask).String()
			if c.LAN.Netmask != expectedMask {
				appendFieldError(&errs, "lan.netmask", "must match the CIDR prefix")
			}
		}
	} else {
		appendFieldError(&errs, "lan.cidr", "is required")
	}

	if c.DHCP.Enabled {
		startIP := parseIPv4(c.DHCP.RangeStart)
		endIP := parseIPv4(c.DHCP.RangeEnd)
		if startIP == nil {
			appendFieldError(&errs, "dhcp.range_start", "must be a valid IPv4 address")
		}
		if endIP == nil {
			appendFieldError(&errs, "dhcp.range_end", "must be a valid IPv4 address")
		}
		if startIP != nil && endIP != nil {
			if compareIPv4(startIP, endIP) > 0 {
				appendFieldError(&errs, "dhcp.range", "range start must not be after range end")
			}
			if lanNetwork != nil && (!lanNetwork.Contains(startIP) || !lanNetwork.Contains(endIP)) {
				appendFieldError(&errs, "dhcp.range", "must be contained in the LAN subnet")
			}
			if lanNetwork != nil {
				networkIP := lanNetwork.IP.To4()
				broadcast := make(net.IP, len(networkIP))
				for i := range networkIP {
					broadcast[i] = networkIP[i] | ^lanNetwork.Mask[i]
				}
				if startIP.Equal(networkIP) || endIP.Equal(broadcast) {
					appendFieldError(&errs, "dhcp.range", "cannot include the network or broadcast address")
				}
			}
			if lanIP != nil && compareIPv4(startIP, lanIP) <= 0 && compareIPv4(lanIP, endIP) <= 0 {
				appendFieldError(&errs, "dhcp.range", "cannot contain the LAN gateway address")
			}
		}
		if len(c.DHCP.DNSServers) < 1 || len(c.DHCP.DNSServers) > 3 {
			appendFieldError(&errs, "dhcp.dns_servers", "must contain between one and three explicit upstream resolvers")
		}
		leaseTime, err := time.ParseDuration(c.DHCP.LeaseTime)
		if err != nil || leaseTime < time.Minute || leaseTime > 7*24*time.Hour {
			appendFieldError(&errs, "dhcp.lease_time", "must be a duration between 1m and 168h")
		}
		for i, dns := range c.DHCP.DNSServers {
			if parseIPv4(dns) == nil {
				appendFieldError(&errs, fmt.Sprintf("dhcp.dns_servers[%d]", i), "must be a valid IPv4 address")
			}
		}
		seenMAC := make(map[string]struct{})
		seenLeaseIP := make(map[string]struct{})
		for i, lease := range c.DHCP.StaticLeases {
			mac, err := net.ParseMAC(lease.MAC)
			if err != nil || len(mac) != 6 {
				appendFieldError(&errs, fmt.Sprintf("dhcp.static_leases[%d].mac", i), "must be a valid 48-bit MAC address")
			} else {
				macKey := mac.String()
				if _, exists := seenMAC[macKey]; exists {
					appendFieldError(&errs, fmt.Sprintf("dhcp.static_leases[%d].mac", i), "duplicates another static lease")
				}
				seenMAC[macKey] = struct{}{}
			}
			ip := parseIPv4(lease.IPAddress)
			if ip == nil || (lanNetwork != nil && !lanNetwork.Contains(ip)) {
				appendFieldError(&errs, fmt.Sprintf("dhcp.static_leases[%d].ip_address", i), "must be a valid address in the LAN subnet")
			} else if lanNetwork != nil {
				networkIP := lanNetwork.IP.To4()
				broadcast := make(net.IP, len(networkIP))
				for j := range networkIP {
					broadcast[j] = networkIP[j] | ^lanNetwork.Mask[j]
				}
				switch {
				case lanIP != nil && ip.Equal(lanIP):
					appendFieldError(&errs, fmt.Sprintf("dhcp.static_leases[%d].ip_address", i), "cannot use the router LAN address")
				case ip.Equal(networkIP):
					appendFieldError(&errs, fmt.Sprintf("dhcp.static_leases[%d].ip_address", i), "cannot use the network address")
				case ip.Equal(broadcast):
					appendFieldError(&errs, fmt.Sprintf("dhcp.static_leases[%d].ip_address", i), "cannot use the broadcast address")
				case startIP != nil && endIP != nil && compareIPv4(ip, startIP) >= 0 && compareIPv4(ip, endIP) <= 0:
					appendFieldError(&errs, fmt.Sprintf("dhcp.static_leases[%d].ip_address", i), "must not overlap the dynamic DHCP pool")
				}
			}
			ipKey := ""
			if ip != nil {
				ipKey = ip.String()
			}
			if _, exists := seenLeaseIP[ipKey]; ipKey != "" && exists {
				appendFieldError(&errs, fmt.Sprintf("dhcp.static_leases[%d].ip_address", i), "duplicates another static lease")
			}
			if ipKey != "" {
				seenLeaseIP[ipKey] = struct{}{}
			}
			if lease.Hostname != "" && !hostnamePattern.MatchString(lease.Hostname) {
				appendFieldError(&errs, fmt.Sprintf("dhcp.static_leases[%d].hostname", i), "must be a valid hostname")
			}
		}
	}

	seenForward := make(map[string]struct{})
	if c.Firewall.DefaultWANInputPolicy != "deny" {
		appendFieldError(&errs, "firewall.default_wan_input_policy", "must remain deny")
	}
	if c.Firewall.WANIngressMode != "" && c.Firewall.WANIngressMode != "wireguard_only" {
		appendFieldError(&errs, "firewall.wan_ingress_mode", "must remain wireguard_only")
	}
	if !c.Firewall.StatefulFirewall {
		appendFieldError(&errs, "firewall.stateful_firewall", "cannot be disabled")
	}
	for i, pf := range c.Firewall.PortForwards {
		if pf.Enabled {
			appendFieldError(&errs, fmt.Sprintf("firewall.port_forwards[%d].enabled", i), "WAN port forwards are forbidden; WireGuard is the only allowed external entry point")
		}
		if pf.Enabled && !c.WAN.Enabled {
			appendFieldError(&errs, fmt.Sprintf("firewall.port_forwards[%d].enabled", i), "cannot be enabled while WAN is disabled")
		}
		if !safeNamePattern.MatchString(pf.Name) || hasUnsafeControl(pf.Name) {
			appendFieldError(&errs, fmt.Sprintf("firewall.port_forwards[%d].name", i), "contains unsupported characters")
		}
		protocol := strings.ToLower(pf.Protocol)
		if protocol != "tcp" && protocol != "udp" && protocol != "both" {
			appendFieldError(&errs, fmt.Sprintf("firewall.port_forwards[%d].protocol", i), "must be tcp, udp, or both")
		}
		if pf.ExternalPort < 1 || pf.ExternalPort > 65535 {
			appendFieldError(&errs, fmt.Sprintf("firewall.port_forwards[%d].external_port", i), "must be between 1 and 65535")
		}
		if pf.InternalPort < 1 || pf.InternalPort > 65535 {
			appendFieldError(&errs, fmt.Sprintf("firewall.port_forwards[%d].internal_port", i), "must be between 1 and 65535")
		}
		targetIP := parseIPv4(pf.InternalIP)
		if targetIP == nil || (lanNetwork != nil && !lanNetwork.Contains(targetIP)) {
			appendFieldError(&errs, fmt.Sprintf("firewall.port_forwards[%d].internal_ip", i), "must be a valid address in the LAN subnet")
		}
		if pf.ExternalPort == c.System.HTTPSPort || pf.ExternalPort == 8443 {
			appendFieldError(&errs, fmt.Sprintf("firewall.port_forwards[%d].external_port", i), "cannot expose a router management port")
		}
		protocols := []string{protocol}
		if protocol == "both" {
			protocols = []string{"tcp", "udp"}
		}
		for _, itemProtocol := range protocols {
			key := fmt.Sprintf("%s/%d", itemProtocol, pf.ExternalPort)
			if _, exists := seenForward[key]; exists {
				appendFieldError(&errs, fmt.Sprintf("firewall.port_forwards[%d]", i), "duplicates an external protocol/port")
			}
			seenForward[key] = struct{}{}
		}
	}

	for i, rule := range c.Firewall.CustomRules {
		if !safeNamePattern.MatchString(rule.Name) || hasUnsafeControl(rule.Name) {
			appendFieldError(&errs, fmt.Sprintf("firewall.custom_rules[%d].name", i), "contains unsupported characters")
		}
		if rule.Action != "allow" && rule.Action != "deny" {
			appendFieldError(&errs, fmt.Sprintf("firewall.custom_rules[%d].action", i), "must be allow or deny")
		}
		if rule.Direction != "input" && rule.Direction != "forward" {
			appendFieldError(&errs, fmt.Sprintf("firewall.custom_rules[%d].direction", i), "must be input or forward")
		}
		switch rule.Protocol {
		case "tcp", "udp":
			if rule.DstPort < 1 || rule.DstPort > 65535 {
				appendFieldError(&errs, fmt.Sprintf("firewall.custom_rules[%d].dst_port", i), "is required for TCP/UDP and must be between 1 and 65535")
			}
		case "icmp", "any":
			if rule.DstPort != 0 {
				appendFieldError(&errs, fmt.Sprintf("firewall.custom_rules[%d].dst_port", i), "must be zero for ICMP/any")
			}
		default:
			appendFieldError(&errs, fmt.Sprintf("firewall.custom_rules[%d].protocol", i), "must be tcp, udp, icmp, or any")
		}
		if rule.SrcIP != "" {
			if ip := parseIPv4(rule.SrcIP); ip == nil || (lanNetwork != nil && !lanNetwork.Contains(ip)) {
				appendFieldError(&errs, fmt.Sprintf("firewall.custom_rules[%d].src_ip", i), "must be a valid LAN source address")
			}
		}
		if c.System.ManagementAccess == "wireguard_only" && rule.Enabled &&
			rule.Direction == "input" && rule.Action == "allow" {
			appendFieldError(&errs, fmt.Sprintf("firewall.custom_rules[%d]", i), "LAN input allow rules are forbidden in wireguard_only management mode")
		}
	}

	wgNetworkCIDR := ""
	var wgNetwork *net.IPNet
	if c.WireGuard.Enabled {
		if _, wgNet, err := net.ParseCIDR(c.WireGuard.Address); err == nil {
			wgNetworkCIDR = wgNet.String()
			wgNetwork = wgNet
		}
	}
	seenExtraInterfaces := make(map[string]struct{})
	var extraNetworks []*net.IPNet
	for i, lan := range c.Firewall.ExtraLANs {
		if !lan.Enabled {
			continue
		}
		prefix := fmt.Sprintf("firewall.extra_lans[%d]", i)
		if !safeNamePattern.MatchString(lan.Name) || hasUnsafeControl(lan.Name) || len(lan.Name) > 64 {
			appendFieldError(&errs, prefix+".name", "must be 1-64 safe characters and contain no control characters")
		}
		if hasUnsafeControl(lan.ID) || len(lan.ID) > 64 {
			appendFieldError(&errs, prefix+".id", "must contain no control characters and be at most 64 characters")
		}
		if !validInterfaceName(lan.Interface) || strings.HasPrefix(lan.Interface, "ppp") {
			appendFieldError(&errs, prefix+".interface", "must be a valid non-PPP interface name")
		} else {
			switch {
			case lan.Interface == c.WAN.Interface:
				appendFieldError(&errs, prefix+".interface", "must not reuse the WAN interface")
			case lan.Interface == c.LAN.Interface || lan.Interface == WiFiBridgeInterface:
				appendFieldError(&errs, prefix+".interface", "must not reuse the LAN interface")
			case c.WireGuard.Enabled && lan.Interface == c.WireGuard.Interface:
				appendFieldError(&errs, prefix+".interface", "must not reuse the WireGuard interface")
			case c.WGClient.Enabled && lan.Interface == c.WGClient.Interface:
				appendFieldError(&errs, prefix+".interface", "must not reuse the WireGuard client interface")
			case c.WiFi.Enabled && (lan.Interface == c.WiFi.Interface || lan.Interface == WiFiBridgeInterface):
				appendFieldError(&errs, prefix+".interface", "must not reuse the Wi-Fi radio or its bridge")
			}
			if _, dup := seenExtraInterfaces[lan.Interface]; dup {
				appendFieldError(&errs, prefix+".interface", "duplicates another extra LAN interface")
			}
			seenExtraInterfaces[lan.Interface] = struct{}{}
		}
		_, extraNet, err := net.ParseCIDR(lan.CIDR)
		if err != nil || extraNet.IP.To4() == nil {
			appendFieldError(&errs, prefix+".cidr", "must be valid IPv4 CIDR notation")
		} else {
			if lanNetwork != nil && networksOverlap(lanNetwork, extraNet) {
				appendFieldError(&errs, prefix+".cidr", "must not overlap the LAN subnet")
			}
			if wgNetwork != nil && networksOverlap(wgNetwork, extraNet) {
				appendFieldError(&errs, prefix+".cidr", "must not overlap the WireGuard subnet")
			}
			if c.WGClient.Address != "" {
				if _, wgClientNet, cidrErr := net.ParseCIDR(c.WGClient.Address); cidrErr == nil && networksOverlap(wgClientNet, extraNet) {
					appendFieldError(&errs, prefix+".cidr", "must not overlap the WireGuard client subnet")
				}
			}
			for _, previous := range extraNetworks {
				if networksOverlap(previous, extraNet) {
					appendFieldError(&errs, prefix+".cidr", "must not overlap another extra LAN subnet")
				}
			}
			extraNetworks = append(extraNetworks, extraNet)

			if lan.RouterAddress == "" {
				appendFieldError(&errs, prefix+".router_address", "is required for an enabled extra LAN so the router can reconstruct the segment")
			} else if routerIP, routerNet, cidrErr := net.ParseCIDR(lan.RouterAddress); cidrErr != nil || routerIP.To4() == nil {
				appendFieldError(&errs, prefix+".router_address", "must be a valid IPv4 interface CIDR")
			} else if extraNet.Contains(routerIP) {
				networkIP := extraNet.IP.To4()
				broadcast := make(net.IP, len(networkIP))
				for j := range networkIP {
					broadcast[j] = networkIP[j] | ^extraNet.Mask[j]
				}
				switch {
				case routerIP.Equal(networkIP):
					appendFieldError(&errs, prefix+".router_address", "cannot use the network address")
				case routerIP.Equal(broadcast):
					appendFieldError(&errs, prefix+".router_address", "cannot use the broadcast address")
				case routerNet.String() != extraNet.String():
					appendFieldError(&errs, prefix+".router_address", "must be contained in the extra LAN subnet")
				}
			} else {
				appendFieldError(&errs, prefix+".router_address", "must be contained in the extra LAN subnet")
			}
		}
		if lan.DstIP == "" || parseIPv4(lan.DstIP) == nil {
			appendFieldError(&errs, prefix+".dst_ip", "must be a valid IPv4 address")
		} else if extraNet != nil && !extraNet.Contains(parseIPv4(lan.DstIP)) {
			appendFieldError(&errs, prefix+".dst_ip", "must be inside the extra LAN subnet")
		} else if extraNet != nil {
			networkIP := extraNet.IP.To4()
			broadcast := make(net.IP, len(networkIP))
			for j := range networkIP {
				broadcast[j] = networkIP[j] | ^extraNet.Mask[j]
			}
			dst := parseIPv4(lan.DstIP)
			if dst.Equal(networkIP) || dst.Equal(broadcast) {
				appendFieldError(&errs, prefix+".dst_ip", "cannot use the network or broadcast address")
			}
			if lan.RouterAddress != "" {
				if routerIP, _, cidrErr := net.ParseCIDR(lan.RouterAddress); cidrErr == nil && dst.Equal(routerIP) {
					appendFieldError(&errs, prefix+".dst_ip", "cannot be the router address")
				}
			}
		}
		if lan.DstPort < 1 || lan.DstPort > 65535 {
			appendFieldError(&errs, prefix+".dst_port", "must be between 1 and 65535")
		}
		switch lan.Protocol {
		case "", "tcp", "udp":
		default:
			appendFieldError(&errs, prefix+".protocol", "must be tcp, udp, or empty")
		}
		if len(lan.AllowFrom) == 0 {
			appendFieldError(&errs, prefix+".allow_from", "must list at least one source CIDR")
		}
		seenSrc := make(map[string]struct{})
		for _, src := range lan.AllowFrom {
			src = strings.TrimSpace(src)
			if src != c.LAN.CIDR && (wgNetworkCIDR == "" || src != wgNetworkCIDR) {
				appendFieldError(&errs, prefix+".allow_from", "only the LAN and WireGuard networks may reach an extra LAN")
				break
			}
			if _, _, cidrErr := net.ParseCIDR(src); cidrErr != nil {
				appendFieldError(&errs, prefix+".allow_from", "must be canonical CIDR notation")
				break
			}
			if _, dup := seenSrc[src]; dup {
				appendFieldError(&errs, prefix+".allow_from", "duplicates a source CIDR")
				break
			}
			seenSrc[src] = struct{}{}
		}
	}

	if c.WireGuard.Enabled {
		if !c.WAN.Enabled {
			appendFieldError(&errs, "wireguard.enabled", "requires an enabled WAN connection")
		}
		if c.WireGuard.Interface != "wg0" {
			appendFieldError(&errs, "wireguard.interface", "must be wg0 in this appliance version")
		}
		if !validWireGuardKey(c.WireGuard.PrivateKey) {
			appendFieldError(&errs, "wireguard.private_key", "must be a valid 32-byte WireGuard key")
		}
		if c.WireGuard.ListenPort < 1024 || c.WireGuard.ListenPort > 65535 ||
			c.WireGuard.ListenPort == c.System.HTTPSPort {
			appendFieldError(&errs, "wireguard.listen_port", "must be a non-management port between 1024 and 65535")
		}
		wgIP, wgNetwork, err := net.ParseCIDR(c.WireGuard.Address)
		if err != nil || wgIP.To4() == nil || !wgIP.Equal(net.ParseIP(strings.Split(c.WireGuard.Address, "/")[0])) {
			appendFieldError(&errs, "wireguard.address", "must be a valid IPv4 interface CIDR")
		} else if lanNetwork != nil && (lanNetwork.Contains(wgIP) || wgNetwork.Contains(lanIP)) {
			appendFieldError(&errs, "wireguard.address", "must not overlap the LAN subnet")
		}
		seenPeerIPs := make(map[string]struct{})
		for i, peer := range c.WireGuard.Peers {
			if !peer.Enabled {
				continue
			}
			if !safeNamePattern.MatchString(peer.Name) {
				appendFieldError(&errs, fmt.Sprintf("wireguard.peers[%d].name", i), "contains unsupported characters")
			}
			if !validWireGuardKey(peer.PublicKey) {
				appendFieldError(&errs, fmt.Sprintf("wireguard.peers[%d].public_key", i), "must be a valid 32-byte WireGuard key")
			}
			if peer.PresharedKey != "" && !validWireGuardKey(peer.PresharedKey) {
				appendFieldError(&errs, fmt.Sprintf("wireguard.peers[%d].preshared_key", i), "must be a valid 32-byte WireGuard key")
			}
			if len(peer.AllowedIPs) != 1 {
				appendFieldError(&errs, fmt.Sprintf("wireguard.peers[%d].allowed_ips", i), "must contain exactly one IPv4 /32 address")
			}
			for j, allowed := range peer.AllowedIPs {
				ip, network, err := net.ParseCIDR(allowed)
				allowedPrefix := 0
				if network != nil {
					allowedPrefix, _ = network.Mask.Size()
				}
				if err != nil || ip.To4() == nil || allowedPrefix != 32 ||
					(wgNetwork != nil && !wgNetwork.Contains(ip)) {
					appendFieldError(&errs, fmt.Sprintf("wireguard.peers[%d].allowed_ips[%d]", i, j), "must be an IPv4 /32 address inside the WireGuard subnet")
					continue
				}
				key := network.String()
				if _, exists := seenPeerIPs[key]; exists {
					appendFieldError(&errs, fmt.Sprintf("wireguard.peers[%d].allowed_ips[%d]", i, j), "duplicates another peer route")
				}
				seenPeerIPs[key] = struct{}{}
			}
			if hasUnsafeControl(peer.Endpoint) || len(peer.Endpoint) > 255 {
				appendFieldError(&errs, fmt.Sprintf("wireguard.peers[%d].endpoint", i), "contains forbidden characters or is too long")
			}
		}
	}

	validateWGClient(c, lanIP, lanNetwork, &errs)

	if c.Cloudflare.DDNSEnabled {
		if !c.WAN.Enabled {
			appendFieldError(&errs, "cloudflare.ddns_enabled", "requires an enabled WAN connection")
		}
		if !domainPattern.MatchString(c.Cloudflare.Domain) || !strings.Contains(c.Cloudflare.Domain, ".") {
			appendFieldError(&errs, "cloudflare.domain", "must be a valid fully qualified DDNS hostname")
		}

		provider := strings.ToLower(strings.TrimSpace(c.Cloudflare.DDNSProvider))
		if provider == "" {
			provider = "cloudflare"
		}
		switch provider {
		case "noip":
			if strings.TrimSpace(c.Cloudflare.DDNSUser) == "" || len(c.Cloudflare.DDNSUser) > 255 || hasUnsafeControl(c.Cloudflare.DDNSUser) {
				appendFieldError(&errs, "cloudflare.ddns_username", "No-IP username or DDNS Key username is required and must not contain control characters")
			}
			if c.Cloudflare.APIToken == "" || c.Cloudflare.APIToken == "[REDACTED]" || len(c.Cloudflare.APIToken) > 1024 || hasUnsafeControl(c.Cloudflare.APIToken) {
				appendFieldError(&errs, "cloudflare.api_token", "No-IP DDNS Key/account password is required and must not contain control characters")
			}
		case "cloudflare":
			if !domainPattern.MatchString(c.Cloudflare.ZoneName) || !strings.Contains(c.Cloudflare.ZoneName, ".") {
				appendFieldError(&errs, "cloudflare.zone_name", "must be the Cloudflare zone name, for example example.com")
			}
			if !cloudflareTokenPattern.MatchString(c.Cloudflare.APIToken) {
				appendFieldError(&errs, "cloudflare.api_token", "must be a valid Cloudflare API token")
			}
		default:
			appendFieldError(&errs, "cloudflare.ddns_provider", "must be noip or cloudflare")
		}
	}
	if c.Cloudflare.TunnelEnabled {
		appendFieldError(&errs, "cloudflare.tunnel_enabled", "is unavailable because WireGuard is the only allowed remote-entry path")
	}

	if c.SquidProxy.Enabled {
		if c.SquidProxy.Port < 1 || c.SquidProxy.Port > 65535 || c.SquidProxy.Port == c.System.HTTPSPort {
			appendFieldError(&errs, "squid_proxy.port", "must be a non-management port between 1 and 65535")
		}
		if hasUnsafeControl(c.SquidProxy.Username) || hasUnsafeControl(c.SquidProxy.Password) {
			appendFieldError(&errs, "squid_proxy", "credentials contain forbidden control characters")
		}
		if !credentialNamePattern.MatchString(c.SquidProxy.Username) {
			appendFieldError(&errs, "squid_proxy.username", "must contain only letters, numbers, dot, underscore, or hyphen")
		}
		if len([]rune(c.SquidProxy.Password)) < 12 || len(c.SquidProxy.Password) > 1024 ||
			c.SquidProxy.Password == "[REDACTED]" {
			appendFieldError(&errs, "squid_proxy.password", "must contain 12-1024 characters")
		}
	}
	for i, item := range c.SquidProxy.RestrictedIPs {
		ip := parseIPv4(item.IPAddress)
		if ip == nil || (lanNetwork != nil && !lanNetwork.Contains(ip)) {
			appendFieldError(&errs, fmt.Sprintf("squid_proxy.restricted_ips[%d].ip_address", i), "must be a valid LAN address")
		}
	}

	errs = append(errs, c.validateDeviceProfiles(lanNetwork)...)
	if c.AdGuard.BlocklistURL != "" {
		appendFieldError(&errs, "adguard.blocklist_url", "external blocklist refresh is unavailable in the hardened pilot; use the built-in global list")
	}

	seenDNSNames := make(map[string]struct{})
	seenDNSIPs := make(map[string]struct{})
	for i, rec := range c.DNS.Records {
		lower := strings.ToLower(strings.TrimSuffix(rec.Name, "."))
		if !domainPattern.MatchString(rec.Name) || hasUnsafeControl(rec.Name) {
			appendFieldError(&errs, fmt.Sprintf("dns.records[%d].name", i), "must be a valid DNS hostname")
		}
		if strings.HasSuffix(lower, ".local") {
			appendFieldError(&errs, fmt.Sprintf("dns.records[%d].name", i), ".local is the mDNS namespace and conflicts with macOS/iOS resolvers; use .home.arpa instead")
		}
		if _, dup := seenDNSNames[lower]; lower != "" && dup {
			appendFieldError(&errs, fmt.Sprintf("dns.records[%d].name", i), "duplicates another record (hostnames are case-insensitive)")
		}
		if lower != "" {
			seenDNSNames[lower] = struct{}{}
		}
		if parseIPv4(rec.IP) == nil {
			appendFieldError(&errs, fmt.Sprintf("dns.records[%d].ip", i), "must be a valid IPv4 address")
		} else {
			ipKey := parseIPv4(rec.IP).String()
			if _, dup := seenDNSIPs[ipKey]; dup {
				appendFieldError(&errs, fmt.Sprintf("dns.records[%d].ip", i), "duplicates another record's address")
			}
			seenDNSIPs[ipKey] = struct{}{}
		}
	}
	domainSuffix := "." + strings.ToLower(strings.TrimSuffix(c.System.Domain, "."))
	for i, lease := range c.DHCP.StaticLeases {
		if lease.Hostname == "" {
			continue
		}
		leaseFQDN := strings.ToLower(lease.Hostname)
		if domainSuffix != "." {
			leaseFQDN += domainSuffix
		}
		if _, collision := seenDNSNames[leaseFQDN]; collision {
			appendFieldError(&errs, fmt.Sprintf("dhcp.static_leases[%d].hostname", i), "conflicts with a static DNS record hostname")
		}
	}

	if c.QoS.Enabled {
		if c.QoS.Algorithm != "cake" && c.QoS.Algorithm != "fq_codel" {
			appendFieldError(&errs, "qos.algorithm", "must be cake or fq_codel")
		}
		if c.QoS.DownloadLimitMbps <= 0 {
			appendFieldError(&errs, "qos.download_limit_mbps", "must be greater than zero")
		}
		if c.QoS.UploadLimitMbps <= 0 {
			appendFieldError(&errs, "qos.upload_limit_mbps", "must be greater than zero")
		}
		if c.QoS.DownloadLimitMbps > 100000 || c.QoS.UploadLimitMbps > 100000 {
			appendFieldError(&errs, "qos", "limits must not exceed 100000 Mbps")
		}
	}

	if c.DHCP.DNSEnabled {
		appendFieldError(&errs, "dhcp.dns_enabled", "DNS-over-HTTPS is unavailable until a packaged and verified local resolver is installed")
	}

	if c.WiFi.Enabled {
		if !validInterfaceName(c.WiFi.Interface) {
			appendFieldError(&errs, "wifi.interface", "must be a valid Linux interface name")
		}
		if c.WiFi.Interface == c.WAN.Interface || c.WiFi.Interface == c.LAN.Interface ||
			c.WiFi.Interface == WiFiBridgeInterface {
			appendFieldError(&errs, "wifi.interface", "must not reuse the WAN or LAN interface")
		}
		if len([]byte(c.WiFi.SSID)) < 1 || len([]byte(c.WiFi.SSID)) > 32 || hasUnsafeControl(c.WiFi.SSID) {
			appendFieldError(&errs, "wifi.ssid", "must contain 1-32 bytes and no control characters")
		}
		if len([]byte(c.WiFi.Passphrase)) < 12 || len([]byte(c.WiFi.Passphrase)) > 63 || hasUnsafeControl(c.WiFi.Passphrase) {
			appendFieldError(&errs, "wifi.passphrase", "must contain 12-63 bytes and no control characters")
		}
		if c.WiFi.Band != "2.4ghz" && c.WiFi.Band != "5ghz" {
			appendFieldError(&errs, "wifi.band", "must be 2.4ghz or 5ghz")
		}
		if c.WiFi.Band == "2.4ghz" && (c.WiFi.Channel < 1 || c.WiFi.Channel > 11) {
			appendFieldError(&errs, "wifi.channel", "must be 1-11 for 2.4 GHz")
		}
		if c.WiFi.Band == "5ghz" &&
			c.WiFi.Channel != 36 && c.WiFi.Channel != 40 &&
			c.WiFi.Channel != 44 && c.WiFi.Channel != 48 {
			appendFieldError(&errs, "wifi.channel", "must be 36, 40, 44, or 48 for the portable 5 GHz profile")
		}
	}

	// Trusted networks gate administrative Web UI/API access. The list must
	// never be empty (an empty list would silently deny every non-loopback
	// client and could lock out administration), and wildcard ranges are
	// rejected because they defeat the purpose of an access boundary.
	if len(c.TrustedNetworks) == 0 {
		appendFieldError(&errs, "trusted_networks", "must contain at least one CIDR network")
	}
	seen := make(map[string]bool, len(c.TrustedNetworks))
	for i, network := range c.TrustedNetworks {
		field := fmt.Sprintf("trusted_networks[%d]", i)
		if hasUnsafeControl(network) {
			appendFieldError(&errs, field, "must not contain control characters")
			continue
		}
		if seen[network] {
			appendFieldError(&errs, field, "must not contain duplicate networks")
			continue
		}
		seen[network] = true
		ip, ipNet, err := net.ParseCIDR(network)
		if err != nil {
			appendFieldError(&errs, field, "must be a valid IPv4 or IPv6 CIDR network")
			continue
		}
		if ones, _ := ipNet.Mask.Size(); ones == 0 || ip.IsUnspecified() {
			appendFieldError(&errs, field, "wildcard networks (0.0.0.0/0, ::/0) are not allowed")
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

// validateWGClient enforces the outbound WireGuard tunnel (wg1) contract:
// a valid key pair, an explicit remote endpoint, and remote networks that do
// not overlap the home LAN or any other local network. Full-tunnel is not
// supported in this version: entries that would capture the default route
// (0.0.0.0/0 and the 0.0.0.0/1, 128.0.0.0/1 half-default tricks, or any
// prefix broader than a /8) are rejected.
func validateWGClient(c *SystemConfig, lanIP net.IP, lanNetwork *net.IPNet, errs *ValidationErrors) {
	if !c.WGClient.Enabled {
		return
	}
	if c.WGClient.Interface != "wg1" {
		appendFieldError(errs, "wg_client.interface", "must be wg1 in this appliance version")
	}
	if !validWireGuardKey(c.WGClient.PrivateKey) {
		appendFieldError(errs, "wg_client.private_key", "must be a valid 32-byte WireGuard key")
	}
	if !validWireGuardKey(c.WGClient.PublicKey) {
		appendFieldError(errs, "wg_client.public_key", "must be a valid 32-byte WireGuard key")
	}
	if c.WGClient.PrivateKey != "" && c.WGClient.PublicKey == c.WGClient.PrivateKey {
		appendFieldError(errs, "wg_client.public_key", "must not match the private key")
	}
	if c.WGClient.PresharedKey != "" && !validWireGuardKey(c.WGClient.PresharedKey) {
		appendFieldError(errs, "wg_client.preshared_key", "must be a valid 32-byte WireGuard key")
	}
	var wgClientAddressNet *net.IPNet
	if c.WGClient.Address != "" {
		clientIP, clientNet, err := net.ParseCIDR(c.WGClient.Address)
		if err != nil || clientIP.To4() == nil {
			appendFieldError(errs, "wg_client.address", "must be a valid IPv4 interface CIDR")
		} else if lanNetwork != nil && networksOverlap(lanNetwork, clientNet) {
			appendFieldError(errs, "wg_client.address", "must not overlap the LAN subnet")
		} else if wgNetwork := wireGuardServerNetwork(c); wgNetwork != nil && networksOverlap(wgNetwork, clientNet) {
			appendFieldError(errs, "wg_client.address", "must not overlap the WireGuard server subnet")
		} else {
			// The outbound tunnel is a point-to-point client; a /32 is the
			// supported model so the router never routes its own segment.
			if ones, _ := clientNet.Mask.Size(); ones != 32 {
				appendFieldError(errs, "wg_client.address", "must be a /32 address in this appliance version")
			}
		}
		wgClientAddressNet = clientNet
	}
	if host, portText, err := net.SplitHostPort(c.WGClient.Endpoint); err != nil || host == "" {
		appendFieldError(errs, "wg_client.endpoint", "must use host:port format")
	} else {
		if parsed := net.ParseIP(host); parsed != nil {
			if parsed.To4() == nil {
				appendFieldError(errs, "wg_client.endpoint", "IPv6 endpoints are unavailable while IPv6 is disabled")
			}
		} else if !wireGuardEndpointHostPattern.MatchString(host) || strings.Contains(host, "..") {
			appendFieldError(errs, "wg_client.endpoint", "contains an invalid hostname")
		}
		port, parseErr := strconv.Atoi(portText)
		if parseErr != nil || port < 1 || port > 65535 {
			appendFieldError(errs, "wg_client.endpoint", "port must be between 1 and 65535")
		}
	}
	if len(c.WGClient.AllowedIPs) == 0 {
		appendFieldError(errs, "wg_client.allowed_ips", "must list at least one remote network")
	}
	seenNets := make(map[string]struct{})
	for i, entry := range c.WGClient.AllowedIPs {
		ip, network, err := net.ParseCIDR(strings.TrimSpace(entry))
		if err != nil || ip.To4() == nil {
			appendFieldError(errs, fmt.Sprintf("wg_client.allowed_ips[%d]", i), "must be a valid IPv4 CIDR network")
			continue
		}
		ones, _ := network.Mask.Size()
		if ones < 8 {
			appendFieldError(errs, fmt.Sprintf("wg_client.allowed_ips[%d]", i),
				"must not be broader than a /8: default-route capture (0.0.0.0/0 and /1 split tricks) is not supported")
		}
		if lanNetwork != nil && networksOverlap(lanNetwork, network) {
			appendFieldError(errs, fmt.Sprintf("wg_client.allowed_ips[%d]", i), "must not overlap the LAN subnet")
		}
		if wgNetwork := wireGuardServerNetwork(c); wgNetwork != nil && networksOverlap(wgNetwork, network) {
			appendFieldError(errs, fmt.Sprintf("wg_client.allowed_ips[%d]", i), "must not overlap the WireGuard server subnet")
		}
		if wgClientAddressNet != nil && networksOverlap(wgClientAddressNet, network) {
			appendFieldError(errs, fmt.Sprintf("wg_client.allowed_ips[%d]", i), "must not overlap the tunnel's own address")
		}
		for _, lan := range c.Firewall.ExtraLANs {
			if !lan.Enabled || lan.CIDR == "" {
				continue
			}
			if _, extraNet, cidrErr := net.ParseCIDR(lan.CIDR); cidrErr == nil && networksOverlap(extraNet, network) {
				appendFieldError(errs, fmt.Sprintf("wg_client.allowed_ips[%d]", i), "must not overlap an extra LAN subnet")
				break
			}
		}
		key := network.String()
		if _, exists := seenNets[key]; exists {
			appendFieldError(errs, fmt.Sprintf("wg_client.allowed_ips[%d]", i), "duplicates another allowed network")
		}
		seenNets[key] = struct{}{}
	}
	if c.WGClient.PersistentKeepalive < 0 || c.WGClient.PersistentKeepalive > 65535 {
		appendFieldError(errs, "wg_client.persistent_keepalive", "must be between 0 and 65535")
	}
	if hasUnsafeControl(c.WGClient.Endpoint) || len(c.WGClient.Endpoint) > 255 {
		appendFieldError(errs, "wg_client.endpoint", "contains forbidden characters or is too long")
	}
}

// wireGuardServerNetwork returns the canonical wg0 network when the server
// tunnel is enabled, or nil otherwise.
func wireGuardServerNetwork(c *SystemConfig) *net.IPNet {
	if !c.WireGuard.Enabled || c.WireGuard.Address == "" {
		return nil
	}
	_, network, err := net.ParseCIDR(c.WireGuard.Address)
	if err != nil {
		return nil
	}
	return network
}
