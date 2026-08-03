package config

import (
	"net"
	"strings"
)

// IsTrustedClientAddress reports whether the source address encoded in a TCP
// peer string belongs to one of the trusted management networks, or is
// loopback. Loopback is always trusted so local services and diagnostics are
// never locked out. The check is fail-safe: any address that cannot be parsed
// reliably is denied, never allowed.
func (c SystemConfig) IsTrustedClientAddress(remoteAddr string) bool {
	ip := parseRemoteIP(remoteAddr)
	if ip == nil {
		return false
	}
	// IPv4-mapped IPv6 (::ffff:192.168.1.2) must match the IPv4 networks.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() {
		return true
	}
	for _, network := range c.TrustedNetworks {
		_, ipNet, err := net.ParseCIDR(network)
		if err != nil {
			continue
		}
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

// parseRemoteIP extracts the host portion of a TCP peer address. Malformed
// addresses yield nil, which callers must treat as deny.
func parseRemoteIP(remoteAddr string) net.IP {
	if remoteAddr == "" {
		return nil
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return net.ParseIP(host)
	}
	// RemoteAddr without a port (e.g. some proxies or tests): accept only if
	// the whole string parses as a bare IP, otherwise deny.
	return net.ParseIP(strings.TrimSpace(remoteAddr))
}
