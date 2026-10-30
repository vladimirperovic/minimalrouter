//! Gateway quality endpoints, device pauses, service recovery actions and
//! the conservative PPPoE auto-recovery observer.

use serde::Serialize;
use std::time::Duration as StdDuration;

use super::*;
use crate::apply::{ACTION_CONFLICT, ACTION_FAILED, ACTION_INVALID, ACTION_RECOVERY_REQUIRED, ACTION_UNAVAILABLE, ActionError, EngineStatus};
use crate::gateway::{Monitor, Summary};

const AUTO_RECOVERY_OUTAGE_WINDOW: Duration = Duration::minutes(3);
const AUTO_RECOVERY_COOLDOWN: Duration = Duration::minutes(10);
const AUTO_RECOVERY_POLL_INTERVAL: StdDuration = StdDuration::from_secs(30);
/// The monitor keeps its last summary when collection fails; an old offline
/// reading is evidence only that measurement stopped.
const AUTO_RECOVERY_SAMPLE_MAX_AGE: Duration = Duration::minutes(2);

#[derive(Clone, Debug, Default, Serialize)]
pub(crate) struct GatewayRecoveryStatus {
    state: String,
    reason: String,
    updated_at: Time,
    #[serde(skip_serializing_if = "Option::is_none")]
    disconnected_since: Option<Time>,
    #[serde(skip_serializing_if = "Option::is_none")]
    next_attempt_at: Option<Time>,
    #[serde(skip_serializing_if = "Option::is_none")]
    last_attempt_at: Option<Time>,
    #[serde(skip_serializing_if = "String::is_empty")]
    last_result: String,
}

fn recovery_status(
    wan: bool,
    summary: &Summary,
    status: &EngineStatus,
    pending: bool,
    down: Option<Time>,
    last: Option<Time>,
    now: Time,
) -> GatewayRecoveryStatus {
    let mut result =
        GatewayRecoveryStatus { state: "armed".into(), reason: "Watching the PPPoE link".into(), updated_at: now, last_attempt_at: last, ..Default::default() };
    let age = now.sub(&summary.timestamp);
    let set = |r: &mut GatewayRecoveryStatus, state: &str, reason: &str| {
        r.state = state.into();
        r.reason = reason.into();
    };
    if !wan || !summary.enabled {
        set(&mut result, "paused", "WAN or monitoring is disabled");
    } else if !summary.available
        || summary.timestamp.is_zero()
        || age.0 < 0
        || age > AUTO_RECOVERY_SAMPLE_MAX_AGE
        || summary.state == crate::gateway::STATE_UNKNOWN
    {
        set(&mut result, "waiting", "Waiting for fresh link evidence");
    } else if status.applying || status.recovery_required || pending {
        set(&mut result, "blocked", "A configuration change, confirmation or recovery is in progress");
    } else if !summary.link.connected {
        set(&mut result, "verifying", "Verifying a continuous 3-minute link outage");
        if let Some(down) = down {
            result.disconnected_since = Some(down);
            result.next_attempt_at = Some(down.add(AUTO_RECOVERY_OUTAGE_WINDOW));
        }
        if let Some(last) = last.filter(|l| now.sub(l) < AUTO_RECOVERY_COOLDOWN) {
            set(&mut result, "cooldown", "Waiting before another recovery attempt");
            result.next_attempt_at = Some(last.add(AUTO_RECOVERY_COOLDOWN));
        }
    }
    result
}

/// "Unknown" is not "down": only a fresh, measured disconnected link is
/// evidence for mutating the runtime.
fn usable_outage_evidence(summary: &Summary, now: Time) -> bool {
    if !summary.enabled || !summary.available || summary.timestamp.is_zero() || summary.state == crate::gateway::STATE_UNKNOWN || summary.state.is_empty() {
        return false;
    }
    let age = now.sub(&summary.timestamp);
    age.0 >= 0 && age <= AUTO_RECOVERY_SAMPLE_MAX_AGE && !summary.link.connected
}

fn auto_recovery_due(wan: bool, summary: &Summary, status: &EngineStatus, pending: bool, down: Option<Time>, last: Option<Time>, now: Time) -> bool {
    let Some(down) = down.filter(|_| wan && usable_outage_evidence(summary, now)) else { return false };
    if status.applying || status.recovery_required || pending || now.sub(&down) < AUTO_RECOVERY_OUTAGE_WINDOW {
        return false;
    }
    last.is_none_or(|l| now.sub(&l) >= AUTO_RECOVERY_COOLDOWN)
}

fn device_pause_error(err: &crate::goish::errors::Error) -> Response {
    let (mut status, mut code, mut message) =
        (503u16, ACTION_UNAVAILABLE.to_string(), "Device pause helper unavailable; refresh status before retrying".to_string());
    if let Some(action) = crate::goish::errors::as_ref::<ActionError>(err) {
        code = action.code.clone();
        message = action.message.clone();
        status = match action.code.as_str() {
            ACTION_INVALID => 400,
            ACTION_CONFLICT => 409,
            ACTION_FAILED => 500,
            _ => 503,
        };
    }
    if err.to_string().contains("deadline exceeded") {
        status = 504;
    }
    write_json(status, &json!({"error": message, "code": code, "recovery_required": code == ACTION_RECOVERY_REQUIRED}))
}

fn device_pause_request_error(err: &str) -> Response {
    if err.contains(crate::httpd::BODY_TOO_LARGE) {
        return write_json(413, &json!({"error": "Device pause request exceeds size limit", "code": ACTION_INVALID}));
    }
    device_pause_error(&ActionError { code: ACTION_INVALID.into(), message: "Invalid device pause request".into() }.into())
}

#[derive(serde::Deserialize, Default)]
#[serde(default)]
struct DevicePauseRequest {
    ip: String,
    seconds: i64,
}

impl Server {
    pub fn configure_gateway_monitor(self: &Arc<Self>, monitor: Option<Arc<Monitor>>) {
        self.subsystems_mut().gateway = monitor.clone();
        self.configure_gateway_auto_recovery(monitor);
    }

    fn monitor_or_error(&self) -> std::result::Result<Arc<Monitor>, Response> {
        self.subsystems().gateway.clone().ok_or_else(|| Response::error("Gateway monitoring is unavailable", 503))
    }

    pub(crate) fn handle_get_gateway_summary(&self, _r: &mut Request<'_>) -> Response {
        match self.monitor_or_error() {
            Ok(monitor) => write_json(200, &monitor.summary()),
            Err(response) => response,
        }
    }

    pub(crate) fn handle_get_gateway_history(&self, r: &mut Request<'_>) -> Response {
        let monitor = match self.monitor_or_error() {
            Ok(m) => m,
            Err(response) => return response,
        };
        let mut window_name = r.query().get("window");
        if window_name.is_empty() {
            window_name = "1h".into();
        }
        let (window, max_points) = match window_name.as_str() {
            "1h" => (Duration::HOUR, 120),
            "24h" => (Duration::hours(24), 288),
            "7d" => (Duration::hours(7 * 24), 336),
            "30d" => (Duration::hours(30 * 24), 360),
            _ => return Response::error("window must be one of 1h, 24h, 7d, or 30d", 400),
        };
        match monitor.history(window, max_points) {
            Ok(points) => write_json(200, &json!({"window": window_name, "points": points})),
            Err(_) => Response::error("Gateway history is unavailable", 503),
        }
    }

    pub(crate) fn handle_get_gateway_insights(&self, _r: &mut Request<'_>) -> Response {
        match self.monitor_or_error() {
            Ok(monitor) => match monitor.insights(Duration::hours(30 * 24), 20) {
                Ok(insights) => write_json(200, &insights),
                Err(_) => Response::error("Gateway insights are unavailable", 503),
            },
            Err(response) => response,
        }
    }

    pub(crate) fn handle_get_gateway_settings(&self, _r: &mut Request<'_>) -> Response {
        match self.monitor_or_error() {
            Ok(monitor) => match monitor.settings() {
                Ok(settings) => write_json(200, &settings),
                Err(_) => Response::error("Gateway settings are unavailable", 503),
            },
            Err(response) => response,
        }
    }

    pub(crate) fn handle_put_gateway_settings(&self, r: &mut Request<'_>) -> Response {
        let monitor = match self.monitor_or_error() {
            Ok(m) => m,
            Err(response) => return response,
        };
        let Ok(settings) = decode_json::<crate::gateway::Settings>(r) else { return Response::error("Invalid JSON", 400) };
        match monitor.update_settings(&settings) {
            Ok(()) => write_json(200, &settings),
            Err(err) => write_json(400, &json!({"error": err.to_string()})),
        }
    }

    pub(crate) fn handle_gateway_probe(&self, _r: &mut Request<'_>) -> Response {
        let Some(monitor) = self.subsystems().gateway.clone() else { return Response::error("Gateway monitoring unavailable", 503) };
        match monitor.probe_now() {
            Ok(summary) => write_json(200, &summary),
            Err(err) => write_json(409, &json!({"error": err.to_string()})),
        }
    }

    pub(crate) fn handle_gateway_events(&self, _r: &mut Request<'_>) -> Response {
        let Some(monitor) = self.subsystems().gateway.clone() else { return Response::error("Gateway monitoring unavailable", 503) };
        let Ok(outages) = monitor.outages() else { return Response::error("Outage history unavailable", 503) };
        let store = self.credential_store();
        let mut available = store.is_some();
        let mut recoveries: Option<Vec<config::AuditEvent>> = Some(Vec::new());
        if let Some(store) = store {
            for kind in ["gateway.auto_recovery_completed", "gateway.auto_recovery_failed", "service.recovery", "service.recovery_failed"] {
                match store.query_audit_events(&config::AuditQuery { limit: 20, event_type: kind.into(), ..Default::default() }) {
                    Ok(page) => {
                        let list = recoveries.get_or_insert_with(Vec::new);
                        for event in page.events {
                            if kind.starts_with("service.") && event.details.get("action").map(String::as_str) != Some("wan-reconnect") {
                                continue;
                            }
                            list.push(event);
                        }
                    }
                    Err(_) => {
                        available = false;
                        recoveries = None;
                        break;
                    }
                }
            }
        }
        if let Some(list) = recoveries.as_mut() {
            list.sort_by(|a, b| b.timestamp.cmp(&a.timestamp));
            list.truncate(20);
        }
        write_json(200, &json!({"outages": outages, "outage_window_days": 7, "recoveries": recoveries, "recovery_history_available": available}))
    }

    pub(crate) fn handle_gateway_recovery_status(&self, _r: &mut Request<'_>) -> Response {
        let Some(monitor) = self.subsystems().gateway.clone() else { return Response::error("Recovery status unavailable", 503) };
        let previous = self.ext.gateway_recovery.lock().unwrap_or_else(|e| e.into_inner()).clone();
        let previous = previous.unwrap_or_else(|| GatewayRecoveryStatus {
            state: "waiting".into(),
            reason: "Recovery observer is starting".into(),
            updated_at: Time::now(),
            ..Default::default()
        });
        if previous.state == "recovering" {
            return write_json(200, &previous);
        }
        let mut result = recovery_status(
            self.engine.get_current_config().wan.enabled,
            &monitor.summary(),
            &self.engine.get_status(),
            self.engine.get_pending_transaction().is_some(),
            previous.disconnected_since,
            previous.last_attempt_at,
            Time::now(),
        );
        result.last_result = previous.last_result;
        write_json(200, &result)
    }

    fn configure_gateway_auto_recovery(self: &Arc<Self>, monitor: Option<Arc<Monitor>>) {
        if let Some(stop) = self.ext.gateway_recovery_stop.lock().unwrap_or_else(|e| e.into_inner()).take() {
            stop.trigger();
        }
        *self.ext.gateway_recovery.lock().unwrap_or_else(|e| e.into_inner()) = None;
        let Some(monitor) = monitor else { return };
        let stop = crate::shutdown::Shutdown::new();
        *self.ext.gateway_recovery_stop.lock().unwrap_or_else(|e| e.into_inner()) = Some(stop.clone());
        let server = Arc::downgrade(self);
        std::thread::spawn(move || run_gateway_auto_recovery(server, monitor, stop));
    }

    fn store_recovery_view(&self, view: &GatewayRecoveryStatus) {
        *self.ext.gateway_recovery.lock().unwrap_or_else(|e| e.into_inner()) = Some(view.clone());
    }

    pub(crate) fn handle_service_action(&self, r: &mut Request<'_>) -> Response {
        let action = r.path.trim_start_matches("/api/v1/system/actions/").to_string();
        if !apply::valid_service_action(&action) {
            return Response::error("Unsupported service recovery action", 404);
        }
        let started = Instant::now();
        let result = self.engine.run_service_action(Instant::now() + StdDuration::from_secs(90), &action);
        let duration = started.elapsed().as_secs().to_string();
        match result {
            Err(err) => {
                self.append_audit("service.recovery_failed", &audit_actor(&r.remote_addr), details(&[("action", &action), ("duration_seconds", &duration)]));
                Response::json(409, &json!({"error": err.to_string()}))
            }
            Ok(()) => {
                self.append_audit("service.recovery", &audit_actor(&r.remote_addr), details(&[("action", &action), ("duration_seconds", &duration)]));
                Response::json(200, &json!({"success": true, "action": action}))
            }
        }
    }

    pub(crate) fn handle_get_device_pauses(&self, _r: &mut Request<'_>) -> Response {
        match self.engine.device_internet_pauses(Instant::now() + StdDuration::from_secs(10)) {
            Ok(pauses) => write_json(200, &json!({"pauses": pauses})),
            Err(err) => device_pause_error(&err),
        }
    }

    pub(crate) fn handle_pause_device(&self, r: &mut Request<'_>) -> Response {
        let request = match decode_json::<DevicePauseRequest>(r) {
            Ok(request) => request,
            Err(err) => return device_pause_request_error(&err),
        };
        match self.engine.pause_device_internet(Instant::now() + StdDuration::from_secs(30), &request.ip, request.seconds) {
            Ok(pauses) => {
                self.append_audit(
                    "device.internet_paused",
                    &audit_actor(&r.remote_addr),
                    details(&[("ip", &request.ip), ("duration_seconds", &request.seconds.to_string())]),
                );
                write_json(200, &json!({"success": true, "pauses": pauses}))
            }
            Err(err) => device_pause_error(&err),
        }
    }

    pub(crate) fn handle_resume_device(&self, r: &mut Request<'_>) -> Response {
        let request = match decode_json::<DevicePauseRequest>(r) {
            Ok(request) => request,
            Err(err) => return device_pause_request_error(&err),
        };
        if request.seconds != 0 {
            return device_pause_error(&ActionError { code: ACTION_INVALID.into(), message: "resume does not accept a pause duration".into() }.into());
        }
        match self.engine.resume_device_internet(Instant::now() + StdDuration::from_secs(30), &request.ip) {
            Ok(pauses) => {
                self.append_audit("device.internet_resumed", &audit_actor(&r.remote_addr), details(&[("ip", &request.ip)]));
                write_json(200, &json!({"success": true, "pauses": pauses}))
            }
            Err(err) => device_pause_error(&err),
        }
    }

    pub(crate) fn handle_config_preview(&self, r: &mut Request<'_>) -> Response {
        let Ok(candidate) = decode_json::<SystemConfig>(r) else {
            return write_json(400, &json!({"error": "Invalid configuration payload."}));
        };
        let current = self.engine.get_current_config();
        let candidate = restore_redacted_config(&candidate, &current);
        if let Err(err) = management_continuity_err(&candidate, &r.remote_addr) {
            return write_json(403, &json!({"error": err}));
        }
        match apply::preview_transition(&current, &candidate) {
            Ok(preview) => write_json(200, &preview),
            Err(err) => write_json(422, &json!({"error": err.to_string()})),
        }
    }

    pub(crate) fn handle_discover_setup_interfaces(&self, _r: &mut Request<'_>) -> Response {
        if self.administrator_configured() {
            return Response::error("404 page not found", 404);
        }
        match crate::network::discover() {
            Ok(result) => write_json(200, &result),
            Err(err) => write_json(503, &json!({"error": err})),
        }
    }

    pub(crate) fn handle_discover_interfaces(&self, _r: &mut Request<'_>) -> Response {
        match crate::network::discover() {
            Ok(result) => write_json(200, &result),
            Err(err) => Response::json(503, &json!({"error": err})),
        }
    }
}

/// Deliberately conservative: never reacts to loss, DNS failures or remote
/// hosts; restarts PPPoE only after the link itself stayed down for three
/// minutes, then waits ten minutes before another attempt.
fn run_gateway_auto_recovery(server: std::sync::Weak<Server>, monitor: Arc<Monitor>, stop: crate::shutdown::Shutdown) {
    let (mut down, mut last, mut last_result): (Option<Time>, Option<Time>, String) = (None, None, String::new());
    while stop.sleep(AUTO_RECOVERY_POLL_INTERVAL) {
        let Some(server) = server.upgrade() else { return };
        let cfg = server.engine.get_current_config();
        let summary = monitor.summary();
        let now = Time::now();
        let status = server.engine.get_status();
        let pending = server.engine.get_pending_transaction().is_some();
        let mut view = recovery_status(cfg.wan.enabled, &summary, &status, pending, down, last, now);
        view.last_result = last_result.clone();
        server.store_recovery_view(&view);
        // Unmeasurable evidence restarts the outage clock.
        if !cfg.wan.enabled || !usable_outage_evidence(&summary, now) {
            down = None;
            view.disconnected_since = None;
            view.next_attempt_at = None;
            server.store_recovery_view(&view);
            continue;
        }
        if down.is_none() {
            down = Some(now);
            view.disconnected_since = Some(now);
            server.store_recovery_view(&view);
            continue;
        }
        if !auto_recovery_due(cfg.wan.enabled, &summary, &status, pending, down, last, now) {
            continue;
        }
        last = Some(now);
        view.state = "recovering".into();
        view.reason = "Reconnecting WAN and verifying the address and route".into();
        view.last_attempt_at = last;
        server.store_recovery_view(&view);
        let result = server.engine.run_service_action(Instant::now() + apply::RECONCILE_BUDGET, apply::SERVICE_ACTION_WAN_RECONNECT);
        let duration = Time::now().sub(&now).0.max(0) / 1_000_000_000;
        last_result = if result.is_ok() { "completed" } else { "failed" }.into();
        view.state = "cooldown".into();
        view.last_result = last_result.clone();
        if result.is_ok() {
            view.disconnected_since = None;
            view.next_attempt_at = None;
            down = None;
        }
        server.store_recovery_view(&view);
        let event = if result.is_ok() { "gateway.auto_recovery_completed" } else { "gateway.auto_recovery_failed" };
        server.append_audit(event, "local", details(&[("reason", "pppoe_link_down"), ("duration_seconds", &duration.to_string())]));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn auto_recovery_requires_fresh_continuous_outage_evidence() {
        let now = Time::from_unix(1_800_000_000, 0);
        let mut summary = Summary { available: true, enabled: true, state: "offline".into(), timestamp: now, ..Default::default() };
        let status = EngineStatus::default();
        assert!(usable_outage_evidence(&summary, now));
        assert!(!auto_recovery_due(true, &summary, &status, false, Some(now.add(Duration::minutes(-2))), None, now));
        assert!(auto_recovery_due(true, &summary, &status, false, Some(now.add(Duration::minutes(-3))), None, now));
        assert!(!auto_recovery_due(true, &summary, &status, false, Some(now.add(Duration::minutes(-3))), Some(now.add(Duration::minutes(-5))), now));
        summary.timestamp = now.add(Duration::minutes(-5));
        assert!(!usable_outage_evidence(&summary, now));
        assert_eq!(recovery_status(true, &summary, &status, false, None, None, now).state, "waiting");
    }
}
