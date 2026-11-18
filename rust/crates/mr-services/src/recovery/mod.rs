//! Local-console recovery operations against the canonical store, and the
//! offline configuration migration protocol. There is no HTTP transport here.

mod guard;
mod migration;

pub use guard::{Guard, acquire_daemon_guard};
pub use migration::{MIGRATION_DB_WORKER_ARGUMENT, migrate_config, run_migration_db_worker};

use std::collections::HashSet;
use std::sync::Arc;

use crate::auth;
use crate::config::{SQLiteStore, Snapshot, StaticLease, SystemConfig, default_config};
use crate::errorf;
use crate::goish::errors::{Result, wrapf};
use crate::goish::json;
use crate::goish::net::{self, IP, IPNet};
use crate::goish::time::Time;

pub struct Manager {
    pub store: Option<Arc<SQLiteStore>>,
}

impl Manager {
    fn store(&self) -> Result<&SQLiteStore> {
        self.store.as_deref().ok_or_else(|| errorf!("recovery store is unavailable"))
    }

    pub fn reset_authentication(&self, password: &str, disable_totp: bool) -> Result<()> {
        let store = self.store()?;
        let hash = auth::hash_password(password).map_err(|e| wrapf("new administrator password", e))?;
        store.recovery_reset_authentication(&hash, disable_totp)
    }

    pub fn set_lan(&self, interface_name: &str, cidr: &str) -> Result<Snapshot> {
        let current = self.latest()?;
        let parsed = net::parse_cidr(cidr.trim());
        let (ip, network) = match parsed {
            Some((ip, network)) if ip.to4().is_some_and(|v4| ip.equal(&v4)) => (ip.to4().unwrap(), network),
            _ => return Err(errorf!("LAN CIDR must contain an IPv4 host address")),
        };
        let (prefix, bits) = network.mask.size();
        if bits != 32 || !(16..=24).contains(&prefix) {
            return Err(errorf!("recovery LAN prefix must be between /16 and /24"));
        }
        if interface_name.trim().is_empty() || interface_name == current.wan.interface {
            return Err(errorf!("LAN interface must be non-empty and distinct from WAN"));
        }
        let (start, end) = dhcp_range(&network, &ip);
        let old_network = normalized_network(&current.lan.cidr);
        let new_network = network.to_string();
        let mut next = current.clone();
        next.revision += 1;
        next.updated_at = Time::now_utc();
        next.lan.interface = interface_name.to_string();
        next.lan.ip_address = ip.to_string();
        next.lan.cidr = format!("{}/{}", ip, prefix);
        next.lan.netmask = IP::from_bytes(network.mask.bytes()).to_string();
        next.dhcp.range_start = uint32_ip(start).to_string();
        next.dhcp.range_end = uint32_ip(end).to_string();
        // Static assignments from the old subnet cannot be safely guessed into
        // the new space; keep only those already valid there.
        next.dhcp.static_leases = filter_static_leases_for_recovery(&next.dhcp.static_leases, &network, &ip, start, end).into();
        // The API must trust the new LAN after recovery moves the address.
        next.trusted_networks = replace_network_entry(&next.trusted_networks, &old_network, &new_network, true).into();
        // Move exact old-LAN allowlist entries, but never grant new trust.
        for lan in next.firewall.extra_lans.iter_mut() {
            lan.allow_from = replace_network_entry(&lan.allow_from, &old_network, &new_network, false).into();
        }
        next.validate().map_err(|e| wrapf("recovery LAN configuration is invalid", e))?;
        next.validate_scenario_safety().map_err(|e| wrapf("recovery LAN scenario is unsafe", e))?;
        self.store()?.recovery_save_config(&current, &next, None, false)
    }

    pub fn set_wan(&self, interface_name: &str) -> Result<Snapshot> {
        let current = self.latest()?;
        let interface_name = interface_name.trim();
        if interface_name.is_empty() || interface_name == current.lan.interface {
            return Err(errorf!("WAN interface must be non-empty and distinct from LAN"));
        }
        let mut next = current.clone();
        next.revision += 1;
        next.updated_at = Time::now_utc();
        next.wan.interface = interface_name.to_string();
        next.validate()?;
        next.validate_scenario_safety()?;
        self.store()?.recovery_save_config(&current, &next, None, false)
    }

    pub fn restore_snapshot(&self, id: &str) -> Result<Snapshot> {
        let current = self.latest()?;
        let store = self.store()?;
        let target = store.get_snapshot(id.trim())?;
        let mut restored: SystemConfig = json::unmarshal(target.config_json.as_bytes()).map_err(|e| wrapf("decode snapshot", e))?;
        restored.migrate_legacy_fields();
        restored.revision = current.revision + 1;
        restored.updated_at = Time::now_utc();
        restored.validate().map_err(|e| wrapf("snapshot configuration is no longer valid", e))?;
        restored.validate_scenario_safety().map_err(|e| wrapf("snapshot scenario is no longer safe", e))?;
        store.recovery_save_config(&current, &restored, None, false)
    }

    /// Returns (undo snapshot, restored snapshot ID).
    pub fn restore_latest_snapshot(&self) -> Result<(Snapshot, String)> {
        let snapshots = self.list_snapshots()?;
        let Some(latest) = snapshots.first() else {
            return Err(errorf!("no recovery snapshots are available"));
        };
        let id = latest.id.clone();
        let undo = self.restore_snapshot(&id)?;
        Ok((undo, id))
    }

    pub fn factory_reset(&self, wan_interface: &str, lan_interface: &str, password: &str) -> Result<Snapshot> {
        let current = self.latest()?;
        let (wan, lan) = (wan_interface.trim(), lan_interface.trim());
        if wan.is_empty() || lan.is_empty() || wan == lan {
            return Err(errorf!("factory reset requires distinct WAN and LAN interfaces"));
        }
        let hash = auth::hash_password(password).map_err(|e| wrapf("new administrator password", e))?;
        let mut reset = default_config();
        reset.revision = current.revision + 1;
        reset.updated_at = Time::now_utc();
        reset.wan.interface = wan.into();
        reset.lan.interface = lan.into();
        reset.validate().map_err(|e| wrapf("factory defaults are invalid", e))?;
        reset.validate_scenario_safety().map_err(|e| wrapf("factory defaults are unsafe", e))?;
        self.store()?.recovery_save_config(&current, &reset, Some(&hash), true)
    }

    pub fn list_snapshots(&self) -> Result<Vec<Snapshot>> {
        self.store()?.list_snapshots()
    }

    fn latest(&self) -> Result<SystemConfig> {
        self.store()?.get_latest_config().map_err(|e| wrapf("read canonical configuration", e))
    }
}

fn normalized_network(cidr: &str) -> String {
    match net::parse_cidr(cidr.trim()) {
        Some((_, network)) => network.to_string(),
        None => cidr.trim().to_string(),
    }
}

/// Migrates only exact old-network entries; `ensure_new` is reserved for
/// management trust.
fn replace_network_entry(entries: &[String], old_network: &str, new_network: &str, ensure_new: bool) -> Vec<String> {
    let mut result = Vec::with_capacity(entries.len() + 1);
    let mut seen = HashSet::new();
    for entry in entries {
        let mut normalized = normalized_network(entry);
        if normalized == old_network {
            normalized = new_network.to_string();
        }
        if normalized.is_empty() || !seen.insert(normalized.clone()) {
            continue;
        }
        result.push(normalized);
    }
    if ensure_new && !seen.contains(new_network) {
        result.push(new_network.to_string());
    }
    result
}

fn ipv4_uint(ip: &IP) -> Option<u32> {
    ip.to4_array().map(u32::from_be_bytes)
}

fn uint32_ip(value: u32) -> IP {
    IP::from_bytes(&value.to_be_bytes())
}

/// Drops static assignments colliding with the new gateway or recovery pool
/// instead of failing the whole recovery action.
fn filter_static_leases_for_recovery(leases: &[StaticLease], network: &IPNet, gateway: &IP, start: u32, end: u32) -> Vec<StaticLease> {
    let broadcast = network.ipv4_broadcast();
    leases
        .iter()
        .filter(|lease| {
            let Some(ip) = net::parse_ipv4(lease.ip_address.trim()) else { return false };
            let in_pool = ipv4_uint(&ip).is_some_and(|v| v >= start && v <= end);
            network.contains(&ip) && !ip.equal(&network.ip) && !ip.equal_opt(broadcast.as_ref()) && !ip.equal(gateway) && !in_pool
        })
        .cloned()
        .collect()
}

/// A contiguous recovery pool of up to 101 addresses that never includes the
/// gateway; .100-.200 when safe, otherwise the larger usable side.
fn dhcp_range(network: &IPNet, gateway: &IP) -> (u32, u32) {
    let base = ipv4_uint(&network.ip).unwrap_or(0);
    let m = network.mask.bytes();
    let mask = u32::from_be_bytes([m[0], m[1], m[2], m[3]]);
    let broadcast = base | !mask;
    let first_usable = base + 1;
    let last_usable = broadcast - 1;
    let (preferred_start, preferred_end) = (base + 100, base + 200);
    let Some(gw) = ipv4_uint(gateway).filter(|g| (preferred_start..=preferred_end).contains(g)) else {
        return (preferred_start, preferred_end);
    };
    const MAX_POOL_SIZE: u32 = 101;
    let below = gw - first_usable;
    let above = last_usable - gw;
    if above >= below {
        let start = gw + 1;
        return (start, (start + MAX_POOL_SIZE - 1).min(last_usable));
    }
    let end = gw - 1;
    let start = if end - first_usable + 1 > MAX_POOL_SIZE { end - MAX_POOL_SIZE + 1 } else { first_usable };
    (start, end)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn test_manager(name: &str) -> (Manager, std::path::PathBuf) {
        let dir = std::env::temp_dir().join(format!("mr-recovery-{name}-{}-{}", std::process::id(), Time::now().unix_nano()));
        let store = Arc::new(SQLiteStore::new(dir.to_str().unwrap()).unwrap());
        (Manager { store: Some(store) }, dir)
    }

    #[test]
    fn restore_latest_prefers_the_newer_revision_after_a_clock_correction() {
        let (manager, dir) = test_manager("snapshot");
        let store = manager.store().unwrap();
        let mut cfg = manager.latest().unwrap();
        let old = store.create_snapshot(&cfg).unwrap();
        cfg.revision += 1;
        cfg.system.hostname = "later-revision".into();
        store.save_config(cfg.clone()).unwrap();
        let latest = store.create_manual_snapshot(&cfg, "before maintenance").unwrap();
        let same_revision = store.create_snapshot(&cfg).unwrap();
        cfg.revision += 1;
        cfg.system.hostname = "after-maintenance".into();
        store.save_config(cfg).unwrap();
        let db = rusqlite::Connection::open(dir.join("minimalrouter.db")).unwrap();
        db.execute("UPDATE snapshots SET created_at='2099-01-01T00:00:00Z' WHERE id=?", [&old.id]).unwrap();
        db.execute("UPDATE snapshots SET created_at='2026-01-01T10:00:00Z' WHERE id=?", [&latest.id]).unwrap();
        db.execute("UPDATE snapshots SET created_at='2026-01-01T12:30:00+03:00' WHERE id=?", [&same_revision.id]).unwrap();
        drop(db);

        assert_eq!(manager.list_snapshots().unwrap()[0].id, latest.id);
        let (undo, id) = manager.restore_latest_snapshot().unwrap();
        assert_eq!(id, latest.id);
        assert_eq!(manager.latest().unwrap().system.hostname, "later-revision");
        assert_eq!(undo.revision, 3);
        drop(manager);
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn narrowing_the_recovery_lan_retains_only_usable_static_leases() {
        let (manager, dir) = test_manager("lan");
        let iface = manager.latest().unwrap().lan.interface;
        manager.set_lan(&iface, "192.168.1.1/16").unwrap();
        let mut cfg = manager.latest().unwrap();
        cfg.revision += 1;
        cfg.dhcp.static_leases = [0_u8, 9, 150, 255]
            .into_iter()
            .map(|octet| StaticLease { mac: format!("02:00:00:00:01:{octet:02x}"), ip_address: format!("192.168.5.{octet}"), ..Default::default() })
            .collect::<Vec<_>>()
            .into();
        manager.store().unwrap().save_config(cfg).unwrap();
        let undo = manager.set_lan(&iface, "192.168.5.1/24").unwrap();
        let restored = manager.latest().unwrap();
        assert_eq!(restored.dhcp.static_leases.len(), 1);
        assert_eq!(restored.dhcp.static_leases[0].ip_address, "192.168.5.9");
        manager.restore_snapshot(&undo.id).unwrap();
        assert_eq!(manager.latest().unwrap().dhcp.static_leases.len(), 4);
        drop(manager);
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn dhcp_range_avoids_gateway() {
        let (_, net24) = net::parse_cidr("192.168.5.0/24").unwrap();
        let gw = net::parse_ipv4("192.168.5.1").unwrap();
        assert_eq!(dhcp_range(&net24, &gw), (u32::from_be_bytes([192, 168, 5, 100]), u32::from_be_bytes([192, 168, 5, 200])));
        let gw = net::parse_ipv4("192.168.5.150").unwrap();
        let (start, end) = dhcp_range(&net24, &gw);
        // The larger usable side is below the gateway: .49-.149 (101 addresses).
        assert_eq!(uint32_ip(start).to_string(), "192.168.5.49");
        assert_eq!(uint32_ip(end).to_string(), "192.168.5.149");
    }

    #[test]
    fn trusted_networks_follow_the_lan() {
        let entries = vec!["192.168.1.0/24".to_string(), "10.8.0.0/24".to_string(), "192.168.1.7/24".to_string()];
        assert_eq!(replace_network_entry(&entries, "192.168.1.0/24", "10.1.0.0/16", true), vec!["10.1.0.0/16", "10.8.0.0/24"]);
        assert_eq!(replace_network_entry(&["10.8.0.0/24".to_string()], "192.168.1.0/24", "10.1.0.0/16", false), vec!["10.8.0.0/24"]);
    }
}
