package config

import (
	"fmt"
	"net"
	"strings"
)

// ValidateScenarioSafety contains cross-component invariants that exist to
// protect management continuity and runtime behavior rather than syntax. It is
// intentionally called by both routerd and the privileged helper so a future
// control-plane bug cannot bypass these appliance safety boundaries.
func (c *SystemConfig) ValidateScenarioSafety() error {
	var errs ValidationErrors

	// An enabled outbound tunnel must own one explicit point-to-point address.
	// Treating an empty address as "optional" creates a route-only interface
	// whose behavior depends on remote topology and breaks deterministic
	// verification/rollback semantics.
	if c.WGClient.Enabled {
		address := strings.TrimSpace(c.WGClient.Address)
		ip, network, err := net.ParseCIDR(address)
		if address == "" || err != nil || ip.To4() == nil {
			appendFieldError(&errs, "wg_client.address", "is required and must be a valid IPv4 /32 when the outbound tunnel is enabled")
		} else if ones, _ := network.Mask.Size(); ones != 32 {
			appendFieldError(&errs, "wg_client.address", "must be a /32 address in this appliance version")
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
