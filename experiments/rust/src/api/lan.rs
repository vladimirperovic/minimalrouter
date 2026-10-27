//! LAN status, DHCP leases and the reservation address check.

use serde::Serialize;
use std::collections::{HashMap, HashSet};

use super::*;
use crate::config::LANSettings;
use crate::network::{self, Neighbor};
use crate::telemetry::{DHCPLeaseSnapshot, DHCPLeaseStatus};

#[derive(Serialize)]
struct LanPool {
    start: String,
    end: String,
    capacity: u64,
    observed_leases: i64,
    free: Option<u64>,
    reservations: i64,
}

fn ipv4_number(value: &str) -> Option<u32> {
    net::parse_ip(value).and_then(|ip| ip.to4()).and_then(|ip| ip.bytes().try_into().ok()).map(u32::from_be_bytes)
}

fn host_range(lan: &LANSettings) -> Option<(u32, u32, bool)> {
    let (_, subnet) = net::parse_cidr(&lan.cidr)?;
    let first = ipv4_number(&subnet.ip.to4()?.to_string())?;
    let mask = subnet.mask.bytes();
    let mask = u32::from_be_bytes(mask.get(mask.len().saturating_sub(4)..)?.try_into().ok()?);
    let last = first | !mask;
    Some((first, last, last > first.saturating_add(1)))
}

fn pool_summary(cfg: &SystemConfig, snapshot: &DHCPLeaseSnapshot) -> LanPool {
    let mut pool = LanPool { start: cfg.dhcp.range_start.clone(), end: cfg.dhcp.range_end.clone(), capacity: 0, observed_leases: 0, free: None, reservations: cfg.dhcp.static_leases.len() as i64 };
    let (Some(start), Some(end)) = (ipv4_number(&pool.start), ipv4_number(&pool.end)) else { return pool };
    if end < start {
        return pool;
    }
    pool.capacity = end as u64 - start as u64 + 1;
    let seen: HashSet<&str> = snapshot.leases.iter().filter(|l| ipv4_number(&l.ip_address).is_some_and(|ip| ip >= start && ip <= end)).map(|l| l.ip_address.as_str()).collect();
    pool.observed_leases = seen.len() as i64;
    if snapshot.status.available && !snapshot.status.truncated && snapshot.status.rejected_entries == 0 {
        pool.free = Some(pool.capacity - pool.observed_leases as u64);
    }
    pool
}

#[derive(Serialize)]
struct AddressAssessment {
    revision: config::Revision,
    address: String,
    state: String,
    conflicts: Vec<String>,
    #[serde(skip_serializing_if = "String::is_empty")]
    suggested_address: String,
    lease_status: DHCPLeaseStatus,
    limitation: String,
}

fn assess_lan_address(cfg: &SystemConfig, snapshot: &DHCPLeaseSnapshot, neighbors: &[Neighbor], address: &str, mac: &str, exclude_id: &str) -> AddressAssessment {
    let (address, mac) = (network::ipv4_identity(address), network::mac_identity(mac));
    let mut result = AddressAssessment {
        revision: cfg.revision,
        address: address.clone(),
        state: "unknown".into(),
        conflicts: Vec::new(),
        suggested_address: String::new(),
        lease_status: snapshot.status.clone(),
        limitation: "Reservations, DHCP leases and devices the router has recently seen on the LAN are checked. No active scan is performed; a quiet static device may still use this address.".into(),
    };
    let (first, last, valid) = host_range(&cfg.lan).unwrap_or((0, 0, false));
    let ip = ipv4_number(&address);
    let start = ipv4_number(&cfg.dhcp.range_start).unwrap_or(0);
    let end = ipv4_number(&cfg.dhcp.range_end).unwrap_or(0);
    if !address.is_empty() {
        if !valid || ip.is_none_or(|ip| ip <= first || ip >= last) {
            result.conflicts.push("Choose a usable host address in the LAN subnet.".into());
        }
        if address == network::ipv4_identity(&cfg.lan.ip_address) {
            result.conflicts.push("This is the router gateway address.".into());
        }
        if ip.is_some_and(|ip| ip >= start && ip <= end) {
            result.conflicts.push("This address is inside the dynamic DHCP pool.".into());
        }
        for reservation in cfg.dhcp.static_leases.iter() {
            if reservation.id != exclude_id && (network::ipv4_identity(&reservation.ip_address) == address || (!mac.is_empty() && mac == network::mac_identity(&reservation.mac))) {
                result.conflicts.push(format!("Address or MAC is already reserved for {} ({}).", reservation.hostname, reservation.mac));
            }
        }
        for lease in &snapshot.leases {
            if network::ipv4_identity(&lease.ip_address) == address && mac != network::mac_identity(&lease.mac) {
                result.conflicts.push(format!("Address is currently leased to {}.", lease.mac));
            }
        }
        // The neighbour table proves an address is taken; a missing entry
        // never proves it is free.
        for neighbor in neighbors {
            if neighbor.ip == address && mac != neighbor.mac {
                result.conflicts.push(format!("Address is in use by {} (recently seen on the LAN).", neighbor.mac));
            }
        }
        if !result.conflicts.is_empty() {
            result.state = "conflict".into();
        } else if snapshot.status.available && !snapshot.status.truncated && snapshot.status.rejected_entries == 0 {
            result.state = "no_known_conflict".into();
        }
    }
    let mut occupied: HashSet<u32> = HashSet::new();
    occupied.extend(cfg.dhcp.static_leases.iter().filter_map(|r| ipv4_number(&r.ip_address)));
    occupied.extend(snapshot.leases.iter().filter_map(|l| ipv4_number(&l.ip_address)));
    occupied.extend(neighbors.iter().filter_map(|n| ipv4_number(&n.ip)));
    occupied.insert(ipv4_number(&cfg.lan.ip_address).unwrap_or(0));
    occupied.extend(cfg.dns.records.iter().filter_map(|r| ipv4_number(&r.ip)));
    if valid {
        // Jump over the pool; inspect at most the occupied count plus two.
        let (mut candidate, mut checks) = (first as u64 + 1, 0usize);
        while candidate < last as u64 && checks <= occupied.len() + 2 {
            if candidate >= start as u64 && candidate <= end as u64 {
                candidate = end as u64 + 1;
                if candidate >= last as u64 {
                    break;
                }
            }
            if !occupied.contains(&(candidate as u32)) {
                result.suggested_address = format!("{}.{}.{}.{}", candidate >> 24, (candidate >> 16) & 255, (candidate >> 8) & 255, candidate & 255);
                break;
            }
            candidate += 1;
            checks += 1;
        }
    }
    result
}

impl Server {
    pub(crate) fn handle_lan_status(&self, _r: &mut Request<'_>) -> Response {
        let cfg = self.engine.get_current_config();
        let leases = telemetry::current_dhcp_lease_snapshot();
        let mut warnings: Vec<&str> = Vec::new();
        if !leases.status.available && cfg.dhcp.enabled {
            warnings.push("DHCP leases are unavailable; allocation usage is unknown.");
        }
        if leases.status.truncated || leases.status.rejected_entries > 0 {
            warnings.push("DHCP observation is incomplete; some addresses may be missing.");
        }
        let pool = pool_summary(&cfg, &leases);
        if cfg.dhcp.enabled && pool.capacity > 0 && pool.free.is_some_and(|free| free * 10 <= pool.capacity) {
            warnings.push("The dynamic DHCP pool is at least 90% allocated.");
        }
        let pending = self.engine.get_pending_transaction().map(|tx| json!({"id": tx.id, "state": tx.current_state, "confirmation_deadline": tx.confirmation_deadline, "management_url": management_url(&tx.config)}));
        write_json(
            200,
            &json!({
                "revision": cfg.revision, "generated_at": Time::now_utc(), "lan": cfg.lan,
                "runtime_interface": cfg.runtime_lan_interface(), "dhcp_enabled": cfg.dhcp.enabled,
                "lease_time": cfg.dhcp.lease_time, "pool": pool, "lease_status": leases.status,
                "dns": {"enabled": true, "encrypted_upstream_supported": false, "client_resolver": cfg.lan.ip_address, "upstreams": cfg.dhcp.dns_servers, "local_records": cfg.dns.records.len()},
                "pending_transaction": pending, "warnings": warnings,
                "limitations": ["A DHCP lease is not proof a device is online.", "Unallocated DHCP addresses may still be used by manually configured devices."],
            }),
        )
    }

    pub(crate) fn handle_network_devices(&self, _r: &mut Request<'_>) -> Response {
        let (cfg, now) = (self.engine.get_current_config(), Time::now_utc());
        let snapshot = telemetry::current_dhcp_lease_snapshot();
        let (mut activity, mut available) = (Vec::new(), false);
        if let Some(store) = self.subsystems().accounting.clone().filter(|_| cfg.accounting.enabled) {
            if let Ok(devices) = store.device_activity(now) {
                activity = devices;
                available = true;
            }
        }
        write_json(
            200,
            &json!({
                "revision": cfg.revision, "updated_at": now, "lease_status": snapshot.status,
                "leases": snapshot.leases, "reservations": cfg.dhcp.static_leases,
                "activity": {"available": available, "enabled": cfg.accounting.enabled, "devices": activity},
            }),
        )
    }

    pub(crate) fn handle_dhcp_leases(&self, r: &mut Request<'_>) -> Response {
        let query = r.query();
        let (mut limit, mut offset) = (100usize, 0usize);
        for key in ["limit", "offset"] {
            let raw = query.get(key);
            if raw.is_empty() {
                continue;
            }
            let parsed = config::go_atoi(&raw).filter(|n| *n >= 0 && (key != "limit" || (1..=500).contains(n)) && (key != "offset" || *n <= 4096));
            let Some(n) = parsed else {
                return write_json(400, &json!({"error": "Invalid pagination; limit is 1–500, offset is 0–4096."}));
            };
            if key == "limit" {
                limit = n as usize;
            } else {
                offset = n as usize;
            }
        }
        let order = query.get("sort");
        if !order.is_empty() && order != "ip" && order != "expiry" && order != "hostname" {
            return write_json(400, &json!({"error": "sort must be ip, expiry or hostname"}));
        }
        let search = query.get("search").trim().to_lowercase();
        if search.len() > 253 {
            return write_json(400, &json!({"error": "Search is too long"}));
        }
        let snapshot = telemetry::current_dhcp_lease_snapshot();
        let mut filtered: Vec<_> = snapshot.leases.iter().filter(|l| format!("{} {} {}", l.hostname, l.mac, l.ip_address).to_lowercase().contains(&search)).cloned().collect();
        filtered.sort_by(|a, b| {
            if order == "expiry" && a.expires_at != b.expires_at {
                return a.expires_at.cmp(&b.expires_at);
            }
            if order == "hostname" && a.hostname != b.hostname {
                return a.hostname.cmp(&b.hostname);
            }
            ipv4_number(&a.ip_address).unwrap_or(0).cmp(&ipv4_number(&b.ip_address).unwrap_or(0))
        });
        let total = filtered.len();
        let offset = offset.min(total);
        let end = (offset + limit).min(total);
        write_json(200, &json!({"lease_status": snapshot.status, "leases": &filtered[offset..end], "total": total, "offset": offset, "limit": limit}))
    }

    pub(crate) fn handle_lan_address_check(&self, r: &mut Request<'_>) -> Response {
        let query = r.query();
        let (address, mut mac, id) = (query.get("ip"), query.get("mac"), query.get("exclude_reservation_id"));
        if !address.is_empty() && ipv4_number(&address).is_none() {
            return write_json(400, &json!({"error": "ip must be IPv4"}));
        }
        if !mac.is_empty() {
            match net::parse_mac(&mac).filter(|m| m.len() == 6) {
                Some(parsed) => mac = net::mac_string(&parsed),
                None => return write_json(400, &json!({"error": "mac must be a 48-bit address"})),
            }
        }
        if id.len() > 128 {
            return write_json(400, &json!({"error": "Reservation ID is too long"}));
        }
        let cfg = self.engine.get_current_config();
        let neighbors = network::read_arp_table(network::ARP_TABLE_PATH, &cfg.lan.interface);
        write_json(200, &assess_lan_address(&cfg, &telemetry::current_dhcp_lease_snapshot(), &neighbors, &address, &mac, &id))
    }
}

/// Names resolved at read time from static and live leases; nothing beyond
/// the address is persisted in the accounting database.
pub(crate) fn device_labels(cfg: &SystemConfig) -> HashMap<String, (String, String)> {
    let mut labels: HashMap<String, (String, String)> = HashMap::new();
    for lease in cfg.dhcp.static_leases.iter() {
        labels.insert(lease.ip_address.clone(), (lease.hostname.clone(), lease.mac.clone()));
    }
    for lease in telemetry::current_dhcp_leases() {
        if labels.get(&lease.ip_address).is_some_and(|(hostname, _)| !hostname.is_empty()) {
            continue;
        }
        labels.insert(lease.ip_address.clone(), (lease.hostname.clone(), lease.mac.clone()));
    }
    labels
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn suggests_an_address_outside_the_pool() {
        let mut cfg = config::default_config();
        cfg.lan.cidr = "192.168.1.0/24".into();
        cfg.lan.ip_address = "192.168.1.1".into();
        cfg.dhcp.range_start = "192.168.1.2".into();
        cfg.dhcp.range_end = "192.168.1.200".into();
        let snapshot = DHCPLeaseSnapshot { status: DHCPLeaseStatus { available: true, ..Default::default() }, leases: Vec::new() };
        let neighbors = vec![Neighbor { ip: "192.168.1.201".into(), mac: "aa:bb:cc:dd:ee:ff".into(), interface: "eth1".into() }];
        let result = assess_lan_address(&cfg, &snapshot, &neighbors, "192.168.1.201", "", "");
        assert_eq!(result.state, "conflict");
        assert!(result.conflicts[0].contains("aa:bb:cc:dd:ee:ff"));
        assert_eq!(result.suggested_address, "192.168.1.202");
        assert_eq!(assess_lan_address(&cfg, &snapshot, &[], "192.168.1.240", "", "").state, "no_known_conflict");
    }
}
