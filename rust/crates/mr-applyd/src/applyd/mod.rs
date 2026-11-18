#![cfg_attr(not(unix), allow(dead_code, unused_imports))]
//! router-applyd: the root helper with a deliberately narrow local IPC. It
//! performs fixed, allowlisted networking/service operations, records intent
//! and outcomes, verifies applied state and restores approved snapshots.

mod actions;
mod dnsactivity_helper;
mod dnsfilter_helper;
mod dnswire;
mod runtime;
mod startup;
mod state;
mod sys;

use std::sync::{Mutex, MutexGuard, OnceLock};

use crate::apply::{ApplyRequest, ApplyResponse, PROTOCOL_VERSION, op};
use crate::config::{SystemConfig, requires_confirmation, validate_live_candidate};
use crate::errorf;
use crate::faultinject;
use crate::goish::errors::Result;
use crate::goish::time::Time;
use runtime::*;
use state::*;
use sys::*;

/// State serialised by the network apply lock (Go's `applyMu`).
#[derive(Default)]
pub(crate) struct Guarded {
    pub last_transaction: Option<TransactionRecord>,
    /// Undo intent retained if a rename succeeded but directory sync failed.
    pub pause_uncertain: Option<actions::DevicePauseRecord>,
}

static APPLY: Mutex<Guarded> = Mutex::new(Guarded { last_transaction: None, pause_uncertain: None });

pub(crate) fn lock_apply() -> MutexGuard<'static, Guarded> {
    APPLY.lock().unwrap_or_else(|e| e.into_inner())
}

static DAEMON_GUARD: OnceLock<std::result::Result<crate::recovery::Guard, String>> = OnceLock::new();

/// Held for the process lifetime, excluding offline migration before startup
/// reconciliation or any listener can mutate runtime.
pub(crate) fn acquire_daemon_guard() -> Result<()> {
    match DAEMON_GUARD.get_or_init(|| crate::recovery::acquire_daemon_guard().map_err(|e| e.to_string())) {
        Ok(_) => Ok(()),
        Err(e) => Err(errorf!("{}", e)),
    }
}

/// Confirmation mode is limited to changes that can affect the administrator's
/// path back, with the previous LAN interface still present.
fn confirmation_mode_allowed(previous: Option<&SystemConfig>, candidate: &SystemConfig) -> bool {
    match previous {
        Some(prev) if prev.lan.interface == candidate.lan.interface => requires_confirmation(prev, candidate),
        _ => false,
    }
}

/// Validates the requested mode independently of routerd: an ordinary apply
/// cannot suppress a required confirmation by omitting the flag.
fn validate_confirmation_request(req: &ApplyRequest, previous: Option<&SystemConfig>) -> Result<()> {
    if req.defer_last_good && req.op != op::apply_all() {
        return Err(errorf!("only an apply may defer last-good persistence"));
    }
    if req.require_confirmation {
        if req.op != op::apply_all() || req.defer_last_good || !confirmation_mode_allowed(previous, &req.config) {
            return Err(errorf!("confirmation mode is invalid for this change"));
        }
    } else if req.op == op::apply_all() && previous.is_some_and(|p| requires_confirmation(p, &req.config)) {
        return Err(errorf!("this change requires confirmation"));
    }
    Ok(())
}

fn runtime_before_apply<'a>(
    req: &ApplyRequest,
    last_good: Option<&'a SystemConfig>,
    pending: Option<&'a PendingConfirmation>,
) -> Result<Option<&'a SystemConfig>> {
    let Some(pending) = pending else { return Ok(last_good) };
    // A pending candidate has already been activated. Keep accepting the
    // exact last-good rollback sent by older management planes.
    if req.op == op::reconcile() || (!req.require_confirmation && !req.defer_last_good && Some(&req.config) == last_good) {
        return Ok(Some(&pending.config));
    }
    Err(errorf!("finish the pending configuration before applying another change"))
}

fn apply_all(req: &ApplyRequest) -> ApplyResponse {
    let last_good = match normalize_last_good(load_last_good_raw()) {
        Ok(p) => p,
        Err(_) => return recovery_failure(&req.id, "last-good configuration could not be read; canonical reconciliation is required"),
    };
    let last_good = last_good.as_ref();
    if validate_live_candidate(&req.config, last_good).is_err() {
        return failure(&req.id, "privileged validation rejected configuration", false);
    }
    if let Err(err) = validate_confirmation_request(req, last_good) {
        return failure(&req.id, &err.to_string(), false);
    }
    let pending = match load_pending_confirmation() {
        Ok(p) => Some(p),
        Err(err) if is_not_found(&err) => None,
        Err(err) => return pending_load_failure(&req.id, &err),
    };
    let previous = match runtime_before_apply(req, last_good, pending.as_ref()) {
        Ok(previous) => previous,
        Err(err) => return failure(&req.id, &err.to_string(), false),
    };
    let Ok(generated) = generate_artifacts(&req.config) else {
        return failure(&req.id, "configuration generation failed", false);
    };
    if compare_generated(req, &generated).is_err() {
        return failure(&req.id, "candidate mismatch across trust boundary", false);
    }
    if let Err(err) = reject_unimplemented_features(&req.config) {
        return failure(&req.id, &err.to_string(), false);
    }
    let Ok(dir) = CandidateDir::create("candidate-") else {
        return failure(&req.id, "could not create private candidate directory", false);
    };
    let Ok(candidates) = write_candidates(&dir.0, &generated) else {
        return failure(&req.id, "could not write candidate files", false);
    };
    let Ok(changes) = ServicePlan::changes(previous, &req.config, &generated["squid"].data) else {
        return failure(&req.id, "could not compare service configurations", false);
    };
    let plan = changes.verification(&req.op, &req.config);
    if let Err(err) = preflight(&req.config, &candidates, plan) {
        return failure(&req.id, &format!("component preflight failed: {}", safe_error(&err)), false);
    }
    let Ok(previous_files) = capture_previous(&generated) else {
        return failure(&req.id, "could not capture previous artifacts", false);
    };
    let roll_back = |stage: &str, err: &crate::goish::errors::Error| -> ApplyResponse {
        match rollback(last_good, pending.as_ref().map(|p| &p.config), &req.config, &previous_files) {
            Err(rollback_err) => {
                log::warn!("apply transaction {:?} {} failed: {}; rollback failed: {}", req.id, stage, safe_error(err), safe_error(&rollback_err));
                recovery_failure(&req.id, &format!("{} failed and rollback could not be verified", stage))
            }
            Ok(()) if pending.is_some() => recovery_failure(&req.id, &format!("{} failed; provisional state still requires canonical reconciliation", stage)),
            Ok(()) => {
                log::warn!("apply transaction {:?} {} failed and was rolled back: {}", req.id, stage, safe_error(err));
                failure(&req.id, &format!("{} failed; previous configuration restored: {}", stage, safe_error(err)), true)
            }
        }
    };
    let handshake_not_before = crate::goish::time::Time::now().unix();
    if let Err(err) = install_and_activate(&req.config, &generated, previous, req.require_confirmation, changes) {
        return roll_back("apply", &err);
    }
    if let Err(err) = verify_active(&req.config, plan) {
        return roll_back("verification", &err);
    }
    if requires_functional_dns_verification(last_good, &req.config)
        && let Err(err) = verify_functional_dns()
    {
        return roll_back("functional DNS verification", &err);
    }
    if requires_ddns_verification(last_good, &req.config)
        && let Err(err) = verify_ddns_update()
    {
        return roll_back("dynamic DNS verification", &err);
    }
    faultinject::run(faultinject::POST_PROVISIONAL_APPLY);
    if req.require_confirmation || req.defer_last_good {
        let saved = hash_config(&req.config).and_then(|hash| {
            save_pending_confirmation(&PendingConfirmation {
                config_hash: hash,
                config: req.config.clone(),
                handshake_not_before,
                wg_client_changed: previous.is_none_or(|p| p.wg_client != req.config.wg_client),
            })
        });
        if let Err(err) = saved {
            return roll_back("persist pending confirmation", &err);
        }
    } else {
        if let Err(err) = save_last_good(&req.config) {
            return roll_back("persist last-good", &err);
        }
        if let Err(err) = clear_pending_confirmation() {
            if !is_not_found(&err) {
                return recovery_failure(&req.id, "last-good saved but pending state could not be cleared; canonical reconciliation is required");
            }
        }
    }
    ApplyResponse { id: req.id.clone(), success: true, verified: true, logs: "validated, preflighted, applied, and verified".into(), ..Default::default() }
}

fn verify_wg_client_handshake(interface: &str, keepalive: i64, not_before: Option<i64>) -> Result<()> {
    let iface = if interface.is_empty() { "wg1" } else { interface };
    let out =
        run_fixed_output("/usr/bin/wg", &["show", iface, "dump"]).map_err(|e| crate::goish::errors::wrapf("WireGuard client interface unavailable", e))?;
    actions::wg_handshake_fresh(&out, keepalive, not_before)
}

fn confirm_apply(req: &ApplyRequest) -> ApplyResponse {
    if req.config.validate().is_err() {
        return failure(&req.id, "confirmation configuration is invalid", false);
    }
    let pending = match load_pending_confirmation() {
        Ok(p) => p,
        Err(err) => return pending_load_failure(&req.id, &err),
    };
    if hash_config(&req.config).map(|h| h != pending.config_hash).unwrap_or(true) {
        return failure(&req.id, "confirmation does not match pending configuration", false);
    }
    // Keep the old LAN address until the SQLite commit succeeds. Final LAN
    // reconciliation removes the alias once the candidate is canonical.
    if verify_active(&req.config, ServicePlan::core(&req.config)).is_err() {
        return failure(&req.id, "candidate runtime could not be verified", false);
    }
    if req.config.wg_client.enabled {
        // A wrong remote key, endpoint or network must never commit from a
        // handshake-less interface.
        if let Err(err) = verify_wg_client_handshake(
            &req.config.wg_client.interface,
            req.config.wg_client.persistent_keepalive,
            pending.wg_client_changed.then_some(pending.handshake_not_before),
        ) {
            return failure(&req.id, &format!("confirmed WireGuard client tunnel is not functional: {}", safe_error(&err)), false);
        }
    }
    ApplyResponse {
        id: req.id.clone(),
        success: true,
        verified: true,
        logs: "runtime confirmation verified; canonical commit pending".into(),
        ..Default::default()
    }
}

fn commit_confirmed_apply(req: &ApplyRequest) -> ApplyResponse {
    if req.config.validate().is_err() {
        return failure(&req.id, "confirmed commit configuration is invalid", false);
    }
    let pending = match load_pending_confirmation() {
        Ok(p) => p,
        Err(err) => return pending_load_failure(&req.id, &err),
    };
    if hash_config(&req.config).map(|h| h != pending.config_hash).unwrap_or(true) {
        return failure(&req.id, "confirmed commit does not match pending configuration", false);
    }
    // Proves the local management/core runtime, not an ISP that may flap.
    if verify_active(&req.config, ServicePlan::core(&req.config)).is_err() {
        return recovery_failure(&req.id, "confirmed runtime is no longer active; canonical reconciliation is required");
    }
    if save_last_good(&req.config).is_err() {
        return recovery_failure(&req.id, "could not persist canonical last-good configuration");
    }
    if let Err(err) = clear_pending_confirmation() {
        if !is_not_found(&err) {
            return recovery_failure(&req.id, "could not clear pending confirmation; canonical reconciliation is required");
        }
    }
    ApplyResponse {
        id: req.id.clone(),
        success: true,
        verified: true,
        logs: "confirmed configuration committed as canonical last-good".into(),
        ..Default::default()
    }
}

/// Decodes, authorises and executes one request; returns the response.
pub(crate) fn process_request(data: &[u8]) -> ApplyResponse {
    let req: ApplyRequest = match crate::apply::decode_single::<ApplyRequest>(data) {
        Ok(req) => req,
        Err(crate::apply::SingleDecodeError::Trailing) => return failure("", "RPC request must contain exactly one object", false),
        Err(crate::apply::SingleDecodeError::Invalid(_)) => return failure("", "invalid or oversized RPC request", false),
    };
    if req.version != PROTOCOL_VERSION {
        return failure(&req.id, "unsupported RPC protocol version", false);
    }
    if !TRANSACTION_ID_PATTERN.is_match(&req.id) {
        return failure(&req.id, "invalid transaction ID", false);
    }
    match req.op.as_str() {
        "APPLY_ALL" | "CONFIRM" | "COMMIT_CONFIRMED" | "RECONCILE" | "WG_TUNNEL_STATUS" | "FIREWALL_COUNTERS" => {}
        _ => return failure(&req.id, "operation is not allowlisted", false),
    }
    // Telemetry queries are read-only and non-transactional.
    if req.op == op::firewall_counters() {
        return actions::firewall_counters(&req);
    }
    if req.op == op::wg_tunnel_status() {
        return actions::wg_tunnel_status(&req);
    }

    let mut guarded = lock_apply();
    let Ok(config_hash) = hash_request(&req) else {
        return failure(&req.id, "could not fingerprint request", false);
    };
    let canonical_reconcile = req.op == op::reconcile();
    if let Some(memory) = &guarded.last_transaction {
        if let Some(replay) = replay_transaction_response(&req.id, &config_hash, Some(memory), None, canonical_reconcile) {
            return replay;
        }
    }
    let (previous, load_err) = match load_last_transaction() {
        Ok(r) => (Some(r), None),
        Err(e) => (None, Some(e)),
    };
    if let Some(replay) = replay_transaction_response(&req.id, &config_hash, previous.as_ref(), load_err.as_ref(), canonical_reconcile) {
        return replay;
    }
    let intent = TransactionRecord { id: req.id.clone(), config_hash: config_hash.clone(), started_at: Time::now(), ..Default::default() };
    if save_last_transaction(&intent).is_err() {
        return failure(&req.id, "privileged transaction intent could not be persisted", false);
    }
    guarded.last_transaction = Some(intent.clone());

    log::info!("apply transaction {:?} revision {}", req.id, req.revision);
    let resp = match req.op.as_str() {
        "CONFIRM" => confirm_apply(&req),
        "COMMIT_CONFIRMED" => commit_confirmed_apply(&req),
        _ => apply_all(&req),
    };
    let record = TransactionRecord { id: req.id.clone(), config_hash, response: resp, started_at: intent.started_at, completed_at: Time::now() };
    let (record, resp) = persist_transaction_outcome(record, save_last_transaction);
    guarded.last_transaction = Some(record);
    resp
}

#[cfg(unix)]
fn handle_connection(stream: std::os::unix::net::UnixStream) {
    let deadline = std::time::Instant::now() + REQUEST_READ_TIMEOUT;
    let respond = |mut resp: ApplyResponse| {
        resp.timestamp = Time::now().unix();
        // The write deadline is armed only now: a long privileged operation
        // must not consume its own reply budget.
        if let Err(err) = write_json_line(&stream, &resp) {
            log::warn!("apply transaction {:?} outcome could not be delivered: {}", resp.id, err);
        }
    };
    if let Err(err) = validate_peer(&stream) {
        respond(failure("", "unauthorized local peer", false));
        log::warn!("rejected apply peer: {}", err);
        return;
    }
    let data = match read_request(&stream, crate::apply::MAX_REQUEST_BYTES, deadline) {
        Ok(data) => data,
        Err(_) => return respond(failure("", "invalid or oversized RPC request", false)),
    };
    respond(process_request(&data));
}

#[cfg(unix)]
fn bind_listener(path: &str) -> Result<std::os::unix::net::UnixListener> {
    let _ = std::fs::remove_file(path);
    let listener = std::os::unix::net::UnixListener::bind(path)?;
    if let Err(err) = secure_socket_for_routerd(path) {
        let _ = std::fs::remove_file(path);
        return Err(err);
    }
    Ok(listener)
}

#[cfg(unix)]
fn handle_service_action_connection(stream: std::os::unix::net::UnixStream) {
    use crate::apply::{ACTION_INVALID, ServiceActionRequest, ServiceActionResponse};
    let deadline = std::time::Instant::now() + REQUEST_READ_TIMEOUT;
    let respond = |resp: ServiceActionResponse| {
        let _ = write_json_line(&stream, &resp);
    };
    if validate_peer(&stream).is_err() {
        return respond(ServiceActionResponse { error: "unauthorized local peer".into(), ..Default::default() });
    }
    let data = match read_request(&stream, 4097, deadline) {
        Ok(d) if d.len() <= 4096 => d,
        _ => return respond(ServiceActionResponse { code: ACTION_INVALID.into(), error: "invalid or oversized action request".into(), ..Default::default() }),
    };
    let request: ServiceActionRequest = match crate::apply::decode_single(&data) {
        Ok(r) => r,
        Err(crate::apply::SingleDecodeError::Trailing) => {
            return respond(ServiceActionResponse {
                code: ACTION_INVALID.into(),
                error: "action request must contain exactly one object".into(),
                ..Default::default()
            });
        }
        Err(_) => return respond(ServiceActionResponse { code: ACTION_INVALID.into(), error: "invalid action request".into(), ..Default::default() }),
    };
    if let Err(err) = actions::validate_service_action_request(&request) {
        return respond(actions::service_action_failure(&err));
    }
    let mut guarded = lock_apply();
    respond(actions::handle_service_action(&mut guarded, &request));
}

/// PPPoE recreates ppp0 on every reconnect, removing its qdiscs; the ip-up
/// hook signals this helper, which reapplies QoS under the apply lock.
#[cfg(unix)]
fn spawn_qos_signal_handler() {
    let Ok(mut signals) = signal_hook::iterator::Signals::new([signal_hook::consts::SIGUSR1]) else {
        log::warn!("reapply-qos signal handler unavailable");
        return;
    };
    std::thread::spawn(move || {
        for _ in signals.forever() {
            let _guard = lock_apply();
            match load_last_good() {
                Err(err) => log::warn!("reapply-qos signal: no last-good config: {}", err),
                Ok(cfg) if cfg.qos.enabled => {
                    if let Err(err) = apply_qos(&cfg) {
                        log::warn!("reapply-qos signal: {}", err);
                    }
                }
                Ok(cfg) => clear_qos(&cfg),
            }
        }
    });
}

/// The binary entry point.
#[cfg(unix)]
pub fn main() {
    crate::goish::log::init();
    if std::env::args().nth(1).as_deref() == Some("--reapply-qos") {
        // Legacy compatibility sink: QoS reapply now goes through SIGUSR1.
        log::info!("deprecated --reapply-qos invocation ignored; install the signal-based PPP hook");
        return;
    }
    startup::run_if_requested();
    if let Err(err) = acquire_daemon_guard() {
        log::error!("applyd offline migration admission failed: {}", err);
        std::process::exit(1);
    }
    if let Err(err) = harden_process() {
        log::error!("applyd process hardening failed: {}", err);
        std::process::exit(1);
    }
    if let Err(err) = dnsfilter_helper::recover_filter_activation() {
        log::error!("recover interrupted DNS filter activation: {}", err);
        std::process::exit(1);
    }
    spawn_qos_signal_handler();
    log::info!("Starting Minimal Router OS router-applyd (privileged execution helper)");
    if let Err(err) = std::fs::create_dir_all(SOCKET_DIR) {
        log::error!("create socket directory: {}", err);
        std::process::exit(1);
    }
    let listener = match bind_listener(crate::apply::DEFAULT_SOCKET_PATH) {
        Ok(l) => l,
        Err(err) => {
            log::error!("bind Unix socket: {}", err);
            std::process::exit(1);
        }
    };
    log::info!("router-applyd listening on unix://{}", crate::apply::DEFAULT_SOCKET_PATH);
    std::thread::spawn(dnsactivity_helper::run_collector);
    let listeners: [(&'static str, fn(std::os::unix::net::UnixStream), &'static str); 3] = [
        (crate::dnsactivity::SOCKET_PATH, dnsactivity_helper::handle_connection, "DNS activity"),
        (crate::dnsfilter::SOCKET_PATH, dnsfilter_helper::handle_connection, "DNS filter"),
        (crate::apply::SERVICE_ACTION_SOCKET_PATH, handle_service_action_connection, "fixed actions"),
    ];
    for (path, handler, label) in listeners {
        std::thread::spawn(move || match bind_listener(path) {
            Err(err) => log::warn!("{} socket unavailable: {}", label, err),
            Ok(listener) => {
                if path == crate::apply::SERVICE_ACTION_SOCKET_PATH {
                    actions::restore_device_pauses(&mut lock_apply());
                }
                log::info!("router-applyd {} listening on unix://{}", label, path);
                serve_bounded(listener, 8, handler);
            }
        });
    }
    serve_bounded(listener, 8, handle_connection);
}

#[cfg(not(unix))]
pub fn main() {
    eprintln!("router-applyd runs only on the Linux appliance");
    std::process::exit(1);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn confirmation_mode_rules() {
        let current = crate::config::default_config();
        let mut moved = current.clone();
        moved.lan.ip_address = "192.168.1.2".into();
        moved.lan.cidr = "192.168.1.2/24".into();
        let mut req = ApplyRequest { op: op::apply_all(), config: moved.clone(), ..Default::default() };
        assert_eq!(validate_confirmation_request(&req, Some(&current)).unwrap_err().to_string(), "this change requires confirmation");
        req.require_confirmation = true;
        validate_confirmation_request(&req, Some(&current)).unwrap();
        assert!(validate_confirmation_request(&req, None).is_err());
    }

    #[test]
    fn restoring_last_good_reloads_services_from_the_provisional_configuration() {
        let canonical = crate::config::default_config();
        let mut active = canonical.clone();
        active.revision += 1;
        active.lan.ip_address = "192.168.1.9".into();
        active.lan.cidr = "192.168.1.9/24".into();
        active.wifi.enabled = true;
        active.wifi.passphrase = "testing-wifi-configuration".into();
        let pending = PendingConfirmation { config: active, ..Default::default() };
        let mut restore = ApplyRequest { op: op::reconcile(), config: canonical.clone(), ..Default::default() };
        let generated = generate_artifacts(&canonical).unwrap();
        for operation in [op::reconcile(), op::apply_all()] {
            restore.op = operation;
            validate_confirmation_request(&restore, Some(&canonical)).unwrap();
            let before = runtime_before_apply(&restore, Some(&canonical), Some(&pending)).unwrap();
            assert!(ServicePlan::changes(before, &canonical, &generated["squid"].data).unwrap().wifi);
            assert!(dnsmasq_artifacts_changed(&generated, before).unwrap());
        }
        restore.config.system.hostname = "other-change".into();
        assert!(runtime_before_apply(&restore, Some(&canonical), Some(&pending)).is_err());
        let before = runtime_before_apply(&restore, Some(&canonical), None).unwrap();
        assert!(!dnsmasq_artifacts_changed(&generated, before).unwrap());
    }

    #[test]
    fn rejects_malformed_requests_before_privileged_work() {
        assert_eq!(process_request(b"not json").error, "invalid or oversized RPC request");
        assert_eq!(process_request(b"{} {}").error, "RPC request must contain exactly one object");
        assert_eq!(process_request(br#"{"version":2,"id":"x"}"#).error, "unsupported RPC protocol version");
        assert_eq!(process_request(br#"{"version":1,"id":"bad id"}"#).error, "invalid transaction ID");
        assert_eq!(process_request(br#"{"version":1,"id":"tx-1","op":"RUN_SHELL"}"#).error, "operation is not allowlisted");
        assert_eq!(process_request(br#"{"version":1,"id":"tx-1","op":"APPLY_ALL","bogus":true}"#).error, "invalid or oversized RPC request");
    }
}
