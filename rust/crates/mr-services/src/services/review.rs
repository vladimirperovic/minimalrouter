//! Firewall rule counters, policy explanations, schedule status and
//! operator-facing change summaries.

use chrono::{Datelike, Duration as ChronoDuration, TimeZone, Timelike};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, HashMap};

use super::device_profiles::{active_managed_services, effective_day_windows};
use crate::config::{AccessWindow, FirewallConfig, FirewallRule, Revision, SystemConfig, equal_section, firewall_rule_scope};
use crate::errorf;
use crate::goish::errors::Result;
use crate::goish::json::{self, List};
use crate::goish::net;
use crate::goish::time::Time;

/// A semantic identity, so counters for an edited rule are never attributed to
/// its old match. Names and preset provenance do not affect matching.
pub fn firewall_rule_counter(rule: &FirewallRule) -> String {
    let mut rule = rule.clone();
    rule.name.clear();
    rule.preset_id.clear();
    rule.scope = firewall_rule_scope(&rule).to_string();
    let data = json::marshal(&rule).unwrap_or_default();
    let sum = Sha256::digest(&data);
    format!("mr_rule_{}", hex::encode(&sum[..12]))
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FirewallFlow {
    pub source_ip: String,
    pub destination_ip: String,
    pub protocol: String,
    pub port: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct FirewallExplanation {
    pub revision: Revision,
    pub checked_at: Time,
    pub basis: String,
    pub verdict: String,
    pub reason: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub rule_id: String,
    pub source_zone: String,
    pub destination_zone: String,
    pub limitations: List<String>,
}

fn in_network(address: &str, cidr: &str) -> bool {
    match (net::parse_cidr(cidr), net::parse_ip(address)) {
        (Some((_, n)), Some(ip)) => n.contains(&ip),
        _ => false,
    }
}

fn first_part(value: &str) -> &str {
    value.split('/').next().unwrap_or("")
}

fn firewall_zone(cfg: &SystemConfig, ip: &str) -> String {
    if ip == cfg.lan.ip_address || (cfg.wireguard.enabled && first_part(&cfg.wireguard.address) == ip) {
        return "router".into();
    }
    if cfg.firewall.extra_lans.iter().any(|lan| lan.enabled && first_part(&lan.router_address) == ip) {
        return "router".into();
    }
    if in_network(ip, &cfg.lan.cidr) {
        return "lan".into();
    }
    if cfg.wireguard.enabled && in_network(ip, &cfg.wireguard.address) {
        return "wireguard".into();
    }
    for lan in cfg.firewall.extra_lans.iter() {
        if lan.enabled && in_network(ip, &lan.cidr) {
            return format!("segment:{}", lan.id);
        }
    }
    if cfg.wg_client.enabled && cfg.wg_client.allowed_ips.iter().any(|cidr| in_network(ip, cidr)) {
        return "tunnel".into();
    }
    if net::parse_ip(ip).is_some_and(|p| p.is_global_unicast() && !p.is_private()) {
        return "wan".into();
    }
    "unknown".into()
}

/// Evaluates NEW IPv4 flows under the configured topology. This is a policy
/// explanation, never a reachability or packet probe.
pub fn explain_firewall_flow(cfg: &SystemConfig, flow: &FirewallFlow) -> Result<FirewallExplanation, (FirewallExplanation, crate::goish::errors::Error)> {
    let mut result = FirewallExplanation {
        revision: cfg.revision,
        checked_at: Time::now_utc(),
        basis: "configured_policy".into(),
        verdict: "unknown".into(),
        limitations: List::from(vec![
            "New IPv4 flow; source interface inferred from configured subnets, not observed.".to_string(),
            "Not a packet test. Live routes, conntrack, rate limits, application access and other firewall tables are not verified.".to_string(),
        ]),
        ..Default::default()
    };
    for value in [&flow.source_ip, &flow.destination_ip] {
        if !net::parse_ip(value).is_some_and(|ip| ip.to4().is_some()) {
            return Err((result, errorf!("source_ip and destination_ip must be IPv4 addresses")));
        }
    }
    if !matches!(flow.protocol.as_str(), "tcp" | "udp" | "icmp") {
        return Err((result, errorf!("protocol must be tcp, udp or icmp")));
    }
    if (flow.protocol == "icmp" && flow.port != 0) || (flow.protocol != "icmp" && (flow.port < 1 || flow.port > 65535)) {
        return Err((result, errorf!("port must be 1-65535 for TCP/UDP and zero for ICMP")));
    }
    let mut flow = flow.clone();
    result.source_zone = firewall_zone(cfg, &flow.source_ip);
    result.destination_zone = firewall_zone(cfg, &flow.destination_ip);
    let mut dnat = String::new();
    // Prerouting rewrites the destination before input/forward policy.
    if result.source_zone == "wireguard" {
        if let Some(pf) =
            cfg.firewall.port_forwards.iter().find(|pf| pf.enabled && pf.external_port == flow.port && (pf.protocol == flow.protocol || pf.protocol == "both"))
        {
            dnat = format!("Tunnel DNAT maps to {}:{}. ", pf.internal_ip, pf.internal_port);
            flow.destination_ip = pf.internal_ip.clone();
            flow.port = pf.internal_port;
            result.destination_zone = firewall_zone(cfg, &flow.destination_ip);
        }
    }
    let verdict = explain_verdict(cfg, &flow, &mut result);
    result.verdict = verdict.0.into();
    result.reason = format!("{}{}", dnat, verdict.1);
    Ok(result)
}

fn explain_verdict(cfg: &SystemConfig, flow: &FirewallFlow, result: &mut FirewallExplanation) -> (&'static str, &'static str) {
    let src = result.source_zone.clone();
    let dst = result.destination_zone.clone();
    if src == "unknown" || dst == "unknown" || src == "router" {
        return ("unknown", "This address needs live routing/interface context outside the supported configured topology.");
    }
    if src == dst && (src == "lan" || src.starts_with("segment:")) {
        return ("not_routed", "Devices in the same configured subnet normally communicate directly; router forwarding rules do not protect that path.");
    }
    if src == "wan" {
        return (
            "unknown",
            "WAN anti-spoof routing checks and WireGuard endpoint rate limits require live ingress context. New non-WireGuard WAN traffic is denied by configured policy.",
        );
    }
    if dst == "router" {
        if src == "tunnel" {
            return ("deny", "The outbound tunnel cannot initiate connections to the router.");
        }
        if src.starts_with("segment:") {
            if flow.protocol == "icmp" {
                return ("allow", "Isolated segments may ping the router.");
            }
            return ("deny", "Isolated segments cannot reach router services.");
        }
        if flow.port == 53 && flow.protocol != "icmp" {
            return ("allow", "Router DNS is permitted from LAN and authenticated WireGuard; listener availability is not tested.");
        }
        if flow.protocol == "tcp"
            && (flow.port == 22 || flow.port == cfg.system.https_port)
            && (src == "wireguard" || cfg.system.management_access != "wireguard_only")
        {
            return ("allow", "Management port is allowed by packet policy; trusted networks and authentication still apply.");
        }
        if src == "lan" {
            if flow.protocol == "icmp" || (flow.protocol == "udp" && flow.port == 67) {
                return ("allow", "Essential LAN router service is permitted.");
            }
            let proxy_port = if cfg.squid_proxy.port == 0 { 3128 } else { cfg.squid_proxy.port };
            if cfg.squid_proxy.enabled && flow.protocol == "tcp" && flow.port == proxy_port {
                return ("allow", "LAN access to the configured proxy listener is permitted.");
            }
            if let Some(rule) =
                cfg.firewall.custom_rules.iter().find(|r| r.enabled && r.direction == "input" && r.action == "allow" && firewall_rule_matches(r, flow))
            {
                result.rule_id = rule.id.clone();
                return ("allow", "Matching custom router-input allowance.");
            }
        }
        return ("deny", "No router-input allowance matches this new flow.");
    }
    // Device schedules precede custom denies and may depend on dynamic nft sets.
    if !active_managed_services(cfg).is_empty() {
        for profile in cfg.adguard.device_profiles.iter() {
            if profile.enabled && profile.ip_addresses.iter().any(|ip| *ip == flow.source_ip) {
                if flow.protocol != "icmp" && (flow.port == 53 || flow.port == 853) {
                    return ("deny", "Device schedules block direct DNS/DoT before custom rules.");
                }
                return (
                    "unknown",
                    "This device has a schedule. Its dynamic DNS destination sets and router-time enforcement must be inspected before deciding.",
                );
            }
        }
    }
    if src == "lan" {
        if let Some(rule) = cfg.firewall.custom_rules.iter().find(|r| {
            r.enabled
                && r.direction == "forward"
                && r.action == "deny"
                && firewall_rule_matches(r, flow)
                && (firewall_rule_scope(r) == "all" || (dst == "wan" && cfg.wan.enabled))
        }) {
            result.rule_id = rule.id.clone();
            return ("deny", "Matching custom deny runs before established connections and all custom allows.");
        }
    }
    if cfg.squid_proxy.enabled && cfg.squid_proxy.restricted_ips.iter().any(|ip| ip.enabled && ip.ip_address == flow.source_ip) {
        return ("deny", "Proxy restriction blocks direct forwarded traffic from this address.");
    }
    if src == "tunnel" {
        return ("deny", "The outbound tunnel may only return traffic; new remote initiations are denied.");
    }
    if src.starts_with("segment:") {
        return ("deny", "Isolated segments cannot initiate routed connections.");
    }
    if dst.starts_with("segment:") {
        for lan in cfg.firewall.extra_lans.iter() {
            if lan.enabled
                && flow.destination_ip == lan.dst_ip
                && flow.port == lan.dst_port
                && (flow.protocol == lan.protocol || (lan.protocol.is_empty() && flow.protocol == "tcp"))
                && lan.allow_from.iter().any(|src| in_network(&flow.source_ip, src))
            {
                return ("allow", "The isolated-segment service ACL permits this source, destination and port.");
            }
        }
        return ("deny", "The isolated-segment service ACL does not allow this flow.");
    }
    if dst == "wan" && cfg.wan.enabled && (src == "lan" || src == "wireguard") {
        if src == "lan" {
            if let Some(rule) =
                cfg.firewall.custom_rules.iter().find(|r| r.enabled && r.direction == "forward" && r.action == "allow" && firewall_rule_matches(r, flow))
            {
                result.rule_id = rule.id.clone();
                return ("allow", "Matching custom WAN allowance, after earlier restrictions. Default LAN-to-WAN egress also permits this path.");
            }
        }
        return ("allow", "Default configured egress allows this source toward WAN, after earlier restrictions.");
    }
    if src == "wireguard" && dst == "lan" {
        return ("allow", "Authenticated WireGuard clients may reach the LAN; DNAT is not an access-control list.");
    }
    if dst == "tunnel" && (src == "lan" || src == "wireguard") {
        return ("allow", "Trusted LAN/WireGuard sources may initiate toward the configured outbound tunnel.");
    }
    ("deny", "No forwarding allowance matches this new flow.")
}

fn firewall_rule_matches(rule: &FirewallRule, flow: &FirewallFlow) -> bool {
    if !rule.src_ip.is_empty() && rule.src_ip != flow.source_ip && !in_network(&flow.source_ip, &rule.src_ip) {
        return false;
    }
    rule.protocol == "any" || (rule.protocol == flow.protocol && (rule.protocol == "icmp" || rule.dst_port == flow.port))
}

pub fn firewall_warnings(cfg: &SystemConfig) -> Vec<String> {
    let mut out = Vec::new();
    let rules = &cfg.firewall.custom_rules;
    for (i, r) in rules.iter().enumerate() {
        if !r.enabled {
            continue;
        }
        if r.direction == "forward" && r.action == "allow" {
            out.push(format!("{}: WAN egress is already allowed by default; an allow cannot override an earlier deny or device schedule.", r.name));
        }
        if r.direction == "forward" && firewall_rule_scope(r) == "all" {
            out.push(format!("{}: applies to every routed destination from LAN, including tunnels and isolated segments.", r.name));
        }
        if let Some(other) = rules[..i].iter().find(|o| {
            o.enabled
                && o.direction == r.direction
                && o.protocol == r.protocol
                && o.src_ip == r.src_ip
                && o.dst_port == r.dst_port
                && firewall_rule_scope(o) == firewall_rule_scope(r)
        }) {
            out.push(format!("{} and {} overlap: denies are evaluated before allows; identical matches may be redundant.", other.name, r.name));
        }
        if out.len() >= 30 {
            out.push("Additional warnings omitted; review all configured rules.".into());
            break;
        }
    }
    out
}

pub fn describe_firewall_changes(before: &FirewallConfig, after: &FirewallConfig) -> Vec<String> {
    let mut changes = Vec::new();
    let describe = |r: &FirewallRule| {
        format!(
            "{}: {} {} {}/{} from {}, scope {}, enabled={}",
            r.name,
            r.action,
            r.direction,
            r.protocol,
            r.dst_port,
            r.src_ip,
            firewall_rule_scope(r),
            r.enabled
        )
    };
    let mut old: HashMap<&str, &FirewallRule> = before.custom_rules.iter().map(|r| (r.id.as_str(), r)).collect();
    for r in after.custom_rules.iter() {
        match old.get(r.id.as_str()) {
            None => changes.push(format!("Add {}", describe(r))),
            Some(prev) if !equal_section(*prev, r) => changes.push(format!("Change {} → {}", describe(prev), describe(r))),
            _ => {}
        }
        old.remove(r.id.as_str());
    }
    for r in before.custom_rules.iter() {
        if old.contains_key(r.id.as_str()) {
            changes.push(format!("Remove {}", describe(r)));
        }
    }
    let mut pf: HashMap<&str, _> = before.port_forwards.iter().map(|r| (r.id.as_str(), r)).collect();
    for r in after.port_forwards.iter() {
        let prev = pf.get(r.id.as_str());
        let label = if prev.is_some() { "Change" } else { "Add" };
        if prev.is_none_or(|p| !equal_section(*p, r)) {
            changes.push(format!(
                "{} tunnel forward {}: {}/{} → {}:{}, enabled={}",
                label, r.name, r.protocol, r.external_port, r.internal_ip, r.internal_port, r.enabled
            ));
        }
        pf.remove(r.id.as_str());
    }
    for r in before.port_forwards.iter() {
        if pf.contains_key(r.id.as_str()) {
            changes.push(format!("Remove tunnel forward {}", r.name));
        }
    }
    if !equal_section(&before.extra_lans, &after.extra_lans) {
        changes.push("Isolated-segment access policy changes; review source networks and destination services.".into());
    }
    if changes.len() > 50 {
        changes.truncate(50);
        changes.push("Additional changes omitted; reduce the batch for a complete review.".into());
    }
    changes
}

/// Operator-facing deltas without credentials.
pub fn describe_network_changes(current: &SystemConfig, next: &SystemConfig) -> Vec<String> {
    let mut changes = Vec::new();
    let field = |changes: &mut Vec<String>, name: &str, a: String, b: String| {
        if a != b {
            changes.push(format!("{}: {} → {}", name, a, b));
        }
    };
    field(&mut changes, "LAN gateway", current.lan.ip_address.clone(), next.lan.ip_address.clone());
    field(&mut changes, "LAN interface", current.lan.interface.clone(), next.lan.interface.clone());
    field(&mut changes, "DHCP enabled", current.dhcp.enabled.to_string(), next.dhcp.enabled.to_string());
    field(&mut changes, "Pool start", current.dhcp.range_start.clone(), next.dhcp.range_start.clone());
    field(&mut changes, "Pool end", current.dhcp.range_end.clone(), next.dhcp.range_end.clone());
    field(&mut changes, "Lease duration", current.dhcp.lease_time.clone(), next.dhcp.lease_time.clone());
    field(&mut changes, "Upstream DNS", current.dhcp.dns_servers.join(", "), next.dhcp.dns_servers.join(", "));
    field(&mut changes, "WAN enabled", current.wan.enabled.to_string(), next.wan.enabled.to_string());
    field(&mut changes, "WAN interface", current.wan.interface.clone(), next.wan.interface.clone());
    field(&mut changes, "WAN MTU", current.wan.mtu.to_string(), next.wan.mtu.to_string());
    if current.wan.username != next.wan.username {
        changes.push("PPPoE username: changed".into());
    }
    if current.wan.password != next.wan.password {
        changes.push("PPPoE password: changed (hidden)".into());
    }
    let mut old_leases: HashMap<&str, _> = current.dhcp.static_leases.iter().map(|l| (l.id.as_str(), l)).collect();
    for lease in next.dhcp.static_leases.iter() {
        match old_leases.get(lease.id.as_str()) {
            None => changes.push(format!("Reservation added: {} · {} → {}", lease.hostname, lease.mac, lease.ip_address)),
            Some(before) if *before != lease => changes.push(format!(
                "Reservation changed: {} / {} / {} → {} / {} / {}",
                before.hostname, before.mac, before.ip_address, lease.hostname, lease.mac, lease.ip_address
            )),
            _ => {}
        }
        old_leases.remove(lease.id.as_str());
    }
    for lease in current.dhcp.static_leases.iter() {
        if old_leases.contains_key(lease.id.as_str()) {
            changes.push(format!("Reservation removed: {} · {}", lease.hostname, lease.ip_address));
        }
    }
    let mut old_records: HashMap<&str, &str> = current.dns.records.iter().map(|r| (r.name.as_str(), r.ip.as_str())).collect();
    for record in next.dns.records.iter() {
        match old_records.get(record.name.as_str()) {
            None => changes.push(format!("Local DNS added: {} → {}", record.name, record.ip)),
            Some(before) => field(&mut changes, &format!("Local DNS {}", record.name), before.to_string(), record.ip.clone()),
        }
        old_records.remove(record.name.as_str());
    }
    for record in current.dns.records.iter() {
        if old_records.contains_key(record.name.as_str()) {
            changes.push(format!("Local DNS removed: {} → {}", record.name, record.ip));
        }
    }
    if changes.len() > 50 {
        let extra = changes.len() - 50;
        changes.truncate(50);
        changes.push(format!("{} additional network changes; review the full candidate before applying.", extra));
    }
    changes
}

/// Explains configuration at the router clock. It does not claim that a
/// destination set or an actual packet was inspected.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct ProfileStatus {
    pub id: String,
    pub name: String,
    pub ip_addresses: Vec<String>,
    pub services: Vec<String>,
    pub state: String,
    pub allowed_now: Option<bool>,
    pub next_change_at: Option<Time>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub next_state: String,
    pub missing_reservations: Vec<String>,
    pub basis: String,
    pub enforcement_verified: bool,
}

/// `strconv.Atoi` of each HH:MM half, ignoring errors as the Go code does.
fn schedule_minute(value: &str) -> i64 {
    let mut parts = value.split(':');
    let hour = parts.next().and_then(|h| crate::config::go_atoi(h)).unwrap_or(0);
    let minute = parts.next().and_then(|m| crate::config::go_atoi(m)).unwrap_or(0);
    hour * 60 + minute
}

fn weekday_name(t: &chrono::DateTime<chrono::FixedOffset>) -> &'static str {
    match t.weekday() {
        chrono::Weekday::Mon => "monday",
        chrono::Weekday::Tue => "tuesday",
        chrono::Weekday::Wed => "wednesday",
        chrono::Weekday::Thu => "thursday",
        chrono::Weekday::Fri => "friday",
        chrono::Weekday::Sat => "saturday",
        chrono::Weekday::Sun => "sunday",
    }
}

fn schedule_allowed(windows: &BTreeMap<String, List<AccessWindow>>, now: &chrono::DateTime<chrono::FixedOffset>) -> bool {
    let minute = now.hour() as i64 * 60 + now.minute() as i64;
    windows.get(weekday_name(now)).is_some_and(|ws| {
        ws.iter().any(|w| {
            let end = if w.end == "23:59" { 1440 } else { schedule_minute(&w.end) };
            minute >= schedule_minute(&w.start) && minute < end
        })
    })
}

pub fn device_profile_statuses(cfg: &SystemConfig, now: Time) -> Vec<ProfileStatus> {
    let reserved: std::collections::HashSet<&str> = cfg.dhcp.static_leases.iter().map(|l| l.ip_address.as_str()).collect();
    let now = now.0;
    let tz = *now.offset();
    let mut result = Vec::new();
    for profile in cfg.adguard.device_profiles.iter() {
        let mut status = ProfileStatus {
            id: profile.id.clone(),
            name: profile.name.clone(),
            ip_addresses: profile.ip_addresses.to_vec(),
            services: profile.services.to_vec(),
            basis: "configured_schedule".into(),
            missing_reservations: profile.ip_addresses.iter().filter(|ip| !reserved.contains(ip.as_str())).cloned().collect(),
            ..Default::default()
        };
        if !profile.enabled {
            status.state = "paused".into();
        } else if !cfg.adguard.enabled {
            status.state = "filter_off".into();
        } else {
            let windows = effective_day_windows(&profile.schedule);
            let allowed = schedule_allowed(&windows, &now);
            status.allowed_now = Some(allowed);
            status.state = if allowed { "allowed" } else { "blocked" }.into();
            // Only interval boundaries, including adjacent-day midnight.
            let mut boundaries = Vec::new();
            for offset in 0..=8 {
                let date = now.date_naive() + ChronoDuration::days(offset);
                let Some(day) = tz.from_local_datetime(&date.and_hms_opt(0, 0, 0).unwrap()).single() else { continue };
                boundaries.push(day);
                for window in windows.get(weekday_name(&day)).map(|w| w.as_slice()).unwrap_or(&[]) {
                    for mut minute in [schedule_minute(&window.start), schedule_minute(&window.end)] {
                        if minute == 1439 && window.end == "23:59" {
                            minute = 1440;
                        }
                        boundaries.push(day + ChronoDuration::minutes(minute));
                    }
                }
            }
            boundaries.sort();
            if let Some(at) = boundaries.into_iter().find(|at| *at > now && schedule_allowed(&windows, at) != allowed) {
                status.next_change_at = Some(Time(at));
                status.next_state = if allowed { "blocked" } else { "allowed" }.into();
            }
        }
        result.push(status);
    }
    result
}
