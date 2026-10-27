//! Manual service recovery and device pause actions over the narrowly
//! allowlisted privileged action socket, plus firewall counter projection.

use regex::Regex;
use serde::{Deserialize, Serialize};
use std::collections::{HashMap, HashSet};
use std::fmt;
use std::sync::LazyLock;
#[cfg(unix)]
use std::time::Duration;
use std::time::Instant;

use super::engine::Engine;
use super::ipc::FirewallCounters;
use crate::errorf;
use crate::goish::errors::Result;
use crate::goish::{json, net, time::Time};

/// Covers 512 IPv4 entries with int64 expiries.
pub const MAX_SERVICE_ACTION_RESPONSE_BYTES: usize = 64 << 10;
pub const MAX_DEVICE_PAUSES: usize = 512;

pub const ACTION_INVALID: &str = "invalid_request";
pub const ACTION_CONFLICT: &str = "conflict";
pub const ACTION_UNAVAILABLE: &str = "unavailable";
pub const ACTION_RECOVERY_REQUIRED: &str = "recovery_required";
pub const ACTION_FAILED: &str = "failed";

/// A stable IPC/API classification, without parsing messages.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ActionError {
    pub code: String,
    pub message: String,
}

impl fmt::Display for ActionError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.message)
    }
}

impl std::error::Error for ActionError {}

fn action_error(code: &str, message: &str) -> crate::goish::errors::Error {
    ActionError { code: code.into(), message: message.into() }.into()
}

pub const SERVICE_ACTION_SOCKET_PATH: &str = "/run/minimalrouter/actions.sock";

pub const SERVICE_ACTION_WAN_RECONNECT: &str = "wan-reconnect";
pub const SERVICE_ACTION_DNS_DHCP_RESTART: &str = "dns-dhcp-restart";
pub const SERVICE_ACTION_WIREGUARD_RESTART: &str = "wireguard-restart";
pub const DEVICE_ACTION_PAUSE: &str = "device-pause";
pub const DEVICE_ACTION_RESUME: &str = "device-resume";
pub const DEVICE_ACTION_STATUS: &str = "device-pause-status";

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct DevicePause {
    pub ip: String,
    pub until_unix: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct ServiceActionRequest {
    pub action: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub ip: String,
    #[serde(skip_serializing_if = "crate::goish::json::is_zero_i64")]
    pub seconds: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct ServiceActionResponse {
    pub success: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub code: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub pauses: Vec<DevicePause>,
}

pub fn valid_service_action(action: &str) -> bool {
    matches!(action, SERVICE_ACTION_WAN_RECONNECT | SERVICE_ACTION_DNS_DHCP_RESTART | SERVICE_ACTION_WIREGUARD_RESTART)
}

#[cfg(unix)]
pub fn call_service_action_socket(deadline: Instant, socket_path: &str, request: &ServiceActionRequest) -> Result<ServiceActionResponse> {
    use std::io::{Read, Write};
    use std::os::unix::net::UnixStream;
    let mut stream = UnixStream::connect(socket_path).map_err(|e| errorf!("cannot connect to privileged action socket: {}", e))?;
    let deadline = deadline.min(Instant::now() + Duration::from_secs(90));
    let remaining = deadline.saturating_duration_since(Instant::now()).max(Duration::from_millis(1));
    stream.set_read_timeout(Some(remaining))?;
    stream.set_write_timeout(Some(remaining))?;
    let mut body = json::marshal(request)?;
    body.push(b'\n');
    stream.write_all(&body).map_err(|e| errorf!("send privileged action: {}", e))?;
    stream.shutdown(std::net::Shutdown::Write).map_err(|e| errorf!("finalize privileged action request: {}", e))?;
    let mut data = Vec::new();
    match stream.take(MAX_SERVICE_ACTION_RESPONSE_BYTES as u64 + 1).read_to_end(&mut data) {
        Ok(_) => {}
        Err(e) if matches!(e.kind(), std::io::ErrorKind::WouldBlock | std::io::ErrorKind::TimedOut) => {
            return Err(errorf!("context deadline exceeded"));
        }
        Err(e) => return Err(errorf!("read privileged action response: {}", e)),
    }
    decode_service_action_response(&data)
}

#[cfg(not(unix))]
pub fn call_service_action_socket(_deadline: Instant, _socket_path: &str, _request: &ServiceActionRequest) -> Result<ServiceActionResponse> {
    Err(errorf!("cannot connect to privileged action socket: unix sockets are unavailable on this platform"))
}

pub fn decode_service_action_response(data: &[u8]) -> Result<ServiceActionResponse> {
    if data.len() > MAX_SERVICE_ACTION_RESPONSE_BYTES {
        return Err(errorf!("privileged action response exceeds limit"));
    }
    let mut response: ServiceActionResponse = match super::ipc::decode_single(data) {
        Ok(r) => r,
        Err(super::ipc::SingleDecodeError::Trailing) => return Err(errorf!("privileged action helper returned trailing data")),
        Err(super::ipc::SingleDecodeError::Invalid(e)) => return Err(errorf!("read privileged action response: {}", e)),
    };
    if response.pauses.len() > MAX_DEVICE_PAUSES {
        return Err(errorf!("privileged action pause count exceeds limit"));
    }
    if !response.success {
        if response.error.is_empty() {
            response.error = "privileged action failed".into();
        }
        if response.code.is_empty() {
            response.code = ACTION_FAILED.into();
        }
        return Err(action_error(&response.code, &response.error));
    }
    if !response.error.is_empty() || !response.code.is_empty() {
        return Err(errorf!("contradictory privileged action response"));
    }
    let mut seen = HashSet::new();
    for pause in &response.pauses {
        let valid = net::parse_ipv4(&pause.ip).is_some_and(|ip| ip.to_string() == pause.ip) && pause.until_unix >= 0;
        if !valid || !seen.insert(pause.ip.clone()) {
            return Err(errorf!("invalid privileged action pause state"));
        }
    }
    Ok(response)
}

fn call_action_socket(deadline: Instant, request: ServiceActionRequest) -> Result<ServiceActionResponse> {
    call_service_action_socket(deadline, SERVICE_ACTION_SOCKET_PATH, &request)
}

impl Engine {
    /// Serialises manual service recovery with configuration transactions and
    /// refuses it while the canonical runtime is pending or in recovery.
    pub fn run_service_action(&self, deadline: Instant, action: &str) -> Result<()> {
        if !valid_service_action(action) {
            return Err(action_error(ACTION_INVALID, "unsupported service action"));
        }
        let _op = self.operation_lock();
        if !self.action_allowed() {
            return Err(action_error(ACTION_CONFLICT, "service recovery is unavailable while configuration recovery or confirmation is active"));
        }
        call_action_socket(deadline, ServiceActionRequest { action: action.into(), ..Default::default() }).map(|_| ())
    }

    /// The caller-side half of device address validation; router-applyd
    /// validates again from its own last-good configuration.
    fn validate_device_ip(&self, value: &str) -> Result<String> {
        let Some(ip) = net::parse_ip(value.trim()).and_then(|ip| ip.to4()) else {
            return Err(action_error(ACTION_INVALID, "device address must be IPv4"));
        };
        let cfg = self.get_current_config();
        let Some((_, lan)) = net::parse_cidr(cfg.lan.cidr.trim()) else {
            return Err(action_error(ACTION_UNAVAILABLE, "trusted LAN range is unavailable"));
        };
        if !lan.contains(&ip) {
            return Err(action_error(ACTION_INVALID, "device address is outside the trusted LAN"));
        }
        if net::parse_ip(cfg.lan.ip_address.trim()).is_some_and(|gw| ip.equal(&gw)) {
            return Err(action_error(ACTION_INVALID, "router LAN address cannot be paused"));
        }
        Ok(ip.to_string())
    }

    pub fn pause_device_internet(&self, deadline: Instant, ip: &str, seconds: i64) -> Result<Vec<DevicePause>> {
        if seconds != 0 && seconds != 15 * 60 && seconds != 60 * 60 {
            return Err(action_error(ACTION_INVALID, "pause duration must be 15 minutes, 1 hour, or until resumed"));
        }
        let _op = self.operation_lock();
        let address = self.validate_device_ip(ip)?;
        if !self.action_allowed() {
            return Err(action_error(ACTION_CONFLICT, "device pause is unavailable while configuration recovery or confirmation is active"));
        }
        call_action_socket(deadline, ServiceActionRequest { action: DEVICE_ACTION_PAUSE.into(), ip: address, seconds }).map(|r| r.pauses)
    }

    pub fn resume_device_internet(&self, deadline: Instant, ip: &str) -> Result<Vec<DevicePause>> {
        let _op = self.operation_lock();
        let address = self.validate_device_ip(ip)?;
        if !self.action_allowed() {
            return Err(action_error(ACTION_CONFLICT, "device resume is unavailable while configuration recovery or confirmation is active"));
        }
        call_action_socket(deadline, ServiceActionRequest { action: DEVICE_ACTION_RESUME.into(), ip: address, seconds: 0 }).map(|r| r.pauses)
    }

    pub fn device_internet_pauses(&self, deadline: Instant) -> Result<Vec<DevicePause>> {
        let _op = self.operation_lock();
        if Instant::now() >= deadline {
            return Err(errorf!("context deadline exceeded"));
        }
        if !self.action_allowed() {
            return Err(action_error(ACTION_CONFLICT, "device pause status is unavailable while configuration recovery or confirmation is active"));
        }
        call_action_socket(deadline, ServiceActionRequest { action: DEVICE_ACTION_STATUS.into(), ..Default::default() }).map(|r| r.pauses)
    }
}

static RULE_COUNTER_NAME: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"^mr_rule_[a-f0-9]{24}$").unwrap());

/// Projects owned counters and base-chain policy metadata from `nft -j`
/// output. Raw rules and set elements are never returned.
pub fn parse_firewall_counters(raw: &str, boot: &str) -> Result<FirewallCounters> {
    #[derive(Deserialize, Default)]
    #[serde(default)]
    struct Chain {
        family: String,
        table: String,
        name: String,
        hook: String,
        #[serde(rename = "type")]
        kind: String,
        prio: i64,
        policy: String,
    }
    #[derive(Deserialize, Default)]
    #[serde(default)]
    struct Table {
        family: String,
        name: String,
        handle: u64,
    }
    #[derive(Deserialize, Default)]
    #[serde(default)]
    struct Counter {
        family: String,
        table: String,
        name: String,
        handle: u64,
        packets: u64,
    }
    #[derive(Deserialize, Default)]
    #[serde(default)]
    struct Entry {
        chain: Option<Chain>,
        table: Option<Table>,
        counter: Option<Counter>,
    }
    #[derive(Deserialize, Default)]
    #[serde(default)]
    struct Payload {
        nftables: Vec<Entry>,
    }
    let payload: Payload = json::unmarshal(raw.as_bytes())?;
    let mut table_handle = 0u64;
    let mut result = FirewallCounters { observed_at: Time::now_utc(), ..Default::default() };
    let mut counters: HashMap<String, u64> = HashMap::new();
    let mut handles: HashMap<String, u64> = HashMap::new();
    for entry in payload.nftables {
        if let Some(c) = &entry.chain {
            if c.family == "inet"
                && c.table == "minimalrouter"
                && c.hook == c.name
                && c.kind == "filter"
                && c.prio == 0
                && matches!(c.name.as_str(), "input" | "forward" | "output")
            {
                result.policies.insert(c.name.clone(), c.policy.clone());
            }
        }
        if let Some(t) = &entry.table {
            if t.family == "inet" && t.name == "minimalrouter" {
                table_handle = t.handle;
            }
        }
        let Some(c) = entry.counter else { continue };
        if c.family != "inet" || c.table != "minimalrouter" {
            continue;
        }
        if let Some(rest) = c.name.strip_prefix("mr_revision_") {
            if let Ok(n) = rest.parse::<u64>() {
                if !rest.starts_with('+') {
                    if result.loaded_revision.is_some() {
                        return Err(errorf!("ambiguous revision"));
                    }
                    result.loaded_revision = Some(n);
                }
            }
        }
        if RULE_COUNTER_NAME.is_match(&c.name) && result.rule_packets.len() < 1024 {
            result.rule_packets.insert(c.name.clone(), c.packets);
        }
        if c.name != "mr_seen" && c.name != "mr_accepted" {
            continue;
        }
        if counters.contains_key(&c.name) {
            return Err(errorf!("duplicate counter"));
        }
        counters.insert(c.name.clone(), c.packets);
        handles.insert(c.name, c.handle);
    }
    let seen_handle = handles.get("mr_seen").copied().unwrap_or(0);
    let accepted_handle = handles.get("mr_accepted").copied().unwrap_or(0);
    if boot.is_empty() || table_handle == 0 || counters.len() != 2 || seen_handle == 0 || accepted_handle == 0 {
        return Err(errorf!("missing counters or generation"));
    }
    result.seen = counters["mr_seen"];
    result.accepted = counters["mr_accepted"];
    result.generation = format!("{}:{}:{}:{}", boot, table_handle, seen_handle, accepted_handle);
    Ok(result)
}
