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
	interfaceNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,14}$`)
	hostnamePattern        = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	domainPattern          = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	safeNamePattern        = regexp.MustCompile(`^[\pL\pN][\pL\pN ._()/-]{0,63}$`)
	credentialNamePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	cloudflareTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{20,256}$`)
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
			}
			macKey := strings.ToLower(lease.MAC)
			if _, exists := seenMAC[macKey]; exists {
				appendFieldError(&errs, fmt.Sprintf("dhcp.static_leases[%d].mac", i), "duplicates another static lease")
			}
			seenMAC[macKey] = struct{}{}
			ip := parseIPv4(lease.IPAddress)
			if ip == nil || (lanNetwork != nil && !lanNetwork.Contains(ip)) {
				appendFieldError(&errs, fmt.Sprintf("dhcp.static_leases[%d].ip_address", i), "must be a valid address in the LAN subnet")
			}
			if _, exists := seenLeaseIP[lease.IPAddress]; exists {
				appendFieldError(&errs, fmt.Sprintf("dhcp.static_leases[%d].ip_address", i), "duplicates another static lease")
			}
			seenLeaseIP[lease.IPAddress] = struct{}{}
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
			if len(peer.AllowedIPs) == 0 || len(peer.AllowedIPs) > 16 {
				appendFieldError(&errs, fmt.Sprintf("wireguard.peers[%d].allowed_ips", i), "must contain 1-16 CIDR entries")
			}
			for j, allowed := range peer.AllowedIPs {
				ip, network, err := net.ParseCIDR(allowed)
				allowedPrefix, wgPrefix := 0, 0
				if network != nil {
					allowedPrefix, _ = network.Mask.Size()
				}
				if wgNetwork != nil {
					wgPrefix, _ = wgNetwork.Mask.Size()
				}
				if err != nil || ip.To4() == nil ||
					(wgNetwork != nil && (!wgNetwork.Contains(ip) || allowedPrefix < wgPrefix)) {
					appendFieldError(&errs, fmt.Sprintf("wireguard.peers[%d].allowed_ips[%d]", i, j), "must be an IPv4 CIDR inside the WireGuard subnet")
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

	if c.Cloudflare.DDNSEnabled {
		if !c.WAN.Enabled {
			appendFieldError(&errs, "cloudflare.ddns_enabled", "requires an enabled WAN connection")
		}
		if !domainPattern.MatchString(c.Cloudflare.Domain) || !strings.Contains(c.Cloudflare.Domain, ".") {
			appendFieldError(&errs, "cloudflare.domain", "must be a valid fully qualified domain")
		}
		if !domainPattern.MatchString(c.Cloudflare.ZoneName) || !strings.Contains(c.Cloudflare.ZoneName, ".") {
			appendFieldError(&errs, "cloudflare.zone_name", "must be the Cloudflare zone name, for example example.com")
		}
		if !cloudflareTokenPattern.MatchString(c.Cloudflare.APIToken) {
			appendFieldError(&errs, "cloudflare.api_token", "must be a valid API token")
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
		if len([]rune(c.SquidProxy.Password)) < 15 || len(c.SquidProxy.Password) > 1024 ||
			c.SquidProxy.Password == "[REDACTED]" {
			appendFieldError(&errs, "squid_proxy.password", "must contain 15-1024 characters")
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

	if len(errs) > 0 {
		return errs
	}
	return nil
}
