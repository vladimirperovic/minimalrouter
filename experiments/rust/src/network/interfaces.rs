use serde::{Deserialize, Serialize};
use std::fs;
use std::path::Path;

use crate::goish::json::List;
use crate::goish::net;

/// A redacted hardware-oriented view suitable for the setup UI.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct InterfaceInfo {
    pub name: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub mac_address: String,
    pub up: bool,
    pub carrier: bool,
    pub carrier_known: bool,
    pub physical: bool,
    pub default_route: bool,
    pub score: i32,
}

/// Deterministic WAN/LAN candidates. The operator still confirms the choice
/// before a disruptive configuration is applied.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct RoleRecommendation {
    pub wan: String,
    pub lan: String,
    pub interfaces: List<InterfaceInfo>,
    #[serde(default, skip_serializing_if = "List::is_empty")]
    pub warnings: List<String>,
}

/// The subset of Go's `net.Interface` discovery needs.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Interface {
    pub name: String,
    pub hardware_addr: Vec<u8>,
    pub up: bool,
}

/// Lists the kernel's interfaces from sysfs (the same data Go reads over
/// netlink): name, hardware address and the IFF_UP flag.
pub fn list_interfaces(sys_class_net: &str) -> std::io::Result<Vec<Interface>> {
    let mut out = Vec::new();
    let mut entries: Vec<_> = fs::read_dir(sys_class_net)?.filter_map(|e| e.ok()).collect();
    entries.sort_by_key(|e| {
        fs::read_to_string(e.path().join("ifindex")).ok().and_then(|s| s.trim().parse::<u32>().ok()).unwrap_or(u32::MAX)
    });
    for entry in entries {
        let name = entry.file_name().to_string_lossy().into_owned();
        let path = entry.path();
        let hardware_addr = fs::read_to_string(path.join("address"))
            .ok()
            .and_then(|s| net::parse_mac(s.trim()))
            .filter(|mac| mac.iter().any(|b| *b != 0))
            .unwrap_or_default();
        let flags = fs::read_to_string(path.join("flags"))
            .ok()
            .and_then(|s| u32::from_str_radix(s.trim().trim_start_matches("0x"), 16).ok())
            .unwrap_or(0);
        out.push(Interface { name, hardware_addr, up: flags & 0x1 != 0 });
    }
    Ok(out)
}

/// Returns usable interfaces and a deterministic WAN/LAN recommendation.
pub fn discover() -> Result<RoleRecommendation, String> {
    let interfaces = list_interfaces("/sys/class/net").map_err(|e| format!("list network interfaces: {}", e))?;
    discover_from("/sys/class/net", "/proc/net/route", &interfaces)
}

/// Exposed for deterministic tests and recovery environments.
pub fn discover_from(sys_class_net: &str, route_file: &str, interfaces: &[Interface]) -> Result<RoleRecommendation, String> {
    let default_routes = read_default_routes(route_file);
    let mut items: Vec<InterfaceInfo> = Vec::with_capacity(interfaces.len());
    for iface in interfaces {
        if !eligible_name(&iface.name) {
            continue;
        }
        let carrier_path = Path::new(sys_class_net).join(&iface.name).join("carrier");
        let mut item = InterfaceInfo {
            name: iface.name.clone(),
            mac_address: if iface.hardware_addr.is_empty() { String::new() } else { net::mac_string(&iface.hardware_addr) },
            up: iface.up,
            carrier: read_bool(&carrier_path),
            carrier_known: false,
            physical: physical_interface(sys_class_net, &iface.name),
            default_route: default_routes.contains(&iface.name),
            score: 0,
        };
        if let Ok(raw) = fs::read_to_string(&carrier_path) {
            let raw = raw.trim();
            item.carrier_known = raw == "0" || raw == "1";
        }
        item.score = score(&item);
        items.push(item);
    }
    if items.len() < 2 {
        return Err("at least two usable network interfaces are required".into());
    }

    items.sort_by(|a, b| b.score.cmp(&a.score).then_with(|| a.name.cmp(&b.name)));

    let wan_index = items.iter().position(|item| item.default_route).unwrap_or(0);
    let wan = items[wan_index].name.clone();
    let mut lan = String::new();
    for (i, item) in items.iter().enumerate() {
        if i == wan_index {
            continue;
        }
        if lan.is_empty() || (item.physical && !physical_by_name(&items, &lan)) {
            lan = item.name.clone();
        }
        if item.physical && item.carrier {
            lan = item.name.clone();
            break;
        }
    }
    if lan.is_empty() {
        return Err("could not select a distinct LAN interface".into());
    }

    let mut result = RoleRecommendation { wan: wan.clone(), lan: lan.clone(), interfaces: List::nil(), warnings: List::nil() };
    if !items[wan_index].default_route {
        result.warnings.push("No existing default route was found; WAN is a scored recommendation only.".into());
    }
    if !physical_by_name(&items, &wan) || !physical_by_name(&items, &lan) {
        result.warnings.push("One or more selected interfaces appear virtual; confirm roles on the local console.".into());
    }
    result.interfaces = items.into();
    Ok(result)
}

fn score(item: &InterfaceInfo) -> i32 {
    let mut value = 0;
    if item.physical {
        value += 100;
    }
    if item.default_route {
        value += 80;
    }
    if item.carrier {
        value += 20;
    }
    if item.up {
        value += 10;
    }
    if !item.mac_address.is_empty() {
        value += 5;
    }
    value
}

fn eligible_name(name: &str) -> bool {
    let lower = crate::goish::strings::to_lower(name.trim());
    if lower.is_empty() || lower == "lo" || lower.len() > 15 {
        return false;
    }
    const VIRTUAL: [&str; 11] = ["br-", "docker", "veth", "virbr", "tun", "tap", "wg", "ppp", "tailscale", "zt", "ifb"];
    !VIRTUAL.iter().any(|prefix| lower.starts_with(prefix))
}

fn physical_interface(sys_class_net: &str, name: &str) -> bool {
    let path = Path::new(sys_class_net).join(name).join("device");
    if fs::metadata(&path).is_ok() {
        return true;
    }
    fs::symlink_metadata(&path).map(|m| m.file_type().is_symlink()).unwrap_or(false)
}

fn read_bool(path: &Path) -> bool {
    fs::read_to_string(path).ok().and_then(|s| s.trim().parse::<i64>().ok()) == Some(1)
}

fn read_default_routes(path: &str) -> std::collections::HashSet<String> {
    let mut result = std::collections::HashSet::new();
    let Ok(data) = fs::read_to_string(path) else {
        return result;
    };
    for line in data.lines().skip(1) {
        let fields: Vec<&str> = line.split_whitespace().collect();
        if fields.len() >= 4 && fields[1] == "00000000" {
            if let Ok(flags) = u64::from_str_radix(fields[3], 16) {
                if flags & 0x1 != 0 {
                    result.insert(fields[0].to_string());
                }
            }
        }
    }
    result
}

fn physical_by_name(items: &[InterfaceInfo], name: &str) -> bool {
    items.iter().find(|item| item.name == name).is_some_and(|item| item.physical)
}
