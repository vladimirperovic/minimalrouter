//! Fixed service actions, device pauses, firewall counter telemetry and the
//! sanitized WireGuard status query.

use std::sync::Mutex;
use std::time::{Duration, Instant};

use super::Guarded;
use super::runtime::{recover_wan, restart_wireguard_services};
use super::state::*;
use super::sys::*;
use crate::apply::{
    ACTION_CONFLICT, ACTION_FAILED, ACTION_INVALID, ACTION_RECOVERY_REQUIRED, ACTION_UNAVAILABLE, ActionError, ApplyRequest, ApplyResponse,
    DEVICE_ACTION_PAUSE, DEVICE_ACTION_RESUME, DEVICE_ACTION_STATUS, DevicePause, FirewallCounters, MAX_DEVICE_PAUSES, MAX_SERVICE_ACTION_RESPONSE_BYTES,
    SERVICE_ACTION_DNS_DHCP_RESTART, SERVICE_ACTION_WAN_RECONNECT, SERVICE_ACTION_WIREGUARD_RESTART, ServiceActionRequest, ServiceActionResponse, TunnelStatus,
    parse_firewall_counters,
};
use crate::config::SystemConfig;
use crate::errorf;
use crate::goish::errors::{self, Result};
use crate::goish::{json, net, time::Time};

const DEVICE_PAUSE_STATE_PATH: &str = "/var/lib/minimalrouter-applyd/device-pauses.json";
const DEVICE_PAUSE_NFT_PATH: &str = "/run/minimalrouter/device-pauses.nft";

fn pause_error(code: &str, message: &str) -> errors::Error {
    ActionError { code: code.into(), message: message.into() }.into()
}

pub(crate) fn service_action_failure(err: &errors::Error) -> ServiceActionResponse {
    if let Some(action) = errors::as_ref::<ActionError>(err) {
        return ServiceActionResponse { code: action.code.clone(), error: action.message.clone(), ..Default::default() };
    }
    log::warn!("fixed action failed: {}", err);
    ServiceActionResponse { code: ACTION_FAILED.into(), error: "privileged action failed".into(), ..Default::default() }
}

pub(crate) fn validate_service_action_request(request: &ServiceActionRequest) -> Result<()> {
    match request.action.as_str() {
        DEVICE_ACTION_PAUSE => {
            if ![0, 900, 3600].contains(&request.seconds) {
                return Err(pause_error(ACTION_INVALID, "unsupported pause duration"));
            }
        }
        DEVICE_ACTION_RESUME => {
            if request.seconds != 0 {
                return Err(pause_error(ACTION_INVALID, "resume does not accept a pause duration"));
            }
        }
        DEVICE_ACTION_STATUS | SERVICE_ACTION_WAN_RECONNECT | SERVICE_ACTION_DNS_DHCP_RESTART | SERVICE_ACTION_WIREGUARD_RESTART => {
            if !request.ip.is_empty() || request.seconds != 0 {
                return Err(pause_error(ACTION_INVALID, "unexpected action parameters"));
            }
        }
        _ => return Err(pause_error(ACTION_INVALID, "service action is not allowlisted")),
    }
    Ok(())
}

/// Every mutation is guarded in the root helper as well as in routerd.
pub(crate) fn guard_service_action_mutation(guarded: &Guarded) -> Result<()> {
    let pending = load_pending_confirmation();
    let record = load_last_transaction();
    if pending.is_ok() || pending.as_ref().is_err_and(|e| !is_not_found(e)) {
        return Err(pause_error(ACTION_CONFLICT, "configuration confirmation or recovery is active"));
    }
    if record.as_ref().is_err_and(|e| !is_not_found(e)) {
        return Err(pause_error(ACTION_RECOVERY_REQUIRED, "configuration transaction state is unreadable"));
    }
    for tx in [record.as_ref().ok(), guarded.last_transaction.as_ref()].into_iter().flatten() {
        if tx.completed_at.is_zero() || tx.response.recovery_required {
            return Err(pause_error(ACTION_RECOVERY_REQUIRED, "configuration transaction requires recovery"));
        }
    }
    Ok(())
}

pub(crate) fn run_fixed_service_action(action: &str) -> Result<()> {
    let cfg = load_last_good().map_err(|_| errorf!("trusted last-good configuration unavailable"))?;
    match action {
        SERVICE_ACTION_WAN_RECONNECT => recover_wan(&cfg),
        SERVICE_ACTION_DNS_DHCP_RESTART => {
            run_fixed("/sbin/rc-service", &["dnsmasq", "restart"]).map_err(|e| errors::wrapf("restart dnsmasq", e))?;
            run_fixed("/sbin/rc-service", &["dnsmasq", "status"]).map_err(|e| errors::wrapf("dnsmasq unhealthy after restart", e))
        }
        SERVICE_ACTION_WIREGUARD_RESTART => restart_wireguard_services(&cfg),
        _ => Err(errorf!("service action is not allowlisted")),
    }
}

/// Handles one service-action request under the apply lock.
pub(crate) fn handle_service_action(guarded: &mut Guarded, request: &ServiceActionRequest) -> ServiceActionResponse {
    if let Err(err) = guard_service_action_mutation(guarded) {
        return service_action_failure(&err);
    }
    let pauses = match request.action.as_str() {
        DEVICE_ACTION_STATUS => devices_reconcile(guarded),
        DEVICE_ACTION_PAUSE => device_change(guarded, &request.ip, request.seconds, false),
        DEVICE_ACTION_RESUME => device_change(guarded, &request.ip, 0, true),
        action => {
            return match run_fixed_service_action(action) {
                Ok(()) => {
                    log::info!("service action {:?} completed", action);
                    ServiceActionResponse { success: true, ..Default::default() }
                }
                Err(err) => {
                    log::warn!("service action {:?} failed: {}", action, err);
                    service_action_failure(&err)
                }
            };
        }
    };
    match pauses {
        Ok(pauses) => ServiceActionResponse { success: true, pauses, ..Default::default() },
        Err(err) => {
            log::warn!("device action failed: {}", err);
            service_action_failure(&err)
        }
    }
}

pub(crate) fn validate_pause_ip_for_config(value: &str, cfg: &SystemConfig) -> Result<String> {
    let Some(ip) = net::parse_ip(value.trim()).and_then(|ip| ip.to4()) else {
        return Err(pause_error(ACTION_INVALID, "device address must be IPv4"));
    };
    let Some((_, lan)) = net::parse_cidr(&cfg.lan.cidr).filter(|(_, n)| n.ip.to4().is_some()) else {
        return Err(pause_error(ACTION_UNAVAILABLE, "trusted LAN range is unavailable"));
    };
    if !lan.contains(&ip) {
        return Err(pause_error(ACTION_INVALID, "device address is outside the trusted LAN"));
    }
    if net::parse_ip(&cfg.lan.ip_address).is_some_and(|gw| ip.equal(&gw)) {
        return Err(pause_error(ACTION_INVALID, "router LAN address cannot be paused"));
    }
    Ok(ip.to_string())
}

/// Pauses always hold the last committed set; Pending is an undo intent.
#[derive(Clone, Debug, Default, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
#[serde(default, deny_unknown_fields)]
pub(crate) struct DevicePauseRecord {
    pub version: i64,
    pub phase: String,
    pub pauses: Vec<DevicePause>,
}

fn load_pause_record(guarded: &Guarded) -> Result<DevicePauseRecord> {
    if let Some(uncertain) = &guarded.pause_uncertain {
        return Ok(uncertain.clone());
    }
    let mut record = DevicePauseRecord { version: 1, phase: "applied".into(), pauses: Vec::new() };
    let pending_path = format!("{}.pending", DEVICE_PAUSE_STATE_PATH);
    let (data, journal) = match std::fs::read(&pending_path) {
        Ok(data) => (data, true),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => match std::fs::read(DEVICE_PAUSE_STATE_PATH) {
            Ok(data) => (data, false),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(record),
            Err(e) => return Err(e.into()),
        },
        Err(e) => return Err(e.into()),
    };
    if data.len() > MAX_SERVICE_ACTION_RESPONSE_BYTES {
        return Err(errorf!("device pause state exceeds size limit"));
    }
    let text = String::from_utf8_lossy(&data);
    let text = text.trim();
    // The old array format is read without rewriting or discarding state.
    if text.starts_with('[') {
        record.pauses = json::unmarshal(text.as_bytes())?;
    } else {
        record = match crate::apply::decode_single(text.as_bytes()) {
            Ok(r) => r,
            Err(crate::apply::SingleDecodeError::Trailing) => return Err(errorf!("trailing pause state")),
            Err(crate::apply::SingleDecodeError::Invalid(e)) => return Err(e.into()),
        };
    }
    if record.version != 1 || !matches!(record.phase.as_str(), "applied" | "pending" | "recovery_required") {
        return Err(errorf!("invalid device pause transaction state"));
    }
    if record.pauses.len() > MAX_DEVICE_PAUSES {
        return Err(errorf!("device pause state exceeds count limit"));
    }
    if journal && record.phase == "applied" {
        return Err(errorf!("invalid device pause journal"));
    }
    let mut seen = std::collections::HashSet::new();
    for pause in &record.pauses {
        let valid = net::parse_ipv4(&pause.ip).is_some_and(|ip| ip.to_string() == pause.ip) && pause.until_unix >= 0;
        if !valid || !seen.insert(pause.ip.clone()) {
            return Err(errorf!("invalid device pause record"));
        }
    }
    Ok(record)
}

fn persist_pause_record(record: &DevicePauseRecord) -> Result<()> {
    let data = json::marshal(record)?;
    std::fs::create_dir_all(std::path::Path::new(DEVICE_PAUSE_STATE_PATH).parent().unwrap())?;
    let path = if record.phase == "applied" { DEVICE_PAUSE_STATE_PATH.to_string() } else { format!("{}.pending", DEVICE_PAUSE_STATE_PATH) };
    atomic_write(&path, &data, 0o600)
}

/// The undo journal is removed only after both the nft ACK and durable commit.
fn finish_pause_record(mut record: DevicePauseRecord) -> Result<()> {
    record.phase = "applied".into();
    persist_pause_record(&record)?;
    clear_file_durably(&format!("{}.pending", DEVICE_PAUSE_STATE_PATH)).map_err(Into::into)
}

fn active_pauses(pauses: &[DevicePause]) -> Vec<DevicePause> {
    let now = Time::now().unix();
    let mut active: Vec<DevicePause> = pauses.iter().filter(|p| p.until_unix == 0 || p.until_unix > now).cloned().collect();
    active.sort_by(|a, b| a.ip.cmp(&b.ip));
    active
}

/// Reasserts the committed nft set before status; a read never prunes durable
/// state and a failed restore never returns a success list.
fn devices_reconcile(guarded: &mut Guarded) -> Result<Vec<DevicePause>> {
    let mut record = load_pause_record(guarded).map_err(|_| pause_error(ACTION_RECOVERY_REQUIRED, "device pause state is unreadable; recovery required"))?;
    let pauses = active_pauses(&record.pauses);
    apply_device_pause_firewall(&pauses).map_err(|_| pause_error(ACTION_RECOVERY_REQUIRED, "device pause firewall restore failed; recovery required"))?;
    if record.phase != "applied" || guarded.pause_uncertain.is_some() {
        record.phase = "applied".into();
        finish_pause_record(record).map_err(|_| pause_error(ACTION_RECOVERY_REQUIRED, "device pause recovery outcome could not be persisted"))?;
        guarded.pause_uncertain = None;
    }
    Ok(pauses)
}

/// Reasserts the committed pause set when the action socket starts.
pub(crate) fn restore_device_pauses(guarded: &mut Guarded) {
    if let Err(err) = devices_reconcile(guarded) {
        log::warn!("device pause restore unavailable: {}", err);
    }
}

fn device_transition(guarded: &mut Guarded, previous: Vec<DevicePause>, next: Vec<DevicePause>) -> Result<Vec<DevicePause>> {
    let mut undo = DevicePauseRecord { version: 1, phase: "pending".into(), pauses: previous.clone() };
    // No nft mutation without durable intent; keep it in memory first.
    guarded.pause_uncertain = Some(undo.clone());
    if persist_pause_record(&undo).is_err() {
        return Err(pause_error(ACTION_RECOVERY_REQUIRED, "device pause intent could not be persisted; refresh status for recovery"));
    }
    if apply_device_pause_firewall(&next).is_ok() {
        let result = DevicePauseRecord { version: 1, phase: "applied".into(), pauses: next.clone() };
        if finish_pause_record(result).is_ok() {
            guarded.pause_uncertain = None;
            return Ok(next);
        }
    }
    undo.phase = "recovery_required".into();
    let intent = persist_pause_record(&undo);
    let rollback = apply_device_pause_firewall(&previous);
    if intent.is_err() || rollback.is_err() {
        return Err(pause_error(ACTION_RECOVERY_REQUIRED, "device pause rollback could not be completed; recovery required"));
    }
    if finish_pause_record(undo).is_err() {
        return Err(pause_error(ACTION_RECOVERY_REQUIRED, "device pause rollback outcome could not be persisted"));
    }
    guarded.pause_uncertain = None;
    Err(pause_error(ACTION_FAILED, "device pause action failed; previous pause state restored"))
}

fn device_change(guarded: &mut Guarded, value: &str, seconds: i64, resume: bool) -> Result<Vec<DevicePause>> {
    if ![0, 900, 3600].contains(&seconds) {
        return Err(pause_error(ACTION_INVALID, "unsupported pause duration"));
    }
    let cfg = load_last_good().map_err(|_| pause_error(ACTION_UNAVAILABLE, "trusted last-good configuration unavailable"))?;
    let ip = validate_pause_ip_for_config(value, &cfg)?;
    let previous = devices_reconcile(guarded)?;
    let mut next: Vec<DevicePause> = previous.iter().filter(|p| p.ip != ip).cloned().collect();
    if !resume {
        if next.len() >= MAX_DEVICE_PAUSES {
            return Err(pause_error(ACTION_CONFLICT, "device pause limit reached"));
        }
        let until = if seconds > 0 { Time::now().unix() + seconds } else { 0 };
        next.push(DevicePause { ip, until_unix: until });
    }
    next.sort_by(|a, b| a.ip.cmp(&b.ip));
    device_transition(guarded, previous, next)
}

fn apply_device_pause_firewall(pauses: &[DevicePause]) -> Result<()> {
    let cfg = load_last_good().map_err(|_| pause_error(ACTION_UNAVAILABLE, "trusted last-good configuration unavailable"))?;
    let exists = device_pause_table_exists()?;
    let mut batch = render_device_pause_firewall(&cfg, pauses, Time::now().unix())?;
    if exists {
        batch = format!("delete table inet minimalrouter_pause\n{}", batch);
    }
    atomic_write(DEVICE_PAUSE_NFT_PATH, batch.as_bytes(), 0o600)?;
    let result = run_fixed("/usr/sbin/nft", &["-c", "-f", DEVICE_PAUSE_NFT_PATH])
        .map_err(|e| errors::wrapf("validate pause firewall", e))
        .and_then(|_| run_fixed("/usr/sbin/nft", &["-f", DEVICE_PAUSE_NFT_PATH]).map_err(|e| errors::wrapf("apply pause firewall", e)));
    let _ = std::fs::remove_file(DEVICE_PAUSE_NFT_PATH);
    result
}

/// A failed table probe is not evidence of absence.
fn device_pause_table_exists() -> Result<bool> {
    let text = run_fixed_output("/usr/sbin/nft", &["-j", "list", "tables"]).map_err(|e| errors::wrapf("inspect pause firewall", e))?;
    let value: serde_json::Value = serde_json::from_str(&text)?;
    let Some(items) = value.get("nftables").and_then(|v| v.as_array()) else {
        return Err(errorf!("invalid nft table inventory"));
    };
    Ok(items.iter().any(|item| {
        item.get("table")
            .is_some_and(|t| t.get("family").and_then(|f| f.as_str()) == Some("inet") && t.get("name").and_then(|n| n.as_str()) == Some("minimalrouter_pause"))
    }))
}

pub(crate) fn render_device_pause_firewall(cfg: &SystemConfig, pauses: &[DevicePause], now: i64) -> Result<String> {
    if pauses.len() > MAX_DEVICE_PAUSES {
        return Err(errorf!("device pause limit exceeded"));
    }
    if !cfg.wan.interface.is_empty() && !INTERFACE_NAME_PATTERN.is_match(&cfg.wan.interface) {
        return Err(errorf!("invalid WAN interface"));
    }
    let mut elements = Vec::new();
    let mut seen = std::collections::HashSet::new();
    for pause in pauses {
        let mut ip = validate_pause_ip_for_config(&pause.ip, cfg)?;
        if pause.until_unix < 0 || !seen.insert(ip.clone()) {
            return Err(errorf!("invalid pause expiry or duplicate address"));
        }
        if pause.until_unix > 0 {
            let remaining = pause.until_unix - now;
            if remaining <= 0 {
                continue;
            }
            ip.push_str(&format!(" timeout {}s", remaining));
        }
        elements.push(ip);
    }
    let mut b = String::from("table inet minimalrouter_pause {\n  set blocked_ipv4 {\n    type ipv4_addr\n    flags timeout\n");
    if !elements.is_empty() {
        b.push_str(&format!("    elements = {{ {} }}\n", elements.join(", ")));
    }
    // Pause only Internet forwarding; never flush the canonical ruleset.
    b.push_str("  }\n  chain forward {\n    type filter hook forward priority -10; policy accept;\n");
    if !cfg.wan.interface.is_empty() {
        b.push_str(&format!("    ip saddr @blocked_ipv4 oifname \"{}\" drop\n", cfg.wan.interface));
    }
    b.push_str("    ip saddr @blocked_ipv4 oifname \"ppp*\" drop\n  }\n}\n");
    Ok(b)
}

struct CounterCache {
    at: Option<Instant>,
    value: Option<FirewallCounters>,
}

static FIREWALL_TELEMETRY: Mutex<CounterCache> = Mutex::new(CounterCache { at: None, value: None });

/// All arguments are fixed: callers cannot select a table or raw rule output.
pub(crate) fn firewall_counters(req: &ApplyRequest) -> ApplyResponse {
    let mut cache = FIREWALL_TELEMETRY.lock().unwrap_or_else(|e| e.into_inner());
    let fresh = cache.at.is_some_and(|at| at.elapsed() < Duration::from_secs(15));
    if fresh && cache.value.is_none() {
        return failure(&req.id, "Firewall counters unavailable", false);
    }
    if cache.value.is_none() || !cache.at.is_some_and(|at| at.elapsed() <= Duration::from_secs(15)) {
        cache.at = Some(Instant::now());
        cache.value = None;
        // Terse output omits potentially large DNS destination sets.
        let Ok(out) = run_command_output(Duration::from_secs(5), "/usr/sbin/nft", &["-a", "-j", "-t", "list", "table", "inet", "minimalrouter"]) else {
            return failure(&req.id, "Firewall counters unavailable", false);
        };
        let Ok(boot) = std::fs::read_to_string("/proc/sys/kernel/random/boot_id") else {
            return failure(&req.id, "Firewall counter generation unavailable", false);
        };
        let Ok(value) = parse_firewall_counters(&out, boot.trim()) else {
            return failure(&req.id, "Firewall counters not initialized", false);
        };
        cache.value = Some(value);
        cache.at = Some(Instant::now());
    }
    ApplyResponse {
        id: req.id.clone(),
        success: true,
        verified: true,
        timestamp: Time::now().unix(),
        firewall_counters: cache.value.clone(),
        ..Default::default()
    }
}

/// A sanitized `wg show <if> dump` projection: keys never cross the boundary.
pub(crate) fn wg_tunnel_status(req: &ApplyRequest) -> ApplyResponse {
    let server = if req.config.wireguard.interface.is_empty() { "wg0" } else { &req.config.wireguard.interface };
    let client = if req.config.wg_client.interface.is_empty() { "wg1" } else { &req.config.wg_client.interface };
    let mut iface = req.tunnel_interface.trim();
    if iface.is_empty() {
        iface = client;
    }
    if !INTERFACE_NAME_PATTERN.is_match(iface) || (iface != server && iface != client) {
        return failure(&req.id, "invalid WireGuard interface name", false);
    }
    match run_fixed_output("/usr/bin/wg", &["show", iface, "dump"]) {
        Err(err) => failure(&req.id, &format!("WireGuard status unavailable: {}", safe_error(&err)), false),
        Ok(out) => ApplyResponse {
            id: req.id.clone(),
            success: true,
            verified: true,
            logs: "sanitized WireGuard tunnel status".into(),
            tunnel_status: Some(parse_wg_tunnel_status(iface, &out)),
            ..Default::default()
        },
    }
}

/// The first line carries the private key and is skipped; a peer's public key
/// (fields[0]) is never copied.
pub(crate) fn parse_wg_tunnel_status(iface: &str, dump: &str) -> TunnelStatus {
    let mut status = TunnelStatus { interface: iface.into(), ..Default::default() };
    for line in dump.split('\n') {
        let f: Vec<&str> = line.split_whitespace().collect();
        if f.len() < 8 || f[0] == "interface" {
            continue;
        }
        status.endpoint = f[2].into();
        status.last_handshake = crate::config::go_atoi(f[4]).unwrap_or(0);
        status.rx_bytes = crate::config::go_atoi(f[5]).unwrap_or(0);
        status.tx_bytes = crate::config::go_atoi(f[6]).unwrap_or(0);
        break;
    }
    status
}

/// Requires a completed and, with keepalive, recent wg1 handshake.
pub(crate) fn wg_handshake_fresh(dump: &str, keepalive: i64) -> Result<()> {
    for line in dump.split('\n') {
        let f: Vec<&str> = line.split_whitespace().collect();
        if f.len() < 8 || f[0] == "interface" {
            continue;
        }
        let Some(ts) = crate::config::go_atoi(f[4]).filter(|ts| *ts > 0) else {
            return Err(errorf!("WireGuard client tunnel has no completed handshake"));
        };
        let last_seen = Time::now().sub(&Time::from_unix(ts, 0));
        if last_seen < crate::goish::time::Duration::secs(-30) {
            return Err(errorf!("WireGuard client handshake timestamp is in the future"));
        }
        if keepalive > 0 {
            let max_age = crate::goish::time::Duration::minutes(3).max(crate::goish::time::Duration::secs(keepalive * 3));
            if last_seen > max_age {
                return Err(errorf!("WireGuard client handshake is stale (last {} ago)", last_seen.round(crate::goish::time::Duration::SECOND)));
            }
        }
        return Ok(());
    }
    Err(errorf!("WireGuard client tunnel has no peer"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn pause_firewall_renders_timeouts_and_rejects_router_address() {
        let cfg = crate::config::default_config();
        let pauses = vec![DevicePause { ip: "192.168.1.20".into(), until_unix: 0 }, DevicePause { ip: "192.168.1.21".into(), until_unix: 1_000_100 }];
        let text = render_device_pause_firewall(&cfg, &pauses, 1_000_000).unwrap();
        assert!(text.contains("elements = { 192.168.1.20, 192.168.1.21 timeout 100s }"));
        assert!(text.contains("oifname \"eth0\" drop"));
        let bad = vec![DevicePause { ip: "192.168.1.1".into(), until_unix: 0 }];
        assert_eq!(render_device_pause_firewall(&cfg, &bad, 0).unwrap_err().to_string(), "router LAN address cannot be paused");
    }

    #[test]
    fn handshake_freshness() {
        let now = Time::now().unix();
        let dump = format!("priv pub 51820 off\npeer psk 1.2.3.4:51820 10.0.0.0/8 {} 10 20 25\n", now - 10);
        wg_handshake_fresh(&dump, 25).unwrap();
        let stale = format!("priv pub 51820 off\npeer psk 1.2.3.4:51820 10.0.0.0/8 {} 10 20 25\n", now - 600);
        assert!(wg_handshake_fresh(&stale, 25).unwrap_err().to_string().starts_with("WireGuard client handshake is stale"));
        assert_eq!(parse_wg_tunnel_status("wg1", &dump).rx_bytes, 10);
    }
}
