use crate::goish::net;

/// The kernel's IPv4 neighbour table, readable without privileges.
pub const ARP_TABLE_PATH: &str = "/proc/net/arp";

/// An IPv4 address the router has recently resolved on a LAN interface. Its
/// presence proves the address is in use by that MAC; its absence proves
/// nothing, because the table only holds recently active hosts.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Neighbor {
    pub ip: String,
    pub mac: String,
    pub interface: String,
}

/// Parses a /proc/net/arp style table and returns the resolved (ATF_COM)
/// entries on `iface`, or on every interface when `iface` is empty. Read errors
/// and malformed rows yield no observations rather than a guess.
pub fn read_arp_table(path: &str, iface: &str) -> Vec<Neighbor> {
    let Ok(data) = std::fs::read_to_string(path) else {
        return Vec::new();
    };
    let lines: Vec<&str> = data.split('\n').collect();
    if lines.len() < 2 {
        return Vec::new();
    }
    let mut out = Vec::new();
    for line in &lines[1..] {
        // IP address, HW type, Flags, HW address, Mask, Device
        let fields: Vec<&str> = line.split_whitespace().collect();
        if fields.len() < 6 {
            continue;
        }
        let Ok(flags) = u32::from_str_radix(fields[2].trim_start_matches("0x"), 16) else {
            continue;
        };
        if flags & 0x2 == 0 {
            continue;
        }
        if !iface.is_empty() && fields[5] != iface {
            continue;
        }
        let ip = net::parse_ipv4(fields[0]);
        let mac = net::parse_mac(fields[3]);
        let (Some(ip), Some(mac)) = (ip, mac) else {
            continue;
        };
        if mac.len() != 6 || net::mac_string(&mac) == "00:00:00:00:00:00" {
            continue;
        }
        out.push(Neighbor { ip: ip.to_string(), mac: net::mac_string(&mac), interface: fields[5].to_string() });
    }
    out
}
