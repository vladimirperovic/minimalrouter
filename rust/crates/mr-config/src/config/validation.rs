use regex::Regex;
use serde::{Deserialize, Serialize};
use std::collections::HashSet;
use std::fmt;
use std::sync::LazyLock;

use super::model::*;
use crate::goish::json;
use crate::goish::net::{self, IP, IPNet};
use crate::goish::strings::{has_control, to_lower};
use crate::goish::time::{Duration, parse_duration};

macro_rules! pattern {
    ($name:ident, $re:expr) => {
        pub(crate) static $name: LazyLock<Regex> = LazyLock::new(|| Regex::new($re).expect("valid pattern"));
    };
}

pattern!(INTERFACE_NAME_PATTERN, r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,14}$");
pattern!(HOSTNAME_PATTERN, r"^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$");
pattern!(DOMAIN_PATTERN, r"^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$");
pattern!(SAFE_NAME_PATTERN, r"^[\pL\pN][\pL\pN ._()/-]{0,63}$");
pattern!(PRESET_ID_PATTERN, r"^[a-z][a-z0-9-]{0,31}$");
pattern!(CREDENTIAL_NAME_PATTERN, r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$");
pattern!(CLOUDFLARE_TOKEN_PATTERN, r"^[A-Za-z0-9_-]{20,256}$");
pattern!(WIREGUARD_ENDPOINT_HOST_PATTERN, r"^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$");
pattern!(FIELD_PATH_SEGMENT, r"([^.\[\]]+)|\[(\d+)\]");

pub(crate) fn has_unsafe_control(value: &str) -> bool {
    has_control(value)
}

pub(crate) fn valid_interface_name(value: &str) -> bool {
    INTERFACE_NAME_PATTERN.is_match(value)
}

pub(crate) fn parse_ipv4(value: &str) -> Option<IP> {
    net::parse_ipv4(value)
}

fn ipv4_value(ip: &IP) -> u32 {
    ip.to4_array().map(u32::from_be_bytes).unwrap_or(0)
}

pub(crate) fn compare_ipv4(a: &IP, b: &IP) -> std::cmp::Ordering {
    ipv4_value(a).cmp(&ipv4_value(b))
}

/// Whether two networks share any address. Both must be canonical networks
/// as returned by `parse_cidr`.
pub(crate) fn networks_overlap(a: &IPNet, b: &IPNet) -> bool {
    a.contains(&b.ip) || b.contains(&a.ip)
}

pub(crate) fn valid_wireguard_key(value: &str) -> bool {
    crate::goish::base64::std_decode(value).is_some_and(|d| d.len() == 32)
}

/// A field-specific error message.
#[derive(Clone, Debug, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct ValidationError {
    pub field: String,
    pub message: String,
}

impl fmt::Display for ValidationError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}: {}", self.field, self.message)
    }
}

impl std::error::Error for ValidationError {}

/// A list of validation errors implementing the error trait.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(transparent)]
pub struct ValidationErrors(pub Vec<ValidationError>);

impl fmt::Display for ValidationErrors {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let parts: Vec<String> = self.0.iter().map(ToString::to_string).collect();
        f.write_str(&parts.join("; "))
    }
}

impl std::error::Error for ValidationErrors {}

impl ValidationErrors {
    pub(crate) fn add(&mut self, field: impl Into<String>, message: impl Into<String>) {
        self.0.push(ValidationError { field: field.into(), message: message.into() });
    }

    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }

    pub fn len(&self) -> usize {
        self.0.len()
    }

    pub fn iter(&self) -> std::slice::Iter<'_, ValidationError> {
        self.0.iter()
    }

    pub(crate) fn into_result(self) -> Result<(), ValidationErrors> {
        if self.0.is_empty() { Ok(()) } else { Err(self) }
    }
}

impl SystemConfig {
    /// Reports introduced problems for diagnostic comparisons. It is NOT an
    /// admission check: inherited errors still make a candidate unsafe to
    /// persist or boot. Preview, apply, persistence and startup must use
    /// [`SystemConfig::validate`].
    pub fn validate_changes_from(&self, previous: Option<&SystemConfig>) -> Result<(), ValidationErrors> {
        let candidate = match self.validate() {
            Ok(()) => return Ok(()),
            Err(errs) => errs,
        };
        let Some(previous) = previous else {
            return Err(candidate);
        };
        let stored: HashSet<ValidationError> = match previous.validate() {
            Ok(()) => HashSet::new(),
            Err(errs) => errs.0.into_iter().collect(),
        };
        if stored.is_empty() {
            return Err(candidate);
        }

        // A stale fault is excused only while the operator leaves it alone.
        // Once the offending field is edited, the new value is this change's
        // responsibility and must satisfy the current rules.
        let candidate_tree = to_field_tree(self);
        let previous_tree = to_field_tree(previous);
        let unchanged = |field: &str| match (&candidate_tree, &previous_tree) {
            (Some(c), Some(p)) => same_field_value(c, p, field),
            _ => false,
        };

        let introduced: Vec<ValidationError> = candidate.0.into_iter().filter(|item| !(stored.contains(item) && unchanged(&item.field))).collect();
        ValidationErrors(introduced).into_result()
    }
}

/// Renders a configuration as generic JSON so a field path can be resolved.
fn to_field_tree(cfg: &SystemConfig) -> Option<serde_json::Value> {
    let data = json::marshal(cfg).ok()?;
    serde_json::from_slice(&data).ok()
}

/// Walks a validation field path such as "wan.password" or
/// "firewall.custom_rules[0].name" through a decoded JSON tree.
fn resolve_field_path<'a>(tree: &'a serde_json::Value, field: &str) -> Option<&'a serde_json::Value> {
    static NULL: serde_json::Value = serde_json::Value::Null;
    let mut node = tree;
    for caps in FIELD_PATH_SEGMENT.captures_iter(field) {
        if let Some(key) = caps.get(1) {
            let object = node.as_object()?;
            // An omitempty field is absent rather than unresolvable, and
            // "absent on both sides" is exactly the unchanged case.
            node = object.get(key.as_str()).unwrap_or(&NULL);
        } else {
            let array = node.as_array()?;
            let index: usize = caps.get(2)?.as_str().parse().ok()?;
            node = array.get(index)?;
        }
    }
    Some(node)
}

/// Whether both configurations carry an identical value at field. An
/// unresolvable path is reported as changed.
fn same_field_value(candidate: &serde_json::Value, previous: &serde_json::Value, field: &str) -> bool {
    match (resolve_field_path(candidate, field), resolve_field_path(previous, field)) {
        (Some(c), Some(p)) => c == p,
        _ => false,
    }
}

impl SystemConfig {
    /// Checks the complete configuration for syntax and cross-field invariant errors.
    pub fn validate(&self) -> Result<(), ValidationErrors> {
        let c = self;
        let mut errs = ValidationErrors::default();

        if !HOSTNAME_PATTERN.is_match(&c.system.hostname) {
            errs.add("system.hostname", "must be a valid RFC-compatible hostname label");
        }
        if !DOMAIN_PATTERN.is_match(&c.system.domain) || has_unsafe_control(&c.system.domain) {
            errs.add("system.domain", "must be a valid local DNS domain");
        }
        if c.system.https_port < 1024 || c.system.https_port > 65535 {
            errs.add("system.https_port", "must be an unprivileged port between 1024 and 65535");
        }
        if !c.system.https_enabled {
            errs.add("system.https_enabled", "management HTTPS cannot be disabled");
        }
        let access = c.system.management_access.as_str();
        if !access.is_empty() && access != "lan_and_wireguard" && access != "wireguard_only" {
            errs.add("system.management_access", "must be lan_and_wireguard or wireguard_only");
        }
        if access == "wireguard_only" {
            if !c.wireguard.enabled {
                errs.add("system.management_access", "requires WireGuard to be enabled");
            }
            if !c.wireguard.peers.iter().any(|peer| peer.enabled) {
                errs.add("system.management_access", "requires at least one enabled WireGuard peer");
            }
        }

        if !valid_interface_name(&c.wan.interface) {
            errs.add("wan.interface", "must be a valid Linux interface name of at most 15 characters");
        }
        if !valid_interface_name(&c.lan.interface) {
            errs.add("lan.interface", "must be a valid Linux interface name of at most 15 characters");
        }
        if c.wan.interface == c.lan.interface {
            errs.add("wan.interface", "WAN interface cannot be the same as LAN interface");
        }

        if c.wan.mtu < 1280 || c.wan.mtu > 1500 {
            errs.add("wan.mtu", "must be between 1280 and 1500");
        }
        if has_unsafe_control(&c.wan.username) || c.wan.username.len() > 255 {
            errs.add("wan.username", "contains forbidden control characters or is too long");
        }
        if has_unsafe_control(&c.wan.password) || c.wan.password.len() > 1024 {
            errs.add("wan.password", "contains forbidden control characters or is too long");
        }
        if c.wan.enabled {
            if c.wan.username.trim().is_empty() {
                errs.add("wan.username", "is required when PPPoE is enabled");
            }
            if c.wan.password.is_empty() || c.wan.password == "[REDACTED]" {
                errs.add("wan.password", "a new PPPoE password is required when none is stored");
            }
        }

        let lan_ip = parse_ipv4(&c.lan.ip_address);
        if lan_ip.is_none() {
            errs.add("lan.ip_address", "must be a valid IPv4 address");
        }

        let mut lan_network: Option<IPNet> = None;
        if !c.lan.cidr.is_empty() {
            match net::parse_cidr(&c.lan.cidr) {
                None => errs.add("lan.cidr", "must be valid IPv4 CIDR notation"),
                Some((cidr_ip, _)) if cidr_ip.to4().is_none() => errs.add("lan.cidr", "must contain an IPv4 address"),
                Some((cidr_ip, network)) => {
                    if let Some(lan_ip) = &lan_ip {
                        if !network.contains(lan_ip) {
                            errs.add("lan.cidr", "must contain the configured LAN address");
                        }
                        if !cidr_ip.equal(lan_ip) {
                            errs.add("lan.cidr", "address must exactly match lan.ip_address");
                        }
                        let broadcast = network.ipv4_broadcast();
                        if lan_ip.equal(&network.ip)
                            || lan_ip.equal_opt(broadcast.as_ref())
                            || lan_ip.is_unspecified()
                            || lan_ip.is_loopback()
                            || lan_ip.is_multicast()
                        {
                            errs.add("lan.ip_address", "must be a usable host address, not a network, broadcast, loopback or multicast address");
                        }
                    }
                    let expected_mask = IP::from_bytes(network.mask.bytes()).to_string();
                    if c.lan.netmask != expected_mask {
                        errs.add("lan.netmask", "must match the CIDR prefix");
                    }
                    lan_network = Some(network);
                }
            }
        } else {
            errs.add("lan.cidr", "is required");
        }

        // dnsmasq remains the LAN resolver even when address allocation is disabled.
        if c.dhcp.dns_servers.is_empty() || c.dhcp.dns_servers.len() > 3 {
            errs.add("dhcp.dns_servers", "must contain between one and three explicit upstream resolvers");
        }
        let mut seen_dns: HashSet<String> = HashSet::new();
        for (i, dns) in c.dhcp.dns_servers.iter().enumerate() {
            let field = format!("dhcp.dns_servers[{}]", i);
            match parse_ipv4(dns) {
                None => errs.add(field, "must be a valid IPv4 address"),
                Some(ip)
                    if ip.is_unspecified()
                        || ip.is_loopback()
                        || ip.is_multicast()
                        || ip.equal(&IP::v4(255, 255, 255, 255))
                        || ip.equal_opt(lan_ip.as_ref()) =>
                {
                    errs.add(field, "must be a unicast upstream resolver, not the router's own DNS listener")
                }
                Some(ip) => {
                    if !seen_dns.insert(ip.to_string()) {
                        errs.add(field, "duplicates another upstream resolver");
                    }
                }
            }
        }
        if c.dhcp.enabled {
            self.validate_dhcp(&mut errs, lan_ip.as_ref(), lan_network.as_ref());
        }

        self.validate_firewall(&mut errs, lan_network.as_ref());

        self.validate_extra_lans(&mut errs, lan_network.as_ref());

        if c.wireguard.enabled {
            self.validate_wireguard(&mut errs, lan_ip.as_ref(), lan_network.as_ref());
        }

        validate_wg_client(c, lan_ip.as_ref(), lan_network.as_ref(), &mut errs);

        if c.cloudflare.ddns_enabled {
            if !c.wan.enabled {
                errs.add("cloudflare.ddns_enabled", "requires an enabled WAN connection");
            }
            if !DOMAIN_PATTERN.is_match(&c.cloudflare.domain) || !c.cloudflare.domain.contains('.') {
                errs.add("cloudflare.domain", "must be a valid fully qualified DDNS hostname");
            }
            let mut provider = to_lower(c.cloudflare.ddns_provider.trim());
            if provider.is_empty() {
                provider = "cloudflare".into();
            }
            match provider.as_str() {
                "noip" => {
                    let user = &c.cloudflare.ddns_user;
                    if user.trim().is_empty() || user.len() > 255 || has_unsafe_control(user) {
                        errs.add("cloudflare.ddns_username", "No-IP username or DDNS Key username is required and must not contain control characters");
                    }
                    let token = &c.cloudflare.api_token;
                    if token.is_empty() || token == "[REDACTED]" || token.len() > 1024 || has_unsafe_control(token) {
                        errs.add("cloudflare.api_token", "No-IP DDNS Key/account password is required and must not contain control characters");
                    }
                }
                "cloudflare" => {
                    if !DOMAIN_PATTERN.is_match(&c.cloudflare.zone_name) || !c.cloudflare.zone_name.contains('.') {
                        errs.add("cloudflare.zone_name", "must be the Cloudflare zone name, for example example.com");
                    }
                    if !CLOUDFLARE_TOKEN_PATTERN.is_match(&c.cloudflare.api_token) {
                        errs.add("cloudflare.api_token", "must be a valid Cloudflare API token");
                    }
                }
                _ => errs.add("cloudflare.ddns_provider", "must be noip or cloudflare"),
            }
        }
        if c.cloudflare.tunnel_enabled {
            errs.add("cloudflare.tunnel_enabled", "is unavailable because WireGuard is the only allowed remote-entry path");
        }

        if c.squid_proxy.enabled {
            let proxy = &c.squid_proxy;
            if proxy.port < 1 || proxy.port > 65535 || proxy.port == c.system.https_port {
                errs.add("squid_proxy.port", "must be a non-management port between 1 and 65535");
            }
            if has_unsafe_control(&proxy.username) || has_unsafe_control(&proxy.password) {
                errs.add("squid_proxy", "credentials contain forbidden control characters");
            }
            if !CREDENTIAL_NAME_PATTERN.is_match(&proxy.username) {
                errs.add("squid_proxy.username", "must contain only letters, numbers, dot, underscore, or hyphen");
            }
            if proxy.password.chars().count() < 8 || proxy.password.len() > 1024 || proxy.password == "[REDACTED]" {
                errs.add("squid_proxy.password", "must contain 8-1024 characters");
            }
        }
        for (i, item) in c.squid_proxy.restricted_ips.iter().enumerate() {
            let ip = parse_ipv4(&item.ip_address);
            if ip.is_none() || lan_network.as_ref().is_some_and(|n| !n.contains_opt(ip.as_ref())) {
                errs.add(format!("squid_proxy.restricted_ips[{}].ip_address", i), "must be a valid LAN address");
            }
        }

        errs.0.extend(c.validate_device_profiles(lan_network.as_ref()).0);
        if !c.adguard.blocklist_url.is_empty() {
            errs.add("adguard.blocklist_url", "external blocklist refresh is unavailable in the hardened pilot; use the built-in global list");
        }

        let mut seen_dns_names: HashSet<String> = HashSet::new();
        let mut seen_dns_ips: HashSet<String> = HashSet::new();
        for (i, rec) in c.dns.records.iter().enumerate() {
            let lower = to_lower(rec.name.strip_suffix('.').unwrap_or(&rec.name));
            if !DOMAIN_PATTERN.is_match(&rec.name) || has_unsafe_control(&rec.name) {
                errs.add(format!("dns.records[{}].name", i), "must be a valid DNS hostname");
            }
            if lower.ends_with(".local") {
                errs.add(format!("dns.records[{}].name", i), ".local is the mDNS namespace and conflicts with macOS/iOS resolvers; use .home.arpa instead");
            }
            if !lower.is_empty() && seen_dns_names.contains(&lower) {
                errs.add(format!("dns.records[{}].name", i), "duplicates another record (hostnames are case-insensitive)");
            }
            if !lower.is_empty() {
                seen_dns_names.insert(lower);
            }
            match parse_ipv4(&rec.ip) {
                None => errs.add(format!("dns.records[{}].ip", i), "must be a valid IPv4 address"),
                Some(ip) => {
                    if !seen_dns_ips.insert(ip.to_string()) {
                        errs.add(format!("dns.records[{}].ip", i), "duplicates another record's address");
                    }
                }
            }
        }
        let domain_suffix = format!(".{}", to_lower(c.system.domain.strip_suffix('.').unwrap_or(&c.system.domain)));
        for (i, lease) in c.dhcp.static_leases.iter().enumerate() {
            if lease.hostname.is_empty() {
                continue;
            }
            let mut lease_fqdn = to_lower(&lease.hostname);
            if domain_suffix != "." {
                lease_fqdn.push_str(&domain_suffix);
            }
            if seen_dns_names.contains(&lease_fqdn) {
                errs.add(format!("dhcp.static_leases[{}].hostname", i), "conflicts with a static DNS record hostname");
            }
        }

        if c.qos.enabled {
            if c.qos.algorithm != "cake" && c.qos.algorithm != "fq_codel" {
                errs.add("qos.algorithm", "must be cake or fq_codel");
            }
            if c.qos.download_limit_mbps <= 0 {
                errs.add("qos.download_limit_mbps", "must be greater than zero");
            }
            if c.qos.upload_limit_mbps <= 0 {
                errs.add("qos.upload_limit_mbps", "must be greater than zero");
            }
            if c.qos.download_limit_mbps > 100000 || c.qos.upload_limit_mbps > 100000 {
                errs.add("qos", "limits must not exceed 100000 Mbps");
            }
        }

        if c.dhcp.dns_enabled {
            errs.add("dhcp.dns_enabled", "DNS-over-HTTPS is unavailable until a packaged and verified local resolver is installed");
        }

        if c.wifi.enabled {
            let wifi = &c.wifi;
            if !valid_interface_name(&wifi.interface) {
                errs.add("wifi.interface", "must be a valid Linux interface name");
            }
            if wifi.interface == c.wan.interface || wifi.interface == c.lan.interface || wifi.interface == WIFI_BRIDGE_INTERFACE {
                errs.add("wifi.interface", "must not reuse the WAN or LAN interface");
            }
            if wifi.ssid.is_empty() || wifi.ssid.len() > 32 || has_unsafe_control(&wifi.ssid) {
                errs.add("wifi.ssid", "must contain 1-32 bytes and no control characters");
            }
            if wifi.passphrase.len() < 12 || wifi.passphrase.len() > 63 || has_unsafe_control(&wifi.passphrase) {
                errs.add("wifi.passphrase", "must contain 12-63 bytes and no control characters");
            }
            if wifi.band != "2.4ghz" && wifi.band != "5ghz" {
                errs.add("wifi.band", "must be 2.4ghz or 5ghz");
            }
            if wifi.band == "2.4ghz" && (wifi.channel < 1 || wifi.channel > 11) {
                errs.add("wifi.channel", "must be 1-11 for 2.4 GHz");
            }
            if wifi.band == "5ghz" && ![36, 40, 44, 48].contains(&wifi.channel) {
                errs.add("wifi.channel", "must be 36, 40, 44, or 48 for the portable 5 GHz profile");
            }
        }

        // Trusted networks gate administrative Web UI/API access. The list must
        // never be empty, and wildcard ranges are rejected because they defeat
        // the purpose of an access boundary.
        if c.trusted_networks.is_empty() {
            errs.add("trusted_networks", "must contain at least one CIDR network");
        }
        let mut seen: HashSet<&str> = HashSet::new();
        for (i, network) in c.trusted_networks.iter().enumerate() {
            let field = format!("trusted_networks[{}]", i);
            if has_unsafe_control(network) {
                errs.add(field, "must not contain control characters");
                continue;
            }
            if !seen.insert(network.as_str()) {
                errs.add(field, "must not contain duplicate networks");
                continue;
            }
            let Some((ip, ip_net)) = net::parse_cidr(network) else {
                errs.add(field, "must be a valid IPv4 or IPv6 CIDR network");
                continue;
            };
            if ip_net.mask.size().0 == 0 || ip.is_unspecified() {
                errs.add(field, "wildcard networks (0.0.0.0/0, ::/0) are not allowed");
            }
        }

        errs.into_result()
    }

    fn validate_dhcp(&self, errs: &mut ValidationErrors, lan_ip: Option<&IP>, lan_network: Option<&IPNet>) {
        let c = self;
        let start_ip = parse_ipv4(&c.dhcp.range_start);
        let end_ip = parse_ipv4(&c.dhcp.range_end);
        if start_ip.is_none() {
            errs.add("dhcp.range_start", "must be a valid IPv4 address");
        }
        if end_ip.is_none() {
            errs.add("dhcp.range_end", "must be a valid IPv4 address");
        }
        if let (Some(start), Some(end)) = (&start_ip, &end_ip) {
            if compare_ipv4(start, end).is_gt() {
                errs.add("dhcp.range", "range start must not be after range end");
            }
            if let Some(network) = lan_network {
                if !network.contains(start) || !network.contains(end) {
                    errs.add("dhcp.range", "must be contained in the LAN subnet");
                }
                let network_ip = network.ip.to4().unwrap_or_else(|| network.ip.clone());
                if start.equal(&network_ip) || end.equal_opt(network.ipv4_broadcast().as_ref()) {
                    errs.add("dhcp.range", "cannot include the network or broadcast address");
                }
            }
            if let Some(lan_ip) = lan_ip {
                if compare_ipv4(start, lan_ip).is_le() && compare_ipv4(lan_ip, end).is_le() {
                    errs.add("dhcp.range", "cannot contain the LAN gateway address");
                }
            }
        }
        match parse_duration(&c.dhcp.lease_time) {
            Ok(lease) if lease >= Duration::MINUTE && lease <= Duration::hours(7 * 24) => {}
            _ => errs.add("dhcp.lease_time", "must be a duration between 1m and 168h"),
        }
        let mut seen_mac: HashSet<String> = HashSet::new();
        let mut seen_lease_ip: HashSet<String> = HashSet::new();
        for (i, lease) in c.dhcp.static_leases.iter().enumerate() {
            match net::parse_mac(&lease.mac) {
                Some(mac) if mac.len() == 6 => {
                    if !seen_mac.insert(net::mac_string(&mac)) {
                        errs.add(format!("dhcp.static_leases[{}].mac", i), "duplicates another static lease");
                    }
                }
                _ => errs.add(format!("dhcp.static_leases[{}].mac", i), "must be a valid 48-bit MAC address"),
            }
            let ip = parse_ipv4(&lease.ip_address);
            let field = format!("dhcp.static_leases[{}].ip_address", i);
            match (&ip, lan_network) {
                (None, _) => errs.add(field.clone(), "must be a valid address in the LAN subnet"),
                (Some(ip), Some(network)) if !network.contains(ip) => errs.add(field.clone(), "must be a valid address in the LAN subnet"),
                (Some(ip), Some(network)) => {
                    let network_ip = network.ip.to4().unwrap_or_else(|| network.ip.clone());
                    if lan_ip.is_some_and(|lan| ip.equal(lan)) {
                        errs.add(field.clone(), "cannot use the router LAN address");
                    } else if ip.equal(&network_ip) {
                        errs.add(field.clone(), "cannot use the network address");
                    } else if ip.equal_opt(network.ipv4_broadcast().as_ref()) {
                        errs.add(field.clone(), "cannot use the broadcast address");
                    } else if let (Some(start), Some(end)) = (&start_ip, &end_ip) {
                        if compare_ipv4(ip, start).is_ge() && compare_ipv4(ip, end).is_le() {
                            errs.add(field.clone(), "must not overlap the dynamic DHCP pool");
                        }
                    }
                }
                (Some(_), None) => {}
            }
            if let Some(ip) = &ip {
                if !seen_lease_ip.insert(ip.to_string()) {
                    errs.add(field, "duplicates another static lease");
                }
            }
            if !lease.hostname.is_empty() && !HOSTNAME_PATTERN.is_match(&lease.hostname) {
                errs.add(format!("dhcp.static_leases[{}].hostname", i), "must be a valid hostname");
            }
        }
    }

    fn validate_firewall(&self, errs: &mut ValidationErrors, lan_network: Option<&IPNet>) {
        let c = self;
        let mut seen_forward: HashSet<String> = HashSet::new();
        if c.firewall.default_wan_input_policy != "deny" {
            errs.add("firewall.default_wan_input_policy", "must remain deny");
        }
        if !c.firewall.wan_ingress_mode.is_empty() && c.firewall.wan_ingress_mode != "wireguard_only" {
            errs.add("firewall.wan_ingress_mode", "must remain wireguard_only");
        }
        if !c.firewall.stateful_firewall {
            errs.add("firewall.stateful_firewall", "cannot be disabled");
        }
        for (i, pf) in c.firewall.port_forwards.iter().enumerate() {
            // Port forwards are reachable over the WireGuard management tunnel
            // only; a forward without a running tunnel would silently do nothing.
            if pf.enabled && !c.wireguard.enabled {
                errs.add(format!("firewall.port_forwards[{}].enabled", i), "requires WireGuard: forwards are reachable only over the tunnel, never from WAN");
            }
            if !SAFE_NAME_PATTERN.is_match(&pf.name) || has_unsafe_control(&pf.name) {
                errs.add(format!("firewall.port_forwards[{}].name", i), "contains unsupported characters");
            }
            let protocol = to_lower(&pf.protocol);
            if protocol != "tcp" && protocol != "udp" && protocol != "both" {
                errs.add(format!("firewall.port_forwards[{}].protocol", i), "must be tcp, udp, or both");
            }
            if pf.external_port < 1 || pf.external_port > 65535 {
                errs.add(format!("firewall.port_forwards[{}].external_port", i), "must be between 1 and 65535");
            }
            if pf.internal_port < 1 || pf.internal_port > 65535 {
                errs.add(format!("firewall.port_forwards[{}].internal_port", i), "must be between 1 and 65535");
            }
            let target_ip = parse_ipv4(&pf.internal_ip);
            if target_ip.is_none() || lan_network.is_some_and(|n| !n.contains_opt(target_ip.as_ref())) {
                errs.add(format!("firewall.port_forwards[{}].internal_ip", i), "must be a valid address in the LAN subnet");
            }
            if pf.external_port == c.system.https_port || pf.external_port == 8443 {
                errs.add(format!("firewall.port_forwards[{}].external_port", i), "cannot expose a router management port");
            }
            let protocols: Vec<&str> = if protocol == "both" { vec!["tcp", "udp"] } else { vec![protocol.as_str()] };
            for item_protocol in protocols {
                let key = format!("{}/{}", item_protocol, pf.external_port);
                if !seen_forward.insert(key) {
                    errs.add(format!("firewall.port_forwards[{}]", i), "duplicates an external protocol/port");
                }
            }
        }

        for (i, rule) in c.firewall.custom_rules.iter().enumerate() {
            if !rule.scope.is_empty() && rule.scope != "all" && rule.scope != "wan" {
                errs.add(format!("firewall.custom_rules[{}].scope", i), "must be all or wan");
            }
            if rule.direction == "input" && rule.scope == "wan" {
                errs.add(format!("firewall.custom_rules[{}].scope", i), "WAN scope is only valid for forward rules");
            }
            if !rule.preset_id.is_empty() && !PRESET_ID_PATTERN.is_match(&rule.preset_id) {
                errs.add(format!("firewall.custom_rules[{}].preset_id", i), "must be a short preset identifier");
            }
            if !SAFE_NAME_PATTERN.is_match(&rule.name) || has_unsafe_control(&rule.name) {
                errs.add(format!("firewall.custom_rules[{}].name", i), "contains unsupported characters");
            }
            if rule.action != "allow" && rule.action != "deny" {
                errs.add(format!("firewall.custom_rules[{}].action", i), "must be allow or deny");
            }
            if rule.direction != "input" && rule.direction != "forward" {
                errs.add(format!("firewall.custom_rules[{}].direction", i), "must be input or forward");
            }
            match rule.protocol.as_str() {
                "tcp" | "udp" => {
                    if rule.dst_port < 1 || rule.dst_port > 65535 {
                        errs.add(format!("firewall.custom_rules[{}].dst_port", i), "is required for TCP/UDP and must be between 1 and 65535");
                    }
                }
                "icmp" | "any" => {
                    if rule.dst_port != 0 {
                        errs.add(format!("firewall.custom_rules[{}].dst_port", i), "must be zero for ICMP/any");
                    }
                }
                _ => errs.add(format!("firewall.custom_rules[{}].protocol", i), "must be tcp, udp, icmp, or any"),
            }
            if !rule.src_ip.is_empty() {
                // Both an address and a CIDR are valid nftables `ip saddr`
                // matches; either must still stay inside the LAN.
                let field = format!("firewall.custom_rules[{}].src_ip", i);
                if let Some(ip) = parse_ipv4(&rule.src_ip) {
                    if lan_network.is_some_and(|n| !n.contains(&ip)) {
                        errs.add(field, "must be a valid LAN source address");
                    }
                } else {
                    match net::parse_cidr(&rule.src_ip) {
                        Some((_, src)) if src.ip.to4().is_some() => {
                            if lan_network.is_some_and(|n| !super::policy::network_contains_network(n, &src)) {
                                errs.add(field, "must be contained in the LAN network");
                            }
                        }
                        _ => errs.add(field, "must be a valid LAN source address or IPv4 CIDR"),
                    }
                }
            }
            if c.system.management_access == "wireguard_only" && rule.enabled && rule.direction == "input" && rule.action == "allow" {
                errs.add(format!("firewall.custom_rules[{}]", i), "LAN input allow rules are forbidden in wireguard_only management mode");
            }
        }
    }

    fn validate_extra_lans(&self, errs: &mut ValidationErrors, lan_network: Option<&IPNet>) {
        let c = self;
        let mut wg_network: Option<IPNet> = None;
        if c.wireguard.enabled {
            if let Some((_, wg_net)) = net::parse_cidr(&c.wireguard.address) {
                wg_network = Some(wg_net);
            }
        }
        let mut seen_extra_interfaces: HashSet<&str> = HashSet::new();
        let mut extra_networks: Vec<IPNet> = Vec::new();
        for (i, lan) in c.firewall.extra_lans.iter().enumerate() {
            if !lan.enabled {
                continue;
            }
            let prefix = format!("firewall.extra_lans[{}]", i);
            if !SAFE_NAME_PATTERN.is_match(&lan.name) || has_unsafe_control(&lan.name) || lan.name.len() > 64 {
                errs.add(format!("{prefix}.name"), "must be 1-64 safe characters and contain no control characters");
            }
            if has_unsafe_control(&lan.id) || lan.id.len() > 64 {
                errs.add(format!("{prefix}.id"), "must contain no control characters and be at most 64 characters");
            }
            if !valid_interface_name(&lan.interface) || lan.interface.starts_with("ppp") {
                errs.add(format!("{prefix}.interface"), "must be a valid non-PPP interface name");
            } else {
                let iface = lan.interface.as_str();
                if iface == c.wan.interface {
                    errs.add(format!("{prefix}.interface"), "must not reuse the WAN interface");
                } else if iface == c.lan.interface || iface == WIFI_BRIDGE_INTERFACE {
                    errs.add(format!("{prefix}.interface"), "must not reuse the LAN interface");
                } else if c.wireguard.enabled && iface == c.wireguard.interface {
                    errs.add(format!("{prefix}.interface"), "must not reuse the WireGuard interface");
                } else if c.wg_client.enabled && iface == c.wg_client.interface {
                    errs.add(format!("{prefix}.interface"), "must not reuse the WireGuard client interface");
                } else if c.wifi.enabled && (iface == c.wifi.interface || iface == WIFI_BRIDGE_INTERFACE) {
                    errs.add(format!("{prefix}.interface"), "must not reuse the Wi-Fi radio or its bridge");
                }
                if !seen_extra_interfaces.insert(iface) {
                    errs.add(format!("{prefix}.interface"), "duplicates another extra LAN interface");
                }
            }
            // Go keeps a parsed non-IPv4 network for the destination checks
            // below even though the CIDR itself is rejected.
            let extra_net = net::parse_cidr(&lan.cidr).map(|(_, n)| n);
            match &extra_net {
                Some(extra) if extra.ip.to4().is_none() => errs.add(format!("{prefix}.cidr"), "must be valid IPv4 CIDR notation"),
                None => errs.add(format!("{prefix}.cidr"), "must be valid IPv4 CIDR notation"),
                Some(extra) => {
                    if lan_network.is_some_and(|n| networks_overlap(n, extra)) {
                        errs.add(format!("{prefix}.cidr"), "must not overlap the LAN subnet");
                    }
                    if wg_network.as_ref().is_some_and(|n| networks_overlap(n, extra)) {
                        errs.add(format!("{prefix}.cidr"), "must not overlap the WireGuard subnet");
                    }
                    if !c.wg_client.address.is_empty() {
                        if let Some((_, client_net)) = net::parse_cidr(&c.wg_client.address) {
                            if networks_overlap(&client_net, extra) {
                                errs.add(format!("{prefix}.cidr"), "must not overlap the WireGuard client subnet");
                            }
                        }
                    }
                    for previous in &extra_networks {
                        if networks_overlap(previous, extra) {
                            errs.add(format!("{prefix}.cidr"), "must not overlap another extra LAN subnet");
                        }
                    }
                    extra_networks.push(extra.clone());

                    if lan.router_address.is_empty() {
                        errs.add(format!("{prefix}.router_address"), "is required for an enabled extra LAN so the router can reconstruct the segment");
                    } else {
                        match net::parse_cidr(&lan.router_address) {
                            Some((router_ip, router_net)) if router_ip.to4().is_some() => {
                                if extra.contains(&router_ip) {
                                    let network_ip = extra.ip.to4().unwrap_or_else(|| extra.ip.clone());
                                    if router_ip.equal(&network_ip) {
                                        errs.add(format!("{prefix}.router_address"), "cannot use the network address");
                                    } else if router_ip.equal_opt(extra.ipv4_broadcast().as_ref()) {
                                        errs.add(format!("{prefix}.router_address"), "cannot use the broadcast address");
                                    } else if router_net.to_string() != extra.to_string() {
                                        errs.add(format!("{prefix}.router_address"), "must be contained in the extra LAN subnet");
                                    }
                                } else {
                                    errs.add(format!("{prefix}.router_address"), "must be contained in the extra LAN subnet");
                                }
                            }
                            _ => errs.add(format!("{prefix}.router_address"), "must be a valid IPv4 interface CIDR"),
                        }
                    }
                }
            }
            let dst = if lan.dst_ip.is_empty() { None } else { parse_ipv4(&lan.dst_ip) };
            match (&dst, &extra_net) {
                (None, _) => errs.add(format!("{prefix}.dst_ip"), "must be a valid IPv4 address"),
                (Some(dst), Some(extra)) if !extra.contains(dst) => errs.add(format!("{prefix}.dst_ip"), "must be inside the extra LAN subnet"),
                (Some(dst), Some(extra)) => {
                    let network_ip = extra.ip.to4().unwrap_or_else(|| extra.ip.clone());
                    if dst.equal(&network_ip) || dst.equal_opt(extra.ipv4_broadcast().as_ref()) {
                        errs.add(format!("{prefix}.dst_ip"), "cannot use the network or broadcast address");
                    }
                    if !lan.router_address.is_empty() {
                        if let Some((router_ip, _)) = net::parse_cidr(&lan.router_address) {
                            if dst.equal(&router_ip) {
                                errs.add(format!("{prefix}.dst_ip"), "cannot be the router address");
                            }
                        }
                    }
                }
                (Some(_), None) => {}
            }
            if lan.dst_port < 1 || lan.dst_port > 65535 {
                errs.add(format!("{prefix}.dst_port"), "must be between 1 and 65535");
            }
            if !matches!(lan.protocol.as_str(), "" | "tcp" | "udp") {
                errs.add(format!("{prefix}.protocol"), "must be tcp, udp, or empty");
            }
            if lan.allow_from.is_empty() {
                errs.add(format!("{prefix}.allow_from"), "must list at least one source CIDR");
            }
            let mut seen_src: HashSet<String> = HashSet::new();
            for src in lan.allow_from.iter() {
                let src = src.trim().to_string();
                let Some((_, src_net)) = net::parse_cidr(&src) else {
                    errs.add(format!("{prefix}.allow_from"), "must be canonical CIDR notation");
                    break;
                };
                // Any subnet fully contained in a trusted source zone is
                // allowed, including a single-device /32 ACL.
                let src_ones = src_net.mask.size().0;
                let mut trusted = false;
                if let Some(zone) = lan_network {
                    trusted = zone.contains(&src_net.ip) && src_ones >= zone.mask.size().0;
                }
                if !trusted {
                    if let Some(zone) = &wg_network {
                        trusted = zone.contains(&src_net.ip) && src_ones >= zone.mask.size().0;
                    }
                }
                if !trusted {
                    errs.add(format!("{prefix}.allow_from"), "only subnets of the LAN and WireGuard networks may reach an extra LAN");
                    break;
                }
                if !seen_src.insert(src) {
                    errs.add(format!("{prefix}.allow_from"), "duplicates a source CIDR");
                    break;
                }
            }
        }
    }

    fn validate_wireguard(&self, errs: &mut ValidationErrors, lan_ip: Option<&IP>, lan_network: Option<&IPNet>) {
        let c = self;
        if !c.wan.enabled {
            errs.add("wireguard.enabled", "requires an enabled WAN connection");
        }
        if c.wireguard.interface != "wg0" {
            errs.add("wireguard.interface", "must be wg0 in this appliance version");
        }
        if !valid_wireguard_key(&c.wireguard.private_key) {
            errs.add("wireguard.private_key", "must be a valid 32-byte WireGuard key");
        }
        if c.wireguard.listen_port < 1024 || c.wireguard.listen_port > 65535 || c.wireguard.listen_port == c.system.https_port {
            errs.add("wireguard.listen_port", "must be a non-management port between 1024 and 65535");
        }
        let parsed = net::parse_cidr(&c.wireguard.address);
        let host_part = c.wireguard.address.split('/').next().unwrap_or("");
        let wg_network = match &parsed {
            Some((wg_ip, wg_net)) if wg_ip.to4().is_some() && net::parse_ip(host_part).is_some_and(|h| wg_ip.equal(&h)) => {
                if lan_network.is_some_and(|n| n.contains(wg_ip)) || wg_net.contains_opt(lan_ip) && lan_network.is_some() {
                    errs.add("wireguard.address", "must not overlap the LAN subnet");
                }
                Some(wg_net.clone())
            }
            _ => {
                errs.add("wireguard.address", "must be a valid IPv4 interface CIDR");
                parsed.as_ref().map(|(_, n)| n.clone())
            }
        };
        let mut seen_peer_ips: HashSet<String> = HashSet::new();
        for (i, peer) in c.wireguard.peers.iter().enumerate() {
            if !peer.enabled {
                continue;
            }
            if !SAFE_NAME_PATTERN.is_match(&peer.name) {
                errs.add(format!("wireguard.peers[{}].name", i), "contains unsupported characters");
            }
            if !valid_wireguard_key(&peer.public_key) {
                errs.add(format!("wireguard.peers[{}].public_key", i), "must be a valid 32-byte WireGuard key");
            }
            if !peer.preshared_key.is_empty() && !valid_wireguard_key(&peer.preshared_key) {
                errs.add(format!("wireguard.peers[{}].preshared_key", i), "must be a valid 32-byte WireGuard key");
            }
            if peer.allowed_ips.len() != 1 {
                errs.add(format!("wireguard.peers[{}].allowed_ips", i), "must contain exactly one IPv4 /32 address");
            }
            for (j, allowed) in peer.allowed_ips.iter().enumerate() {
                let field = format!("wireguard.peers[{}].allowed_ips[{}]", i, j);
                let parsed = net::parse_cidr(allowed);
                let ok = match &parsed {
                    Some((ip, network)) => ip.to4().is_some() && network.mask.size().0 == 32 && !wg_network.as_ref().is_some_and(|n| !n.contains(ip)),
                    None => false,
                };
                if !ok {
                    errs.add(field, "must be an IPv4 /32 address inside the WireGuard subnet");
                    continue;
                }
                let key = parsed.unwrap().1.to_string();
                if !seen_peer_ips.insert(key) {
                    errs.add(field, "duplicates another peer route");
                }
            }
            if !peer.endpoint.is_empty() {
                // Endpointless peers are valid for a server: they dial in.
                let valid = net::split_host_port(&peer.endpoint).ok().is_some_and(|(host, _)| net::parse_ip(&host).is_some());
                if !valid {
                    errs.add(format!("wireguard.peers[{}].endpoint", i), "must be a static IPv4/IPv6 address in host:port form; hostnames are not resolved");
                }
            }
        }
    }
}

/// Enforces the outbound WireGuard tunnel (wg1) contract: a valid key pair, an
/// explicit remote endpoint, and remote networks that do not overlap any
/// local network. Full-tunnel is not supported: entries broader than a /8 are
/// rejected.
fn validate_wg_client(c: &SystemConfig, _lan_ip: Option<&IP>, lan_network: Option<&IPNet>, errs: &mut ValidationErrors) {
    let client = &c.wg_client;
    if !client.enabled {
        return;
    }
    if client.interface != "wg1" {
        errs.add("wg_client.interface", "must be wg1 in this appliance version");
    }
    if !valid_wireguard_key(&client.private_key) {
        errs.add("wg_client.private_key", "must be a valid 32-byte WireGuard key");
    }
    if !valid_wireguard_key(&client.public_key) {
        errs.add("wg_client.public_key", "must be a valid 32-byte WireGuard key");
    }
    if !client.private_key.is_empty() && client.public_key == client.private_key {
        errs.add("wg_client.public_key", "must not match the private key");
    }
    if !client.preshared_key.is_empty() && !valid_wireguard_key(&client.preshared_key) {
        errs.add("wg_client.preshared_key", "must be a valid 32-byte WireGuard key");
    }
    let mut client_address_net: Option<IPNet> = None;
    if !client.address.is_empty() {
        match net::parse_cidr(&client.address) {
            Some((client_ip, client_net)) if client_ip.to4().is_some() => {
                if lan_network.is_some_and(|n| networks_overlap(n, &client_net)) {
                    errs.add("wg_client.address", "must not overlap the LAN subnet");
                } else if wireguard_server_network(c).is_some_and(|n| networks_overlap(&n, &client_net)) {
                    errs.add("wg_client.address", "must not overlap the WireGuard server subnet");
                } else if client_net.mask.size().0 != 32 {
                    // The outbound tunnel is a point-to-point client.
                    errs.add("wg_client.address", "must be a /32 address in this appliance version");
                }
                client_address_net = Some(client_net);
            }
            Some((_, client_net)) => {
                errs.add("wg_client.address", "must be a valid IPv4 interface CIDR");
                client_address_net = Some(client_net);
            }
            None => errs.add("wg_client.address", "must be a valid IPv4 interface CIDR"),
        }
    }
    match net::split_host_port(&client.endpoint) {
        Ok((host, port_text)) if !host.is_empty() => {
            if let Some(parsed) = net::parse_ip(&host) {
                if parsed.to4().is_none() {
                    errs.add("wg_client.endpoint", "IPv6 endpoints are unavailable while IPv6 is disabled");
                }
            } else if !WIREGUARD_ENDPOINT_HOST_PATTERN.is_match(&host) || host.contains("..") {
                errs.add("wg_client.endpoint", "contains an invalid hostname");
            }
            match go_atoi(&port_text) {
                Some(port) if (1..=65535).contains(&port) => {}
                _ => errs.add("wg_client.endpoint", "port must be between 1 and 65535"),
            }
        }
        _ => errs.add("wg_client.endpoint", "must use host:port format"),
    }
    if client.allowed_ips.is_empty() {
        errs.add("wg_client.allowed_ips", "must list at least one remote network");
    }
    let endpoint_ip = net::split_host_port(&client.endpoint).ok().and_then(|(host, _)| net::parse_ip(&host));
    let dns_upstreams: Vec<IP> = c.dhcp.dns_servers.iter().filter_map(|server| net::parse_ip(server.trim())).filter(|ip| ip.to4().is_some()).collect();
    let mut seen_nets: HashSet<String> = HashSet::new();
    for (i, entry) in client.allowed_ips.iter().enumerate() {
        let field = format!("wg_client.allowed_ips[{}]", i);
        let network = match net::parse_cidr(entry.trim()) {
            Some((ip, network)) if ip.to4().is_some() => network,
            _ => {
                errs.add(field, "must be a valid IPv4 CIDR network");
                continue;
            }
        };
        if network.mask.size().0 < 8 {
            errs.add(field.clone(), "must not be broader than a /8: default-route capture (0.0.0.0/0 and /1 split tricks) is not supported");
        }
        // The tunnel must never capture the peer endpoint itself.
        if endpoint_ip.as_ref().is_some_and(|ip| network.contains(ip)) {
            errs.add(field.clone(), "must not contain the peer endpoint address; the endpoint needs a real route outside the tunnel");
        }
        // Capturing a configured DNS upstream would silently reroute every
        // resolution through the tunnel.
        if dns_upstreams.iter().any(|upstream| network.contains(upstream)) {
            errs.add(field.clone(), "must not contain a configured DNS upstream; the resolver path needs a real route outside the tunnel");
        }
        if lan_network.is_some_and(|n| networks_overlap(n, &network)) {
            errs.add(field.clone(), "must not overlap the LAN subnet");
        }
        if wireguard_server_network(c).is_some_and(|n| networks_overlap(&n, &network)) {
            errs.add(field.clone(), "must not overlap the WireGuard server subnet");
        }
        if client_address_net.as_ref().is_some_and(|n| networks_overlap(n, &network)) {
            errs.add(field.clone(), "must not overlap the tunnel's own address");
        }
        for lan in c.firewall.extra_lans.iter() {
            if !lan.enabled || lan.cidr.is_empty() {
                continue;
            }
            if let Some((_, extra)) = net::parse_cidr(&lan.cidr) {
                if networks_overlap(&extra, &network) {
                    errs.add(field.clone(), "must not overlap an extra LAN subnet");
                    break;
                }
            }
        }
        if !seen_nets.insert(network.to_string()) {
            errs.add(field, "duplicates another allowed network");
        }
    }
    if client.persistent_keepalive < 0 || client.persistent_keepalive > 65535 {
        errs.add("wg_client.persistent_keepalive", "must be between 0 and 65535");
    }
    if has_unsafe_control(&client.endpoint) || client.endpoint.len() > 255 {
        errs.add("wg_client.endpoint", "contains forbidden characters or is too long");
    }
}

/// The canonical wg0 network when the server tunnel is enabled.
pub(crate) fn wireguard_server_network(c: &SystemConfig) -> Option<IPNet> {
    if !c.wireguard.enabled || c.wireguard.address.is_empty() {
        return None;
    }
    net::parse_cidr(&c.wireguard.address).map(|(_, n)| n)
}

/// `strconv.Atoi`: optional sign followed by decimal digits only.
pub fn go_atoi(text: &str) -> Option<i64> {
    let digits = text.strip_prefix(['+', '-']).unwrap_or(text);
    if digits.is_empty() || !digits.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    text.parse::<i64>().ok()
}
