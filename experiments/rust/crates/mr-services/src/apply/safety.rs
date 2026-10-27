//! Transition invariants that depend on both canonical and candidate state,
//! and the read-only change preview that shares them.

use serde::{Deserialize, Serialize};
use std::collections::HashMap;

use super::engine::{same_ipv4_network, CONFIRMATION_TIMEOUT};
use crate::config::{equal_section, requires_confirmation, validate_live_candidate, SystemConfig};
use crate::errorf;
use crate::goish::errors::{wrapf, Result};
use crate::goish::net;
use crate::network::{ipv4_identity, mac_identity};
use crate::services;
use crate::telemetry::{current_dhcp_lease_snapshot, DHCPLeaseSnapshot};

fn cidr_contained_by_any(candidate: &str, zones: &[String]) -> bool {
    let Some((_, candidate_net)) = net::parse_cidr(candidate).filter(|(_, n)| n.ip.to4().is_some()) else {
        return false;
    };
    let (candidate_ones, candidate_bits) = candidate_net.mask.size();
    if candidate_bits != 32 {
        return false;
    }
    zones.iter().filter_map(|z| net::parse_cidr(z)).any(|(_, zone)| {
        let (zone_ones, zone_bits) = zone.mask.size();
        zone.ip.to4().is_some() && zone_bits == 32 && candidate_ones >= zone_ones && zone.contains(&candidate_net.ip)
    })
}

/// Moving the LAN to another subnet is a two-step operation: the new subnet
/// must first be trusted and committed while the old management path still
/// works, and the candidate must retain that trust.
pub(crate) fn validate_transition_safety(current: &SystemConfig, candidate: &SystemConfig) -> Result<()> {
    if current.lan.cidr == candidate.lan.cidr {
        return Ok(());
    }
    let (Some((_, old_net)), Some((_, new_net))) = (net::parse_cidr(&current.lan.cidr), net::parse_cidr(&candidate.lan.cidr)) else {
        return Ok(()); // ordinary validation reports malformed CIDRs
    };
    if old_net.ip.to4().is_none() || new_net.ip.to4().is_none() || old_net.to_string() == new_net.to_string() {
        return Ok(());
    }
    let new_network = new_net.to_string();
    if !cidr_contained_by_any(&new_network, &current.trusted_networks) {
        return Err(errorf!(
            "LAN subnet migration requires the new subnet {} to be added to trusted_networks in a separate confirmed transaction first",
            new_network
        ));
    }
    if !cidr_contained_by_any(&new_network, &candidate.trusted_networks) {
        return Err(errorf!("LAN subnet migration candidate must retain trusted management access for the new subnet {}", new_network));
    }
    Ok(())
}

pub(crate) fn inspect_lan_change(current: &SystemConfig, candidate: &SystemConfig) -> Result<Vec<String>> {
    if current.lan.ip_address == candidate.lan.ip_address && equal_section(&current.dhcp.static_leases, &candidate.dhcp.static_leases) {
        return Ok(Vec::new());
    }
    inspect_lan_observation(current, candidate, &current_dhcp_lease_snapshot())
}

/// Observations can reject a known collision, but never prove an IP is free.
pub(crate) fn inspect_lan_observation(current: &SystemConfig, candidate: &SystemConfig, snapshot: &DHCPLeaseSnapshot) -> Result<Vec<String>> {
    let current_gateway = ipv4_identity(&current.lan.ip_address);
    let candidate_gateway = ipv4_identity(&candidate.lan.ip_address);
    let previous_by_id: HashMap<&str, _> = current.dhcp.static_leases.iter().map(|r| (r.id.as_str(), r)).collect();
    let mut changed_by_ip: HashMap<String, String> = HashMap::new();
    for reservation in candidate.dhcp.static_leases.iter() {
        let ip = ipv4_identity(&reservation.ip_address);
        let mac = mac_identity(&reservation.mac);
        let unchanged = previous_by_id
            .get(reservation.id.as_str())
            .is_some_and(|p| ipv4_identity(&p.ip_address) == ip && mac_identity(&p.mac) == mac);
        if !unchanged {
            changed_by_ip.insert(ip, mac);
        }
    }
    for lease in &snapshot.leases {
        let lease_ip = ipv4_identity(&lease.ip_address);
        if candidate_gateway != current_gateway && candidate_gateway == lease_ip {
            return Err(errorf!("new LAN gateway is currently leased to {}", lease.mac));
        }
        if let Some(mac) = changed_by_ip.get(&lease_ip) {
            if *mac != mac_identity(&lease.mac) {
                return Err(errorf!("reserved address {} is currently leased to {}", lease.ip_address, lease.mac));
            }
        }
    }
    if !snapshot.status.available || snapshot.status.truncated || snapshot.status.rejected_entries > 0 {
        return Ok(vec!["DHCP observation is unavailable or incomplete; check for address conflicts before applying.".into()]);
    }
    Ok(vec!["Only known DHCP collisions were checked. Manually configured devices may not be observed.".into()])
}

/// A read-only assessment of a candidate configuration, sharing the real
/// apply path's rules without generating files or contacting the helper.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ChangePreview {
    pub changes: Vec<String>,
    pub risk: String,
    pub requires_confirmation: bool,
    #[serde(default, skip_serializing_if = "crate::goish::json::is_zero_i64")]
    pub rollback_seconds: i64,
    pub expected_interruption: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub firewall_changes: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub firewall_warnings: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub network_changes: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub network_warnings: Vec<String>,
}

pub fn preview_transition(current: &SystemConfig, candidate: &SystemConfig) -> Result<ChangePreview> {
    if candidate.revision != current.revision {
        return Err(errorf!("stale revision: expected {}, received {}", current.revision, candidate.revision));
    }
    if candidate.lan.interface != current.lan.interface {
        return Err(errorf!("live LAN interface changes are unsupported; use the local recovery console"));
    }
    if !same_ipv4_network(&current.lan.cidr, &candidate.lan.cidr) {
        return Err(errorf!("live LAN subnet changes are unsupported; use the local recovery console"));
    }
    if candidate.system.https_port != current.system.https_port {
        return Err(errorf!("live management-port changes are unsupported"));
    }
    if candidate.system.management_access == "wireguard_only" && current.system.management_access != "wireguard_only" && !current.wireguard.enabled {
        return Err(errorf!("enable and verify WireGuard in a separate transaction before restricting management access"));
    }
    validate_live_candidate(candidate, Some(current)).map_err(|e| wrapf("validation failed", e))?;
    validate_transition_safety(current, candidate).map_err(|e| wrapf("transition safety validation failed", e))?;
    let network_warnings = inspect_lan_change(current, candidate)?;

    let changes = changed_sections(current, candidate);
    let confirmation = requires_confirmation(current, candidate);
    let risk = if confirmation {
        "high"
    } else if changes.iter().any(|c| ["WAN / PPPoE", "Firewall", "WireGuard", "DHCP / DNS", "QoS"].contains(&c.as_str())) {
        "medium"
    } else {
        "low"
    };
    let mut interruption = "No management interruption expected. Affected services may restart briefly.";
    let mut rollback_seconds = 0;
    if confirmation {
        rollback_seconds = CONFIRMATION_TIMEOUT.as_secs() as i64;
        interruption = "Management connectivity may change. The candidate is provisional until confirmed.";
    }
    if changes.is_empty() {
        interruption = "No effective configuration changes detected.";
    }
    Ok(ChangePreview {
        changes,
        risk: risk.into(),
        requires_confirmation: confirmation,
        rollback_seconds,
        expected_interruption: interruption.into(),
        firewall_changes: services::describe_firewall_changes(&current.firewall, &candidate.firewall),
        firewall_warnings: services::firewall_warnings(candidate),
        network_changes: services::describe_network_changes(current, candidate),
        network_warnings,
    })
}

fn changed_sections(current: &SystemConfig, candidate: &SystemConfig) -> Vec<String> {
    let c = current;
    let n = candidate;
    let sections: [(&str, bool); 13] = [
        ("System / management", equal_section(&c.system, &n.system)),
        ("WAN / PPPoE", equal_section(&c.wan, &n.wan)),
        ("LAN", equal_section(&c.lan, &n.lan)),
        ("DHCP / DNS", equal_section(&c.dhcp, &n.dhcp) && equal_section(&c.dns, &n.dns)),
        ("Firewall", equal_section(&c.firewall, &n.firewall)),
        ("WireGuard", equal_section(&c.wireguard, &n.wireguard) && equal_section(&c.wg_client, &n.wg_client)),
        ("Dynamic DNS / tunnel", equal_section(&c.cloudflare, &n.cloudflare)),
        ("Squid proxy", equal_section(&c.squid_proxy, &n.squid_proxy)),
        ("DNS filtering / device profiles", equal_section(&c.adguard, &n.adguard)),
        ("QoS", equal_section(&c.qos, &n.qos)),
        ("Traffic accounting", equal_section(&c.accounting, &n.accounting)),
        ("Wi-Fi", equal_section(&c.wifi, &n.wifi)),
        ("Trusted management networks", equal_section(&c.trusted_networks, &n.trusted_networks)),
    ];
    sections.iter().filter(|(_, equal)| !equal).map(|(name, _)| name.to_string()).collect()
}
