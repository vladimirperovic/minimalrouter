package config

import (
	"fmt"
	"net"
	"strings"
)

func validDNSNameStrict(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 253 || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func networkContainedIn(candidate, zone *net.IPNet) bool {
	if candidate == nil || zone == nil || candidate.IP.To4() == nil || zone.IP.To4() == nil {
		return false
	}
	candidateOnes, candidateBits := candidate.Mask.Size()
	zoneOnes, zoneBits := zone.Mask.Size()
	return candidateBits == 32 && zoneBits == 32 && candidateOnes >= zoneOnes && zone.Contains(candidate.IP)
}

func isPrivateSiteNetwork(network *net.IPNet) bool {
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		_, zone, _ := net.ParseCIDR(cidr)
		if networkContainedIn(network, zone) {
			return true
		}
	}
	return false
}

// ValidateScenarioSafety contains cross-component invariants that exist to
// protect management continuity and runtime behavior rather than syntax. It is
// called by the control plane and again from security-sensitive generators so
// a future API path cannot silently bypass appliance safety boundaries.
func (c *SystemConfig) ValidateScenarioSafety() error {
	var errs ValidationErrors

	// Regex-only hostname checks are insufficient: strings such as a..b or
	// labels ending in '-' can satisfy a broad character class but are not
	// valid DNS names and can later break dnsmasq/TLS behavior.
	if !validDNSNameStrict(c.System.Domain) {
		appendFieldError(&errs, "system.domain", "must contain valid DNS labels (1-63 alphanumeric/hyphen characters, no empty labels or edge hyphens)")
	}
	for i, rec := range c.DNS.Records {
		if !validDNSNameStrict(rec.Name) {
			appendFieldError(&errs, fmt.Sprintf("dns.records[%d].name", i), "must contain valid DNS labels")
		}
	}

	// An enabled outbound tunnel must own one explicit point-to-point address.
	// Treating an empty address as optional creates a route-only interface whose
	// behavior depends on remote topology and breaks deterministic verification.
	if c.WGClient.Enabled {
		address := strings.TrimSpace(c.WGClient.Address)
		ip, network, err := net.ParseCIDR(address)
		if address == "" || err != nil || ip.To4() == nil {
			appendFieldError(&errs, "wg_client.address", "is required and must be a valid IPv4 /32 when the outbound tunnel is enabled")
		} else if ones, _ := network.Mask.Size(); ones != 32 {
			appendFieldError(&errs, "wg_client.address", "must be a /32 address in this appliance version")
		}

		// This appliance version deliberately implements wg1 as a private
		// site-to-site tunnel, not arbitrary public policy routing. Constraining
		// AllowedIPs to RFC1918 networks prevents a hostname peer's public
		// endpoint, public DNS, update traffic, or other control-plane egress from
		// ever being captured by a broad wg1 route.
		for i, entry := range c.WGClient.AllowedIPs {
			_, remote, parseErr := net.ParseCIDR(strings.TrimSpace(entry))
			if parseErr == nil && remote.IP.To4() != nil && !isPrivateSiteNetwork(remote) {
				appendFieldError(&errs, fmt.Sprintf("wg_client.allowed_ips[%d]", i), "must be contained in an RFC1918 private site network (10/8, 172.16/12, or 192.168/16)")
			}
		}
		if host, _, splitErr := net.SplitHostPort(c.WGClient.Endpoint); splitErr == nil && net.ParseIP(host) == nil && !validDNSNameStrict(host) {
			appendFieldError(&errs, "wg_client.endpoint", "hostname must contain valid DNS labels")
		}
	}

	// Custom input-deny rules are deliberately forbidden. The router exposes a
	// tiny fixed set of local services (HTTPS management, DHCP/DNS and the
	// WireGuard endpoint); a generic deny placed before established/related can
	// sever the exact connection used to administer the appliance before a
	// confirmation request can be sent. Access to router-local services belongs
	// to trusted_networks / management_access / feature-specific policy instead.
	for i, rule := range c.Firewall.CustomRules {
		if rule.Enabled && rule.Direction == "input" && rule.Action == "deny" {
			appendFieldError(&errs, fmt.Sprintf("firewall.custom_rules[%d]", i),
				"custom input deny rules are unavailable because they can lock out router management; use trusted networks or feature-specific access controls")
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}
