//! Cross-component policy: scenario safety, live-candidate admission, legacy
//! migration, trusted management networks, confirmation classification and
//! firewall rule scope.

use super::model::*;
use super::validation::ValidationErrors;
use crate::goish::json::equal_json;
use crate::goish::net::{self, IP, IPNet};

pub fn network_contains_network(parent: &IPNet, child: &IPNet) -> bool {
    let (p, pb) = parent.mask.size();
    let (c, cb) = child.mask.size();
    pb == cb && c >= p && parent.contains(&child.ip)
}

/// Keeps old all-zone denies unchanged. Forward allows have always been
/// WAN-only, including configurations created before Scope existed.
pub fn firewall_rule_scope(rule: &FirewallRule) -> &'static str {
    if rule.direction == "input" {
        return "router";
    }
    if rule.action == "allow" || rule.scope == "wan" {
        return "wan";
    }
    "all"
}

/// Compares the persisted/API representation of configuration sections. nil
/// and empty omitempty collections represent the same setting after a JSON
/// round trip, and must not invent a runtime change.
pub fn equal_section<A: serde::Serialize + ?Sized, B: serde::Serialize + ?Sized>(a: &A, b: &B) -> bool {
    equal_json(a, b)
}

/// Classifies changes that can disrupt operator access. Callers must still
/// independently validate the candidate and transition.
pub fn requires_confirmation(current: &SystemConfig, candidate: &SystemConfig) -> bool {
    let wireguard_management_changed = (current.system.management_access == "wireguard_only" || candidate.system.management_access == "wireguard_only")
        && !equal_section(&current.wireguard, &candidate.wireguard);
    let wifi_changed = !equal_section(&current.wifi, &candidate.wifi) && (current.wifi.enabled || candidate.wifi.enabled);
    current.lan.ip_address != candidate.lan.ip_address
        || current.lan.cidr != candidate.lan.cidr
        || current.system.management_access != candidate.system.management_access
        || wifi_changed
        || wireguard_management_changed
        || !equal_section(&current.wg_client, &candidate.wg_client)
        || !equal_section(&current.trusted_networks, &candidate.trusted_networks)
}

impl SystemConfig {
    /// Fills fields that were added after the original schema. Only absent
    /// fields that can be derived without guessing are mutated.
    ///
    /// Older ExtraLAN records did not persist RouterAddress; the first usable
    /// host in the configured subnet is the deterministic gateway.
    pub fn migrate_legacy_fields(&mut self) {
        for lan in self.firewall.extra_lans.iter_mut() {
            if !lan.enabled || !lan.router_address.trim().is_empty() {
                continue;
            }
            let Some((_, network)) = net::parse_cidr(lan.cidr.trim()) else {
                continue;
            };
            let Some(network_v4) = network.ip.to4_array() else {
                continue;
            };
            let (prefix, bits) = network.mask.size();
            if bits != 32 || prefix >= 31 {
                // /31 and /32 have no address that passes gateway validation.
                continue;
            }
            let mask = network.mask.bytes();
            let network_value = u32::from_be_bytes(network_v4);
            let broadcast_value = network_value | !u32::from_be_bytes([mask[0], mask[1], mask[2], mask[3]]);
            let gateway_value = network_value.wrapping_add(1);
            if gateway_value >= broadcast_value {
                continue;
            }
            let g = gateway_value.to_be_bytes();
            lan.router_address = format!("{}/{}", IP::from_bytes(&g), prefix);
        }
    }

    /// Whether the source address in a TCP peer string belongs to a trusted
    /// management network, or is loopback. Unparseable addresses are denied.
    pub fn is_trusted_client_address(&self, remote_addr: &str) -> bool {
        let Some(mut ip) = parse_remote_ip(remote_addr) else {
            return false;
        };
        // IPv4-mapped IPv6 must match the IPv4 networks.
        if let Some(v4) = ip.to4() {
            ip = v4;
        }
        if ip.is_loopback() {
            return true;
        }
        self.trusted_networks.iter().any(|network| net::parse_cidr(network).is_some_and(|(_, n)| n.contains(&ip)))
    }

    /// Cross-component invariants that protect management continuity and
    /// runtime behaviour rather than syntax.
    pub fn validate_scenario_safety(&self) -> Result<(), ValidationErrors> {
        let c = self;
        let mut errs = ValidationErrors::default();

        // Regex-only hostname checks are insufficient: a..b or labels ending in
        // '-' can satisfy a broad character class but are not valid DNS names.
        if !valid_dns_name_strict(&c.system.domain) {
            errs.add("system.domain", "must contain valid DNS labels (1-63 alphanumeric/hyphen characters, no empty labels or edge hyphens)");
        }
        for (i, rec) in c.dns.records.iter().enumerate() {
            if !valid_dns_name_strict(&rec.name) {
                errs.add(format!("dns.records[{}].name", i), "must contain valid DNS labels");
            }
        }

        // An enabled outbound tunnel must own one explicit point-to-point address.
        if c.wg_client.enabled {
            let address = c.wg_client.address.trim();
            match net::parse_cidr(address) {
                Some((ip, network)) if !address.is_empty() && ip.to4().is_some() => {
                    if network.mask.size().0 != 32 {
                        errs.add("wg_client.address", "must be a /32 address in this appliance version");
                    }
                }
                _ => errs.add("wg_client.address", "is required and must be a valid IPv4 /32 when the outbound tunnel is enabled"),
            }

            // wg1 is a private site-to-site tunnel: AllowedIPs are constrained
            // to RFC1918 so control-plane egress can never be captured.
            for (i, entry) in c.wg_client.allowed_ips.iter().enumerate() {
                if let Some((_, remote)) = net::parse_cidr(entry.trim()) {
                    if remote.ip.to4().is_some() && !is_private_site_network(&remote) {
                        errs.add(
                            format!("wg_client.allowed_ips[{}]", i),
                            "must be contained in an RFC1918 private site network (10/8, 172.16/12, or 192.168/16)",
                        );
                    }
                }
            }
            if let Ok((host, _)) = net::split_host_port(&c.wg_client.endpoint) {
                if net::parse_ip(&host).is_none() && !valid_dns_name_strict(&host) {
                    errs.add("wg_client.endpoint", "hostname must contain valid DNS labels");
                }
            }
        }

        // Custom input-deny rules are forbidden: a generic deny placed before
        // established/related can sever the connection used to administer the
        // appliance before a confirmation request can be sent.
        for (i, rule) in c.firewall.custom_rules.iter().enumerate() {
            if rule.enabled && rule.direction == "input" && rule.action == "deny" {
                errs.add(
                    format!("firewall.custom_rules[{}]", i),
                    "custom input deny rules are unavailable because they can lock out router management; use trusted networks or feature-specific access controls",
                );
            }
        }

        errs.into_result()
    }
}

/// Admits only complete, valid configurations with a recoverable baseline. A
/// `None` baseline is the first-run case.
pub fn validate_live_candidate(candidate: &SystemConfig, previous: Option<&SystemConfig>) -> crate::goish::errors::Result<()> {
    candidate.validate()?;
    candidate.validate_scenario_safety()?;
    if let Some(previous) = previous {
        let prefix = "rollback baseline requires local configuration migration before live changes";
        if let Err(err) = previous.validate() {
            return Err(crate::goish::errors::wrapf(prefix, err));
        }
        if let Err(err) = previous.validate_scenario_safety() {
            return Err(crate::goish::errors::wrapf(prefix, err));
        }
    }
    Ok(())
}

fn parse_remote_ip(remote_addr: &str) -> Option<IP> {
    if remote_addr.is_empty() {
        return None;
    }
    if let Ok((host, _)) = net::split_host_port(remote_addr) {
        return net::parse_ip(&host);
    }
    // RemoteAddr without a port: accept only a bare IP.
    net::parse_ip(remote_addr.trim())
}

pub(crate) fn valid_dns_name_strict(value: &str) -> bool {
    let value = value.trim();
    if value.is_empty() || value.len() > 253 || value.starts_with('.') || value.ends_with('.') {
        return false;
    }
    value.split('.').all(|label| {
        !label.is_empty()
            && label.len() <= 63
            && !label.starts_with('-')
            && !label.ends_with('-')
            && label.chars().all(|r| r.is_ascii_alphanumeric() || r == '-')
    })
}

fn network_contained_in(candidate: &IPNet, zone: &IPNet) -> bool {
    if candidate.ip.to4().is_none() || zone.ip.to4().is_none() {
        return false;
    }
    let (candidate_ones, candidate_bits) = candidate.mask.size();
    let (zone_ones, zone_bits) = zone.mask.size();
    candidate_bits == 32 && zone_bits == 32 && candidate_ones >= zone_ones && zone.contains(&candidate.ip)
}

fn is_private_site_network(network: &IPNet) -> bool {
    ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"].iter().filter_map(|cidr| net::parse_cidr(cidr)).any(|(_, zone)| network_contained_in(network, &zone))
}
