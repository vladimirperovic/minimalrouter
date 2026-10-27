//! Operator-facing appliance health: a pure classification of telemetry the
//! management plane already collects, plus read-only runtime facts.

use serde::{Deserialize, Serialize};
use std::time::{Duration as StdDuration, Instant};

use crate::apply::EngineStatus;
use crate::config::SystemConfig;
use crate::gateway;
use crate::goish::json::is_zero_i64;
use crate::goish::time::{Duration, Time};
use crate::storage::PressureLevel;
use crate::telemetry::RuntimeStatus;

pub type State = String;
pub const STATE_HEALTHY: &str = "healthy";
pub const STATE_WARNING: &str = "warning";
pub const STATE_DEGRADED: &str = "degraded";
pub const STATE_RECOVERY_REQUIRED: &str = "recovery_required";
pub const STATE_UNKNOWN: &str = "unknown";

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Check {
    pub id: String,
    pub label: String,
    pub state: State,
    pub summary: String,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Snapshot {
    pub state: State,
    pub headline: String,
    pub checks: Vec<Check>,
    pub generated_at: Time,
}

#[derive(Clone, Debug, Default)]
pub struct Input {
    pub config: SystemConfig,
    pub runtime: RuntimeStatus,
    pub engine: EngineStatus,
    pub gateway: gateway::Summary,
    pub gateway_configured: bool,
    pub update_trust_configured: bool,
    pub facts: RuntimeFacts,
    pub last_backup_at: Option<Time>,
    pub backup_history_unavailable: bool,
    pub dns_resolves: Option<bool>,
    pub dns_error: String,
    pub now: Time,
    pub firewall: Option<Check>,
}

pub fn build(input: &Input) -> Snapshot {
    let now = if input.now.is_zero() { Time::now_utc() } else { input.now };
    let mut checks: Vec<Check> = Vec::with_capacity(14);
    let mut add = |id: &str, label: &str, state: &str, summary: String| {
        checks.push(Check { id: id.into(), label: label.into(), state: state.into(), summary });
    };
    if let Some(firewall) = &input.firewall {
        add(&firewall.id, &firewall.label, &firewall.state, firewall.summary.clone());
    }

    if input.engine.recovery_required {
        let reason = if input.engine.recovery_reason.is_empty() { "Canonical reconciliation is required before configuration changes.".to_string() } else { input.engine.recovery_reason.clone() };
        add("recovery", "Recovery", STATE_RECOVERY_REQUIRED, reason);
    } else if input.engine.applying {
        add("recovery", "Configuration engine", STATE_WARNING, "A configuration transaction is currently being applied.".into());
    } else {
        add("recovery", "Configuration engine", STATE_HEALTHY, "Canonical configuration is available for normal operation.".into());
    }

    let storage = &input.runtime.storage;
    if !storage.available {
        add("storage", "Storage", STATE_UNKNOWN, "Filesystem pressure telemetry is unavailable.".into());
    } else if storage.level == PressureLevel::Critical {
        add("storage", "Storage", STATE_DEGRADED, format!("Disk is {:.1}% used; durable mutations are blocked.", storage.usage_percent));
    } else if storage.level == PressureLevel::Warning {
        add("storage", "Storage", STATE_WARNING, format!("Disk is {:.1}% used; free space should be reclaimed.", storage.usage_percent));
    } else {
        add("storage", "Storage", STATE_HEALTHY, format!("Disk usage is {:.1}%.", storage.usage_percent));
    }

    if input.runtime.memory_total_bytes == 0 {
        add("memory", "Memory", STATE_UNKNOWN, "Memory telemetry is unavailable.".into());
    } else {
        let percent = input.runtime.memory_used_bytes as f64 / input.runtime.memory_total_bytes as f64 * 100.0;
        let state = if percent >= 90.0 { STATE_DEGRADED } else if percent >= 80.0 { STATE_WARNING } else { STATE_HEALTHY };
        add("memory", "Memory", state, format!("Memory usage is {:.1}%.", percent));
    }

    if input.runtime.conntrack_max == 0 {
        add("conntrack", "Connection tracking", STATE_UNKNOWN, "Conntrack telemetry is unavailable.".into());
    } else {
        let usage = input.runtime.conntrack_usage_percent;
        let state = if usage >= 90.0 { STATE_DEGRADED } else if usage >= 75.0 { STATE_WARNING } else { STATE_HEALTHY };
        add("conntrack", "Connection tracking", state, format!("Conntrack table is {:.1}% full.", usage));
    }

    if !input.runtime.available {
        add("time", "Time synchronization", STATE_UNKNOWN, "Runtime time telemetry is unavailable.".into());
    } else if input.runtime.time_synchronized {
        add("time", "Time synchronization", STATE_HEALTHY, "Kernel clock reports synchronized time.".into());
    } else {
        add("time", "Time synchronization", STATE_WARNING, "Clock is not synchronized; TOTP, TLS and signed-update checks may be affected.".into());
    }

    if !input.config.wan.enabled {
        add("gateway", "WAN / gateway", STATE_HEALTHY, "WAN is intentionally disabled.".into());
    } else if !input.runtime.wan_connected {
        add("gateway", "WAN / gateway", STATE_DEGRADED, "WAN is enabled but no connected IPv4 PPP interface is reported.".into());
    } else if !input.gateway_configured || !input.gateway.available || !input.gateway.enabled {
        add("gateway", "WAN / gateway", STATE_WARNING, "WAN is connected but active gateway-quality monitoring is unavailable or disabled.".into());
    } else {
        let (state, summary) = match input.gateway.state.as_str() {
            "healthy" => (STATE_HEALTHY, "Gateway quality is healthy."),
            "degraded" => (STATE_WARNING, "Gateway quality is degraded."),
            "flapping" => (STATE_WARNING, "PPPoE is reconnecting frequently."),
            "offline" => (STATE_DEGRADED, "Gateway monitoring reports the WAN offline."),
            _ => (STATE_UNKNOWN, "Gateway health has not produced a stable classification yet."),
        };
        add("gateway", "WAN / gateway", state, summary.into());
    }

    let facts = &input.facts;
    if !facts.available {
        add("services", "Core services", STATE_UNKNOWN, "Installed-appliance service telemetry is unavailable.".into());
    } else {
        if facts.routerd_healthy && facts.applyd_healthy && facts.apply_socket_available {
            add("services", "Core services", STATE_HEALTHY, "routerd and router-applyd supervision are healthy.".into());
        } else {
            add("services", "Core services", STATE_DEGRADED, "A supervised router process or the privileged apply socket is unavailable; inspect crash-loop/service state.".into());
        }

        // DNS remains enabled independently of the DHCP allocator.
        match input.dns_resolves {
            _ if !facts.dnsmasq_started => add("dns_dhcp", "DNS / DHCP", STATE_DEGRADED, "DNS/DHCP is configured but dnsmasq is not started.".into()),
            Some(false) => {
                let detail = if input.dns_error.is_empty() { String::new() } else { format!(": {}", input.dns_error) };
                add("dns_dhcp", "DNS / DHCP", STATE_DEGRADED, format!("dnsmasq is started but cannot resolve public names{}", detail));
            }
            None => add("dns_dhcp", "DNS / DHCP", STATE_UNKNOWN, "dnsmasq is started; DNS resolution has not been observed.".into()),
            Some(true) => add("dns_dhcp", "DNS / DHCP", STATE_HEALTHY, "dnsmasq is started and resolves public names.".into()),
        }

        if input.config.wan.enabled {
            if facts.pppoe_started {
                add("pppoe_service", "PPPoE service", STATE_HEALTHY, "The PPPoE OpenRC service is started.".into());
            } else {
                add("pppoe_service", "PPPoE service", STATE_DEGRADED, "WAN is enabled but the PPPoE OpenRC service is not started.".into());
            }
        }

        if input.config.wireguard.enabled {
            if facts.wireguard_interface_up {
                add("wireguard", "WireGuard", STATE_HEALTHY, "The configured WireGuard interface is up.".into());
            } else {
                add("wireguard", "WireGuard", STATE_DEGRADED, "WireGuard is enabled but its interface is unavailable or down.".into());
            }
        }

        if input.config.wg_client.enabled {
            // An interface that is up but has never handshaked (or whose
            // handshake is stale) is not a connected tunnel.
            let (state, summary) = if !facts.wireguard_client_interface_up {
                (STATE_DEGRADED, "WireGuard client is enabled but its tunnel interface is unavailable or down.")
            } else if facts.wireguard_client_last_handshake == 0 {
                (STATE_DEGRADED, "The outbound tunnel is up but no handshake has completed; the remote endpoint may be unreachable.")
            } else if now.sub(&Time::from_unix(facts.wireguard_client_last_handshake, 0)) > Duration::minutes(3) {
                (STATE_DEGRADED, "The outbound tunnel handshake is stale; the remote endpoint may be unreachable.")
            } else {
                (STATE_HEALTHY, "The outbound WireGuard client tunnel is connected.")
            };
            add("wireguard-client", "WireGuard client tunnel", state, summary.into());
        }
    }

    if !input.update_trust_configured {
        add("update", "Updates", STATE_WARNING, "The firmware verification trust anchor is not configured.".into());
    } else if facts.update_state_available && !facts.update_pending.is_empty() {
        add("update", "Updates", STATE_WARNING, "A verified update is staged and pending activation.".into());
    } else {
        add("update", "Updates", STATE_HEALTHY, "Signed update trust is configured with no pending activation reported.".into());
    }

    match &input.last_backup_at {
        _ if input.backup_history_unavailable => add("backup", "Encrypted backup", STATE_UNKNOWN, "Backup export history is unavailable.".into()),
        None => add("backup", "Encrypted backup", STATE_WARNING, "No successful encrypted backup export is recorded.".into()),
        Some(last) => {
            let age = now.sub(&last.utc());
            let days = (age.as_secs_f64() / 3600.0 / 24.0) as i64;
            if age > Duration::hours(30 * 24) {
                add("backup", "Encrypted backup", STATE_DEGRADED, format!("Last recorded encrypted backup export was {} days ago.", days));
            } else if age > Duration::hours(7 * 24) {
                add("backup", "Encrypted backup", STATE_WARNING, format!("Last recorded encrypted backup export was {} days ago.", days));
            } else {
                add("backup", "Encrypted backup", STATE_HEALTHY, "A recent encrypted backup export is recorded.".into());
            }
        }
    }

    let overall = checks.iter().fold(STATE_HEALTHY.to_string(), |current, check| if severity(&check.state) > severity(&current) { check.state.clone() } else { current });
    let headline = match overall.as_str() {
        STATE_RECOVERY_REQUIRED => "Recovery is required",
        STATE_DEGRADED => "Appliance is degraded",
        STATE_WARNING => "Appliance has warnings",
        STATE_UNKNOWN => "Appliance health is partially unknown",
        _ => "Appliance is healthy",
    };
    Snapshot { state: overall, headline: headline.into(), checks, generated_at: now.utc() }
}

fn severity(state: &str) -> i32 {
    match state {
        STATE_RECOVERY_REQUIRED => 5,
        STATE_DEGRADED => 4,
        STATE_WARNING => 3,
        STATE_UNKNOWN => 2,
        STATE_HEALTHY => 1,
        _ => 0,
    }
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct RuntimeFacts {
    pub available: bool,
    pub routerd_healthy: bool,
    pub applyd_healthy: bool,
    pub apply_socket_available: bool,
    pub dnsmasq_started: bool,
    pub pppoe_started: bool,
    pub wireguard_interface_up: bool,
    pub wireguard_client_interface_up: bool,
    /// Epoch of the last completed client handshake, or 0 when the tunnel
    /// never handshaked (interface up is not the same as connected).
    #[serde(skip_serializing_if = "is_zero_i64")]
    pub wireguard_client_last_handshake: i64,
    pub update_state_available: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub update_current: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub update_previous: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub update_pending: String,
}

#[cfg(target_os = "linux")]
pub fn inspect_runtime_facts(cfg: &SystemConfig) -> RuntimeFacts {
    use crate::apply::{op, ApplyRequest, Client, UnixClient, DEFAULT_SOCKET_PATH, PROTOCOL_VERSION};
    use std::os::unix::fs::FileTypeExt;
    use std::path::Path;

    fn path_exists(path: &str) -> bool {
        Path::new(path).exists()
    }
    fn service_started(name: &str) -> bool {
        path_exists(&format!("/run/openrc/started/{}", name))
    }
    fn supervised_child_healthy(service: &str) -> bool {
        let Some(pid) = std::fs::read_to_string(format!("/run/{}.supervisor.pid", service)).ok().and_then(|d| crate::config::go_atoi(d.trim())) else {
            return false;
        };
        if pid <= 1 {
            return false;
        }
        let Ok(children) = std::fs::read_to_string(format!("/proc/{}/task/{}/children", pid, pid)) else {
            return false;
        };
        children.split_whitespace().filter_map(crate::config::go_atoi).any(|child| child > 1 && path_exists(&format!("/proc/{}", child)))
    }
    fn interface_up(name: &str) -> bool {
        if name.trim().is_empty() {
            return false;
        }
        std::fs::read_to_string(format!("/sys/class/net/{}/flags", name)).ok().and_then(|f| u32::from_str_radix(f.trim().trim_start_matches("0x"), 16).ok()).is_some_and(|f| f & 0x1 != 0)
    }
    // The dump is read by the privileged helper and returned sanitized:
    // WireGuard dump lines carry private and preshared keys.
    fn read_wireguard_client_handshake() -> i64 {
        let request = ApplyRequest { version: PROTOCOL_VERSION, id: "health-wg1".into(), op: op::wg_tunnel_status(), config: SystemConfig::default(), ..Default::default() };
        match UnixClient::new(DEFAULT_SOCKET_PATH).apply(Instant::now() + StdDuration::from_secs(10), &request) {
            Ok(resp) if resp.success => resp.tunnel_status.map(|t| t.last_handshake).unwrap_or(0),
            _ => 0,
        }
    }

    #[derive(Deserialize, Default)]
    #[serde(default)]
    struct UpdateSlotState {
        current: String,
        previous: String,
        pending: String,
    }

    let mut facts = RuntimeFacts { available: path_exists("/run/openrc"), ..Default::default() };
    if !facts.available {
        return facts;
    }
    facts.routerd_healthy = service_started("routerd") && supervised_child_healthy("routerd");
    facts.applyd_healthy = service_started("router-applyd") && supervised_child_healthy("router-applyd");
    facts.apply_socket_available = std::fs::metadata(DEFAULT_SOCKET_PATH).is_ok_and(|m| m.file_type().is_socket());
    facts.dnsmasq_started = service_started("dnsmasq");
    facts.pppoe_started = service_started("pppoe-wan");
    if cfg.wireguard.enabled {
        facts.wireguard_interface_up = interface_up(&cfg.wireguard.interface);
    }
    if cfg.wg_client.enabled {
        facts.wireguard_client_interface_up = interface_up(&cfg.wg_client.interface);
        facts.wireguard_client_last_handshake = read_wireguard_client_handshake();
    }
    if let Ok(data) = std::fs::read("/var/lib/minimalrouter-update/state.json") {
        if let Ok(state) = crate::goish::json::unmarshal::<UpdateSlotState>(&data) {
            facts.update_state_available = true;
            facts.update_current = state.current;
            facts.update_previous = state.previous;
            facts.update_pending = state.pending;
        }
    }
    facts
}

#[cfg(not(target_os = "linux"))]
pub fn inspect_runtime_facts(_cfg: &SystemConfig) -> RuntimeFacts {
    RuntimeFacts::default()
}

/// A failed host lookup: `NotFound` is a valid negative answer.
#[derive(Debug)]
pub enum LookupError {
    NotFound,
    Other(String),
}

pub trait HostResolver {
    fn lookup_host(&self, deadline: Instant, name: &str) -> Result<Vec<String>, LookupError>;
}

/// Asks the router's own dnsmasq listener to resolve a public name. Process
/// liveness alone cannot prove clients can resolve.
pub fn probe_functional_dns(timeout: StdDuration) -> (bool, Option<String>) {
    let timeout = if timeout.is_zero() { StdDuration::from_secs(4) } else { timeout };
    let nanos = Time::now().unix_nano();
    let probe_name = format!("minimalrouter-health-{}.example.com.", format_base36(nanos));
    probe_with(&LocalResolver, timeout, &probe_name)
}

/// strconv.FormatInt(n, 36) for the non-negative values a clock produces.
fn format_base36(mut n: i64) -> String {
    const DIGITS: &[u8] = b"0123456789abcdefghijklmnopqrstuvwxyz";
    let negative = n < 0;
    let mut out = Vec::new();
    loop {
        out.push(DIGITS[(n % 36).unsigned_abs() as usize]);
        n /= 36;
        if n == 0 {
            break;
        }
    }
    if negative {
        out.push(b'-');
    }
    out.reverse();
    String::from_utf8(out).unwrap_or_default()
}

fn probe_with(resolver: &dyn HostResolver, timeout: StdDuration, probe_name: &str) -> (bool, Option<String>) {
    match resolver.lookup_host(Instant::now() + timeout, probe_name) {
        Ok(addresses) if !addresses.is_empty() => (true, None),
        Ok(_) => (false, Some("local DNS returned no response".into())),
        // A valid NXDOMAIN proves the query reached a recursive upstream; the
        // randomized name bypasses dnsmasq's positive and negative cache.
        Err(LookupError::NotFound) => (true, None),
        Err(LookupError::Other(err)) => (false, Some(err)),
    }
}

/// A single-question A lookup against 127.0.0.1:53 over UDP.
struct LocalResolver;

impl HostResolver for LocalResolver {
    fn lookup_host(&self, deadline: Instant, name: &str) -> Result<Vec<String>, LookupError> {
        let lookup_err = |detail: &str| LookupError::Other(format!("lookup {} on 127.0.0.1:53: {}", name, detail));
        let mut id = [0u8; 2];
        let _ = crate::goish::rand::fill(&mut id);
        let mut query = vec![id[0], id[1], 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0];
        for label in name.trim_end_matches('.').split('.') {
            if label.is_empty() || label.len() > 63 {
                return Err(lookup_err("no such host"));
            }
            query.push(label.len() as u8);
            query.extend_from_slice(label.as_bytes());
        }
        query.extend_from_slice(&[0, 0, 1, 0, 1]);
        let socket = std::net::UdpSocket::bind("127.0.0.1:0").map_err(|e| lookup_err(&e.to_string()))?;
        socket.connect("127.0.0.1:53").map_err(|e| lookup_err(&e.to_string()))?;
        let mut buf = [0u8; 1500];
        loop {
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Err(lookup_err("i/o timeout"));
            }
            socket.send(&query).map_err(|e| lookup_err(&e.to_string()))?;
            socket.set_read_timeout(Some(remaining.min(StdDuration::from_secs(2)))).map_err(|e| lookup_err(&e.to_string()))?;
            let n = match socket.recv(&mut buf) {
                Ok(n) => n,
                Err(e) if matches!(e.kind(), std::io::ErrorKind::WouldBlock | std::io::ErrorKind::TimedOut) => continue,
                Err(e) => return Err(lookup_err(&e.to_string())),
            };
            let reply = &buf[..n];
            if n < 12 || reply[0..2] != id || reply[2] & 0x80 == 0 {
                continue;
            }
            return match reply[3] & 0x0f {
                0 => match parse_a_answers(reply) {
                    Some(addresses) if !addresses.is_empty() => Ok(addresses),
                    Some(_) => Err(LookupError::NotFound),
                    None => Err(lookup_err("cannot unmarshal DNS message")),
                },
                3 => Err(LookupError::NotFound),
                2 => Err(lookup_err("server misbehaving")),
                _ => Err(lookup_err("server misbehaving")),
            };
        }
    }
}

fn skip_name(message: &[u8], mut offset: usize) -> Option<usize> {
    loop {
        let len = *message.get(offset)? as usize;
        if len & 0xc0 == 0xc0 {
            return Some(offset + 2);
        }
        if len == 0 {
            return Some(offset + 1);
        }
        offset += 1 + len;
    }
}

fn parse_a_answers(message: &[u8]) -> Option<Vec<String>> {
    let questions = u16::from_be_bytes([message[4], message[5]]);
    let answers = u16::from_be_bytes([message[6], message[7]]);
    let mut offset = 12;
    for _ in 0..questions {
        offset = skip_name(message, offset)? + 4;
    }
    let mut addresses = Vec::new();
    for _ in 0..answers {
        offset = skip_name(message, offset)?;
        let header = message.get(offset..offset + 10)?;
        let (rtype, rdlen) = (u16::from_be_bytes([header[0], header[1]]), u16::from_be_bytes([header[8], header[9]]) as usize);
        let data = message.get(offset + 10..offset + 10 + rdlen)?;
        if rtype == 1 && rdlen == 4 {
            addresses.push(format!("{}.{}.{}.{}", data[0], data[1], data[2], data[3]));
        }
        offset += 10 + rdlen;
    }
    Some(addresses)
}

#[cfg(test)]
mod tests {
    use super::*;

    struct Fixed(fn() -> Result<Vec<String>, LookupError>);
    impl HostResolver for Fixed {
        fn lookup_host(&self, _: Instant, _: &str) -> Result<Vec<String>, LookupError> {
            (self.0)()
        }
    }

    #[test]
    fn dns_probe_treats_nxdomain_as_functional() {
        assert_eq!(probe_with(&Fixed(|| Err(LookupError::NotFound)), StdDuration::from_secs(1), "x.example.com.").0, true);
        assert_eq!(probe_with(&Fixed(|| Ok(vec!["192.0.2.1".into()])), StdDuration::from_secs(1), "x.example.com.").0, true);
        let (ok, err) = probe_with(&Fixed(|| Err(LookupError::Other("server misbehaving".into()))), StdDuration::from_secs(1), "x.example.com.");
        assert!(!ok && err.as_deref() == Some("server misbehaving"));
    }

    #[test]
    fn build_reports_worst_state() {
        let mut input = Input { now: Time::from_unix(1_800_000_000, 0), update_trust_configured: true, ..Default::default() };
        input.runtime.storage = crate::storage::evaluate(100, 50);
        input.runtime.memory_total_bytes = 100;
        input.runtime.memory_used_bytes = 85;
        let snapshot = build(&input);
        assert_eq!(snapshot.state, STATE_WARNING);
        assert_eq!(snapshot.headline, "Appliance has warnings");
        let memory = snapshot.checks.iter().find(|c| c.id == "memory").unwrap();
        assert_eq!((memory.state.as_str(), memory.summary.as_str()), (STATE_WARNING, "Memory usage is 85.0%."));
        input.engine.recovery_required = true;
        assert_eq!(build(&input).state, STATE_RECOVERY_REQUIRED);
    }
}
