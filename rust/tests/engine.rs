//! Transaction engine behaviour, ported from the Go apply package tests.

use std::collections::VecDeque;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use minimalrouter::apply::{ApplyRequest, ApplyResponse, Client, Engine, State, op, preview_transition};
use minimalrouter::config::{SQLiteStore, SystemConfig, default_config};
use minimalrouter::errorf;
use minimalrouter::goish::errors::Result;

/// Replies with scripted outcomes (the last one repeats) and records requests.
struct ScriptedClient {
    script: Mutex<VecDeque<std::result::Result<ApplyResponse, String>>>,
    last: Mutex<std::result::Result<ApplyResponse, String>>,
    requests: Mutex<Vec<ApplyRequest>>,
    delay: Duration,
}

impl ScriptedClient {
    fn ok() -> Arc<ScriptedClient> {
        Self::new(vec![Ok(ApplyResponse { success: true, verified: true, ..Default::default() })])
    }

    fn new(script: Vec<std::result::Result<ApplyResponse, String>>) -> Arc<ScriptedClient> {
        let last = script.last().cloned().unwrap();
        Arc::new(ScriptedClient { script: Mutex::new(script.into()), last: Mutex::new(last), requests: Mutex::new(Vec::new()), delay: Duration::ZERO })
    }

    fn ops(&self) -> Vec<String> {
        self.requests.lock().unwrap().iter().map(|r| r.op.as_str().to_string()).collect()
    }
}

impl Client for ScriptedClient {
    fn apply(&self, _deadline: Instant, req: &ApplyRequest) -> Result<ApplyResponse> {
        self.requests.lock().unwrap().push(req.clone());
        std::thread::sleep(self.delay);
        let next = {
            let mut script = self.script.lock().unwrap();
            match script.pop_front() {
                Some(item) => {
                    *self.last.lock().unwrap() = item.clone();
                    item
                }
                None => self.last.lock().unwrap().clone(),
            }
        };
        match next {
            Ok(mut resp) => {
                resp.id = req.id.clone();
                Ok(resp)
            }
            Err(e) => Err(errorf!("{}", e)),
        }
    }
}

fn ok() -> std::result::Result<ApplyResponse, String> {
    Ok(ApplyResponse { success: true, verified: true, ..Default::default() })
}

fn temp_store() -> (Arc<SQLiteStore>, std::path::PathBuf) {
    let dir = std::env::temp_dir().join(format!("mr-engine-{}-{}", std::process::id(), minimalrouter::goish::time::Time::now().unix_nano()));
    let store = SQLiteStore::new(dir.to_str().unwrap()).unwrap();
    (Arc::new(store), dir)
}

fn gateway_move(cfg: &SystemConfig) -> SystemConfig {
    let mut next = cfg.clone();
    next.lan.ip_address = "192.168.1.2".into();
    next.lan.cidr = "192.168.1.2/24".into();
    next
}

#[test]
fn transaction_lifecycle_requires_confirmation_then_commits() {
    let (store, dir) = temp_store();
    let mut initial = default_config();
    initial.trusted_networks = vec!["192.168.1.0/24".to_string(), "10.0.0.0/24".to_string()].into();
    let client = ScriptedClient::ok();
    let engine = Engine::with_client(initial.clone(), Some(store), client.clone());

    let (tx, result) = engine.process_transaction("tx-test-1", gateway_move(&initial));
    result.unwrap();
    assert_eq!(tx.current_state, State::AwaitingConfirmation);
    assert_eq!(tx.config.revision, initial.revision + 1);
    assert!(tx.confirmation_deadline.is_some());

    let (tx, result) = engine.confirm_transaction("tx-test-1");
    result.unwrap();
    assert_eq!(tx.unwrap().current_state, State::Committed);
    assert_eq!(client.ops(), vec!["APPLY_ALL", "COMMIT_CONFIRMED", "RECONCILE"]);
    let requests = client.requests.lock().unwrap();
    assert!(requests.iter().all(|r| r.config.revision == initial.revision + 1));
    assert!(requests[1].skip_wan_verify);
    assert_eq!(engine.get_current_config().lan.ip_address, "192.168.1.2");
    std::fs::remove_dir_all(dir).ok();
}

#[test]
fn cross_subnet_change_requires_recovery_console() {
    let initial = default_config();
    let client = ScriptedClient::ok();
    let engine = Engine::with_client(initial.clone(), None, client.clone());
    let mut candidate = initial.clone();
    candidate.lan.ip_address = "192.168.2.1".into();
    candidate.lan.cidr = "192.168.2.1/24".into();
    candidate.dhcp.range_start = "192.168.2.100".into();
    candidate.dhcp.range_end = "192.168.2.200".into();
    let (tx, result) = engine.process_transaction("tx-cross-subnet", candidate);
    assert!(result.is_err());
    assert_eq!(tx.current_state, State::Rejected);
    assert!(tx.error.contains("local recovery console"));
    assert!(client.ops().is_empty(), "unsafe candidate reached the privileged helper");
}

#[test]
fn routine_save_commits_store_before_helper_last_good() {
    let (store, dir) = temp_store();
    let initial = store.get_latest_config().unwrap();
    let client = ScriptedClient::ok();
    let engine = Engine::with_client(initial.clone(), Some(store.clone()), client.clone());
    let mut candidate = initial.clone();
    candidate.system.hostname = "edge".into();
    let (tx, result) = engine.process_transaction("tx-routine", candidate);
    result.unwrap();
    assert_eq!(tx.current_state, State::Committed);
    assert_eq!(client.ops(), vec!["APPLY_ALL", "COMMIT_CONFIRMED"]);
    let requests = client.requests.lock().unwrap();
    assert!(requests[0].defer_last_good && !requests[0].require_confirmation);
    assert_eq!(requests[1].id, "tx-routine-commit-canonical-0");
    assert_eq!(store.get_latest_config().unwrap().system.hostname, "edge");
    std::fs::remove_dir_all(dir).ok();
}

#[test]
fn stale_revision_is_rejected_without_helper_contact() {
    let initial = default_config();
    let client = ScriptedClient::ok();
    let engine = Engine::with_client(initial.clone(), None, client.clone());
    let mut candidate = initial.clone();
    candidate.revision += 5;
    let (tx, result) = engine.process_transaction("tx-stale", candidate);
    assert_eq!(result.unwrap_err().to_string(), format!("stale revision: expected {}, received {}", initial.revision, initial.revision + 5));
    assert_eq!(tx.current_state, State::Rejected);
    assert!(client.ops().is_empty());
}

#[test]
fn ambiguous_outcome_blocks_changes_until_reconcile() {
    let initial = default_config();
    let client = ScriptedClient::new(vec![Err("connection reset".into()), Err("connection reset".into()), ok()]);
    let engine = Engine::with_client(initial.clone(), None, client.clone());
    let mut candidate = initial.clone();
    candidate.system.hostname = "edge".into();
    let (tx, result) = engine.process_transaction("tx-ambiguous", candidate.clone());
    assert!(result.is_err());
    assert_eq!(tx.current_state, State::RecoveryRequired);
    // The transport failure was retried with the same transaction ID.
    assert_eq!(client.requests.lock().unwrap().iter().map(|r| r.id.clone()).collect::<Vec<_>>(), vec!["tx-ambiguous", "tx-ambiguous"]);
    assert!(engine.get_status().recovery_required);

    let (blocked, result) = engine.process_transaction("tx-next", candidate);
    assert!(result.is_err());
    assert_eq!(blocked.current_state, State::RecoveryRequired);
    assert!(blocked.error.starts_with("configuration changes are blocked until canonical reconciliation succeeds"));

    engine.reconcile(Instant::now() + Duration::from_secs(5)).unwrap();
    assert!(!engine.get_status().recovery_required);
}

#[test]
fn malformed_helper_outcome_requires_recovery() {
    let initial = default_config();
    let client = ScriptedClient::new(vec![Ok(ApplyResponse { success: true, verified: false, ..Default::default() })]);
    let engine = Engine::with_client(initial.clone(), None, client);
    let mut candidate = initial;
    candidate.system.hostname = "edge".into();
    let (tx, result) = engine.process_transaction("tx-malformed", candidate);
    assert!(result.unwrap_err().to_string().contains("successful response is not verified"));
    assert_eq!(tx.current_state, State::RecoveryRequired);
}

#[test]
fn helper_rejection_is_not_reported_as_rollback() {
    let initial = default_config();
    let client = ScriptedClient::new(vec![Ok(ApplyResponse { success: false, error: "nft preflight failed".into(), ..Default::default() })]);
    let engine = Engine::with_client(initial.clone(), None, client);
    let mut candidate = initial;
    candidate.system.hostname = "edge".into();
    let (tx, result) = engine.process_transaction("tx-rejected", candidate);
    assert_eq!(result.unwrap_err().to_string(), "apply rejected by router-applyd: nft preflight failed");
    assert_eq!(tx.current_state, State::Rejected);
    assert!(!engine.get_status().recovery_required);
}

#[test]
fn config_store_failure_with_verified_rollback_reports_rolled_back() {
    let initial = default_config();
    let client = ScriptedClient::ok();
    let engine = Engine::with_client(initial.clone(), None, client.clone());
    let mut candidate = initial.clone();
    candidate.system.hostname = "edge".into();
    let fail: &dyn Fn(&SystemConfig) -> Result<()> = &|_| Err(errorf!("disk full"));
    let (tx, result) = engine.process_initial_setup("setup-fail", candidate, fail);
    assert!(result.is_err());
    assert_eq!(tx.current_state, State::RolledBack);
    assert!(tx.error.ends_with("previous configuration was verified restored"));
    assert_eq!(client.ops(), vec!["APPLY_ALL", "APPLY_ALL"]);
    assert_eq!(client.requests.lock().unwrap()[1].id, "setup-fail-rollback");
    assert_eq!(engine.get_current_config().system.hostname, initial.system.hostname);
}

#[test]
fn reconcile_discards_unconfirmed_candidate() {
    let initial = default_config();
    let client = ScriptedClient::ok();
    let engine = Engine::with_client(initial.clone(), None, client);
    let (tx, result) = engine.process_transaction("tx-pending", gateway_move(&initial));
    result.unwrap();
    assert_eq!(tx.current_state, State::AwaitingConfirmation);
    engine.reconcile(Instant::now() + Duration::from_secs(5)).unwrap();
    assert!(engine.get_pending_transaction().is_none());
    let status = engine.get_status();
    assert_eq!(status.active_state, Some(State::RolledBack));
    assert_eq!(engine.get_current_config().lan.ip_address, initial.lan.ip_address);
}

#[test]
fn status_remains_readable_during_slow_apply() {
    let initial = default_config();
    let mut client = ScriptedClient::new(vec![ok()]);
    Arc::get_mut(&mut client).unwrap().delay = Duration::from_millis(300);
    let engine = Engine::with_client(initial.clone(), None, client);
    let worker = {
        let engine = engine.clone();
        let mut candidate = initial.clone();
        candidate.system.hostname = "edge".into();
        std::thread::spawn(move || engine.process_transaction("tx-slow", candidate))
    };
    std::thread::sleep(Duration::from_millis(100));
    let started = Instant::now();
    let status = engine.get_status();
    assert!(started.elapsed() < Duration::from_millis(50), "status blocked behind the privileged call");
    assert!(status.applying);
    assert_eq!(status.active_transaction_id, "tx-slow");
    assert_eq!(status.active_state, Some(State::Snapshotted));
    let (tx, result) = worker.join().unwrap();
    result.unwrap();
    assert_eq!(tx.current_state, State::Committed);
}

#[test]
fn qos_bypass_restores_canonical_runtime() {
    let mut initial = default_config();
    initial.qos.enabled = true;
    let client = ScriptedClient::ok();
    let engine = Engine::with_client(initial, None, client.clone());
    engine.with_qos_bypassed(|| Ok(())).unwrap();
    let requests = client.requests.lock().unwrap();
    assert_eq!(requests.len(), 2);
    assert!(!requests[0].config.qos.enabled && requests[0].defer_last_good);
    assert_eq!(requests[1].op, op::reconcile());
    assert!(requests[1].config.qos.enabled);
}

#[test]
fn qos_bypass_marks_recovery_when_restore_fails() {
    let mut initial = default_config();
    initial.qos.enabled = true;
    let client = ScriptedClient::new(vec![ok(), Ok(ApplyResponse { success: false, error: "tc failed".into(), ..Default::default() })]);
    let engine = Engine::with_client(initial, None, client);
    let err = engine.with_qos_bypassed(|| Err(errorf!("speed test failed"))).unwrap_err().to_string();
    assert_eq!(err, "speed test failed\nrestore canonical QoS runtime: router-applyd rejected temporary runtime: tc failed");
    assert!(engine.get_status().recovery_required);
}

#[test]
fn preview_classifies_protected_changes() {
    let initial = default_config();
    let preview = preview_transition(&initial, &gateway_move(&initial)).unwrap();
    assert!(preview.requires_confirmation);
    assert_eq!(preview.risk, "high");
    assert_eq!(preview.rollback_seconds, 90);
    assert_eq!(preview.changes, vec!["LAN".to_string()]);

    let mut hostname = initial.clone();
    hostname.system.hostname = "edge".into();
    let preview = preview_transition(&initial, &hostname).unwrap();
    assert_eq!(preview.risk, "low");
    assert!(!preview.requires_confirmation);

    let same = preview_transition(&initial, &initial).unwrap();
    assert_eq!(same.expected_interruption, "No effective configuration changes detected.");
}

#[test]
fn timer_rollback_restores_previous_configuration() {
    // Exercises the real 90 s confirmation timer only when explicitly asked.
    if std::env::var("MINIMALROUTER_SLOW_TESTS").is_err() {
        return;
    }
    let initial = default_config();
    let client = ScriptedClient::ok();
    let engine = Engine::with_client(initial.clone(), None, client.clone());
    let (_, result) = engine.process_transaction("tx-timeout", gateway_move(&initial));
    result.unwrap();
    std::thread::sleep(Duration::from_secs(92));
    assert!(engine.get_pending_transaction().is_none());
    assert_eq!(engine.get_status().active_state, Some(State::RolledBack));
    assert_eq!(client.requests.lock().unwrap()[1].id, "tx-timeout-timeout-rollback-1");
}
