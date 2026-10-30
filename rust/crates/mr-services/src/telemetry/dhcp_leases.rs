use serde::{Deserialize, Serialize};
use std::io::Read;

use crate::goish::net;
use crate::goish::time::Time;

const DNSMASQ_LEASE_PATH: &str = "/var/lib/minimalrouter-dhcp/dnsmasq.leases";
const MAX_DHCP_LEASE_BYTES: usize = 1 << 20;
const MAX_DHCP_LEASE_ENTRIES: usize = 4096;
/// bufio.Scanner's maximum token size in the Go reader.
const MAX_LINE_BYTES: usize = 64 * 1024;

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct DHCPLease {
    pub expires_at: i64,
    pub mac: String,
    pub ip_address: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub hostname: String,
}

/// Distinguishes an empty lease table from an unreadable or incomplete one.
/// CollectedAt records observation time, not client liveness.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DHCPLeaseStatus {
    pub available: bool,
    pub collected_at: Time,
    pub truncated: bool,
    pub rejected_entries: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DHCPLeaseSnapshot {
    #[serde(flatten)]
    pub status: DHCPLeaseStatus,
    pub leases: Vec<DHCPLease>,
}

#[cfg_attr(not(target_os = "linux"), allow(dead_code))]
pub(crate) fn read_dhcp_lease_snapshot(path: &str) -> DHCPLeaseSnapshot {
    let mut snapshot = DHCPLeaseSnapshot { status: DHCPLeaseStatus { collected_at: Time::now_utc(), ..Default::default() }, leases: Vec::new() };
    let Ok(file) = std::fs::File::open(path) else {
        snapshot.status.error = "DHCP lease file is unavailable".into();
        return snapshot;
    };
    let mut data = Vec::new();
    if file.take(MAX_DHCP_LEASE_BYTES as u64 + 1).read_to_end(&mut data).is_err() {
        snapshot.status.error = "DHCP leases could not be read".into();
        return snapshot;
    }
    if data.len() > MAX_DHCP_LEASE_BYTES {
        snapshot.status.error = "DHCP lease file exceeds the safe read limit".into();
        snapshot.status.truncated = true;
        return snapshot;
    }
    let leases = match parse_dhcp_leases_with_status(&data, &mut snapshot.status) {
        Ok(leases) => leases,
        Err(_) => {
            snapshot.status.error = "DHCP lease data could not be parsed".into();
            return snapshot;
        }
    };
    snapshot.status.available = true;
    let now = snapshot.status.collected_at.unix();
    snapshot.leases = leases.into_iter().filter(|l| l.expires_at == 0 || l.expires_at > now).collect();
    snapshot
}

pub fn parse_dhcp_leases(data: &[u8]) -> Result<Vec<DHCPLease>, String> {
    parse_dhcp_leases_with_status(data, &mut DHCPLeaseStatus::default())
}

fn parse_dhcp_leases_with_status(data: &[u8], status: &mut DHCPLeaseStatus) -> Result<Vec<DHCPLease>, String> {
    if data.len() > MAX_DHCP_LEASE_BYTES {
        return Err("dnsmasq lease file is too large".into());
    }
    let mut leases = Vec::new();
    let text = String::from_utf8_lossy(data);
    for line in text.split('\n') {
        if line.len() > MAX_LINE_BYTES {
            return Err("bufio.Scanner: token too long".into());
        }
        let fields: Vec<&str> = line.split_whitespace().collect();
        if fields.is_empty() {
            continue;
        }
        if fields.len() < 5 {
            status.rejected_entries += 1;
            continue;
        }
        let expires_at = match crate::config::go_atoi(fields[0]) {
            Some(v) if v >= 0 => v,
            _ => {
                status.rejected_entries += 1;
                continue;
            }
        };
        let Some(mac) = net::parse_mac(fields[1]).filter(|m| m.len() == 6) else {
            status.rejected_entries += 1;
            continue;
        };
        let Some(ip) = net::parse_ipv4(fields[2]) else {
            status.rejected_entries += 1;
            continue;
        };
        let hostname = if fields[3] == "*" { "" } else { fields[3] };
        if hostname.len() > 253 || hostname.contains(['\0', '\r', '\n', '\t']) {
            status.rejected_entries += 1;
            continue;
        }
        if leases.len() >= MAX_DHCP_LEASE_ENTRIES {
            status.truncated = true;
            break;
        }
        leases.push(DHCPLease { expires_at, mac: net::mac_string(&mac), ip_address: ip.to_string(), hostname: hostname.to_string() });
    }
    Ok(leases)
}

/// Only the bounded, active DHCP lease table.
#[cfg(target_os = "linux")]
pub fn current_dhcp_leases() -> Vec<DHCPLease> {
    read_dhcp_lease_snapshot(DNSMASQ_LEASE_PATH).leases
}

#[cfg(target_os = "linux")]
pub fn current_dhcp_lease_snapshot() -> DHCPLeaseSnapshot {
    read_dhcp_lease_snapshot(DNSMASQ_LEASE_PATH)
}

/// Unavailable on non-appliance platforms.
#[cfg(not(target_os = "linux"))]
pub fn current_dhcp_leases() -> Vec<DHCPLease> {
    let _ = DNSMASQ_LEASE_PATH;
    Vec::new()
}

#[cfg(not(target_os = "linux"))]
pub fn current_dhcp_lease_snapshot() -> DHCPLeaseSnapshot {
    DHCPLeaseSnapshot {
        status: DHCPLeaseStatus { collected_at: Time::now_utc(), error: "DHCP lease observation requires Linux".into(), ..Default::default() },
        leases: Vec::new(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_and_rejects_like_go() {
        let data =
            b"1700000000 AA:BB:CC:DD:EE:FF 192.168.1.50 laptop 01:aa\n0 aa:bb:cc:dd:ee:01 192.168.1.51 * *\nbad line\n-1 aa:bb:cc:dd:ee:02 192.168.1.52 x y\n";
        let mut status = DHCPLeaseStatus::default();
        let leases = parse_dhcp_leases_with_status(data, &mut status).unwrap();
        assert_eq!(leases.len(), 2);
        assert_eq!(leases[0].mac, "aa:bb:cc:dd:ee:ff");
        assert_eq!(leases[1].hostname, "");
        assert_eq!(status.rejected_entries, 2);
    }
}
