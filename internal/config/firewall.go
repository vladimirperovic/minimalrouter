package config

import "net"

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
