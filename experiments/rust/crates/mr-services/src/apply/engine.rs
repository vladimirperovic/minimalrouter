//! The configuration transaction state machine.

use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex, MutexGuard, Weak};
use std::time::{Duration, Instant};

use super::ipc::{op, ApplyRequest, ApplyResponse, Client, TunnelStatus, UnixClient, DEFAULT_SOCKET_PATH};
use super::safety::{inspect_lan_change, validate_transition_safety};
use crate::config::{self, equal_section, requires_confirmation, validate_live_candidate, Revision, SQLiteStore, SystemConfig};
use crate::errorf;
use crate::faultinject;
use crate::goish::errors::{wrapf, Result};
use crate::goish::time::{Duration as GoDuration, Time};
use crate::services;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub enum State {
    #[default]
    Received,
    Planned,
    Generated,
    Snapshotted,
    Applied,
    Verified,
    AwaitingConfirmation,
    Committed,
    RolledBack,
    RecoveryRequired,
    Rejected,
}

impl State {
    pub fn as_str(&self) -> &'static str {
        match self {
            State::Received => "Received",
            State::Planned => "Planned",
            State::Generated => "Generated",
            State::Snapshotted => "Snapshotted",
            State::Applied => "Applied",
            State::Verified => "Verified",
            State::AwaitingConfirmation => "AwaitingConfirmation",
            State::Committed => "Committed",
            State::RolledBack => "RolledBack",
            State::RecoveryRequired => "RecoveryRequired",
            State::Rejected => "Rejected",
        }
    }
}

/// An individual configuration change execution.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct Transaction {
    pub id: String,
    #[serde(rename = "state")]
    pub current_state: State,
    pub config: SystemConfig,
    pub diff: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub error: String,
    pub created_at: Time,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub confirmed_at: Option<Time>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub confirmation_deadline: Option<Time>,
    #[serde(default, skip_serializing_if = "crate::goish::json::is_false")]
    pub firewall_changed: bool,
}

pub(crate) const CONFIRMATION_TIMEOUT: Duration = Duration::from_secs(90);
const ROLLBACK_RETRY_DELAY: Duration = Duration::from_secs(10);
const MAXIMUM_ROLLBACK_ATTEMPTS: u32 = 5;
const PRIVILEGED_APPLY_TIMEOUT: Duration = Duration::from_secs(120);
const PRIVILEGED_APPLY_ATTEMPTS: usize = 2;

/// The wall-clock budget a caller should give reconcile: above
/// PRIVILEGED_APPLY_TIMEOUT * PRIVILEGED_APPLY_ATTEMPTS so a transport retry is
/// never cancelled halfway through.
pub const RECONCILE_BUDGET: Duration = Duration::from_secs(120 * 2 + 30);

type TxRef = Arc<Mutex<Transaction>>;

fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    m.lock().unwrap_or_else(|e| e.into_inner())
}

/// A cancellable one-shot timer (Go's time.AfterFunc).
struct Timer {
    cancelled: Arc<AtomicBool>,
}

impl Timer {
    fn after(delay: Duration, f: impl FnOnce() + Send + 'static) -> Timer {
        let cancelled = Arc::new(AtomicBool::new(false));
        let flag = cancelled.clone();
        std::thread::spawn(move || {
            std::thread::sleep(delay);
            if !flag.load(Ordering::SeqCst) {
                f();
            }
        });
        Timer { cancelled }
    }

    fn stop(&self) {
        self.cancelled.store(true, Ordering::SeqCst);
    }
}

struct PendingChange {
    tx: TxRef,
    previous: SystemConfig,
    timer: Option<Timer>,
    rollback_attempts: u32,
    commit_attempts: u32,
    canonical_committed: bool,
}

/// An immutable management-plane snapshot, readable during a mutation.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct EngineStatus {
    #[serde(rename = "apply_in_progress")]
    pub applying: bool,
    pub recovery_required: bool,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub recovery_reason: String,
    #[serde(rename = "transaction_id", default, skip_serializing_if = "String::is_empty")]
    pub active_transaction_id: String,
    #[serde(rename = "transaction_state", default, skip_serializing_if = "Option::is_none")]
    pub active_state: Option<State>,
}

struct EngineState {
    active_tx: Option<TxRef>,
    applying: bool,
    current_config: SystemConfig,
    pending: Option<PendingChange>,
    recovery_required: bool,
    recovery_reason: String,
}

impl EngineState {
    fn require_recovery(&mut self, reason: String) {
        self.recovery_required = true;
        self.recovery_reason = reason;
    }
}

/// Executes configuration transactions. `operation` serialises mutations;
/// `state` guards the snapshot and is released during privileged calls so
/// status stays readable.
pub struct Engine {
    operation: Mutex<()>,
    state: Mutex<EngineState>,
    store: Option<Arc<SQLiteStore>>,
    client: Arc<dyn Client>,
    this: Weak<Engine>,
}

pub type CommitFn<'a> = &'a dyn Fn(&SystemConfig) -> Result<()>;

impl Engine {
    pub fn new(initial: SystemConfig, store: Option<Arc<SQLiteStore>>) -> Arc<Engine> {
        Self::with_client(initial, store, Arc::new(UnixClient::new(DEFAULT_SOCKET_PATH)))
    }

    pub fn with_client(initial: SystemConfig, store: Option<Arc<SQLiteStore>>, client: Arc<dyn Client>) -> Arc<Engine> {
        Arc::new_cyclic(|this| Engine {
            operation: Mutex::new(()),
            state: Mutex::new(EngineState {
                active_tx: None,
                applying: false,
                current_config: initial,
                pending: None,
                recovery_required: false,
                recovery_reason: String::new(),
            }),
            store,
            client,
            this: this.clone(),
        })
    }

    pub fn store(&self) -> Option<&Arc<SQLiteStore>> {
        self.store.as_ref()
    }

    /// Retries only ambiguous transport failures. The helper deduplicates by
    /// transaction ID, so replaying the exact request is safe.
    fn apply_privileged(&self, deadline: Instant, req: &ApplyRequest) -> Result<ApplyResponse> {
        match req.op.as_str() {
            "APPLY_ALL" | "CONFIRM" => faultinject::run(faultinject::PRE_PRIVILEGED_APPLY),
            "COMMIT_CONFIRMED" => faultinject::run(faultinject::PRE_CANONICAL_ACK),
            "RECONCILE" => faultinject::run(faultinject::DURING_FINAL_RECONCILE),
            _ => {}
        }
        let mut last_err = None;
        for _ in 0..PRIVILEGED_APPLY_ATTEMPTS {
            if Instant::now() >= deadline {
                return Err(last_err.unwrap_or_else(|| errorf!("context deadline exceeded")));
            }
            match self.client.apply(deadline, req) {
                Ok(resp) => {
                    if let Err(e) = resp.validate() {
                        return Err(wrapf("router-applyd returned an invalid outcome", e));
                    }
                    return Ok(resp);
                }
                Err(e) => last_err = Some(e),
            }
        }
        Err(last_err.unwrap_or_else(|| errorf!("router-applyd returned an empty response")))
    }

    /// Releases the state lock for a privileged call, marking `applying`.
    fn privileged<'a>(
        &'a self,
        mut st: MutexGuard<'a, EngineState>,
        timeout: Duration,
        req: &ApplyRequest,
    ) -> (MutexGuard<'a, EngineState>, Result<ApplyResponse>) {
        st.applying = true;
        drop(st);
        let result = self.apply_privileged(Instant::now() + timeout, req);
        let mut st = lock(&self.state);
        st.applying = false;
        (st, result)
    }

    pub fn process_transaction(&self, tx_id: &str, new_cfg: SystemConfig) -> (Transaction, Result<()>) {
        self.process(tx_id, new_cfg, false, None)
    }

    /// The one-time selection of real WAN/LAN interface names. `commit` must
    /// atomically persist the verified network configuration and credential.
    pub fn process_initial_setup(&self, tx_id: &str, new_cfg: SystemConfig, commit: CommitFn<'_>) -> (Transaction, Result<()>) {
        self.process(tx_id, new_cfg, true, Some(commit))
    }

    fn process(&self, tx_id: &str, new_cfg: SystemConfig, allow_interface_change: bool, commit: Option<CommitFn<'_>>) -> (Transaction, Result<()>) {
        let _op = lock(&self.operation);
        let mut st = lock(&self.state);
        let tx: TxRef = Arc::new(Mutex::new(Transaction {
            id: tx_id.into(),
            current_state: State::Received,
            config: new_cfg.clone(),
            created_at: Time::now(),
            firewall_changed: !equal_section(&st.current_config.firewall, &new_cfg.firewall),
            ..Default::default()
        }));
        let previous_revision = st.current_config.revision;
        st.active_tx = Some(tx.clone());
        let (st, result) = self.process_locked(st, &tx, tx_id, new_cfg, allow_interface_change, commit);
        self.audit_transaction(&lock(&tx), previous_revision, "apply");
        drop(st);
        let snapshot = lock(&tx).clone();
        (snapshot, result)
    }

    fn process_locked<'a>(
        &'a self,
        st: MutexGuard<'a, EngineState>,
        tx: &TxRef,
        tx_id: &str,
        mut new_cfg: SystemConfig,
        allow_interface_change: bool,
        commit: Option<CommitFn<'_>>,
    ) -> (MutexGuard<'a, EngineState>, Result<()>) {
        let set = |state: State, error: Option<String>| {
            let mut t = lock(tx);
            t.current_state = state;
            if let Some(error) = error {
                t.error = error;
            }
        };
        macro_rules! reject {
            ($state:expr, $msg:expr) => {{
                let msg: String = $msg;
                set($state, Some(msg.clone()));
                return (st, Err(errorf!("{}", msg)));
            }};
        }
        if st.recovery_required {
            let mut msg = "configuration changes are blocked until canonical reconciliation succeeds".to_string();
            if !st.recovery_reason.is_empty() {
                msg.push_str(": ");
                msg.push_str(&st.recovery_reason);
            }
            reject!(State::RecoveryRequired, msg);
        }
        if st.pending.is_some() {
            reject!(State::Rejected, "another configuration is awaiting confirmation".into());
        }
        let current = st.current_config.clone();
        if new_cfg.revision != current.revision {
            reject!(State::Rejected, format!("stale revision: expected {}, received {}", current.revision, new_cfg.revision));
        }
        if !allow_interface_change && new_cfg.lan.interface != current.lan.interface {
            reject!(State::Rejected, "live LAN interface changes are unsupported; use the local recovery console".into());
        }
        // A cross-subnet change cannot safely migrate already leased DHCP clients.
        if !allow_interface_change && !same_ipv4_network(&current.lan.cidr, &new_cfg.lan.cidr) {
            reject!(State::Rejected, "live LAN subnet changes are unsupported; use the local recovery console".into());
        }
        if new_cfg.system.https_port != current.system.https_port {
            reject!(State::Rejected, "live management-port changes are unsupported".into());
        }
        if new_cfg.system.management_access == "wireguard_only" && current.system.management_access != "wireguard_only" && !current.wireguard.enabled {
            reject!(State::Rejected, "enable and verify WireGuard in a separate transaction before restricting management access".into());
        }
        // Admit exactly the configuration that persistence and startup accept.
        if let Err(err) = validate_live_candidate(&new_cfg, Some(&current)) {
            set(State::Rejected, Some(format!("Validation failed: {}", err)));
            return (st, Err(err));
        }
        if !allow_interface_change {
            if let Err(err) = inspect_lan_change(&current, &new_cfg) {
                set(State::Rejected, Some(err.to_string()));
                return (st, Err(err));
            }
            if let Err(err) = validate_transition_safety(&current, &new_cfg) {
                set(State::Rejected, Some(format!("Transition safety validation failed: {}", err)));
                return (st, Err(err));
            }
        }

        new_cfg.revision = current.revision + 1;
        new_cfg.updated_at = Time::now();
        lock(tx).config = new_cfg.clone();
        set(State::Planned, None);

        let request = match build_apply_request(tx_id, &new_cfg) {
            Ok(req) => req,
            Err((what, err)) => {
                set(State::Rejected, Some(format!("{} generator failed: {}", what, err)));
                return (st, Err(err));
            }
        };
        set(State::Generated, None);

        if let Some(store) = &self.store {
            if let Err(err) = store.create_snapshot(&current) {
                set(State::Rejected, Some(format!("Pre-apply snapshot creation failed: {}", err)));
                return (st, Err(err));
            }
        }
        set(State::Snapshotted, None);

        let store_commit = |cfg: &SystemConfig| -> Result<()> { self.store.as_ref().expect("store").save_config(cfg.clone()) };
        let commit_config: Option<CommitFn<'_>> = match commit {
            Some(c) => Some(c),
            None if self.store.is_some() => Some(&store_commit),
            None => None,
        };
        let mut apply_req = request;
        apply_req.require_confirmation = !allow_interface_change && requires_confirmation(&current, &new_cfg);
        // Canonical persistence always precedes helper last-good persistence
        // when a commit callback exists, including first-run setup.
        apply_req.defer_last_good = !apply_req.require_confirmation && commit_config.is_some();
        let (mut st, result) = self.privileged(st, PRIVILEGED_APPLY_TIMEOUT, &apply_req);
        let resp = match result {
            Ok(resp) => resp,
            Err(err) => {
                let msg = format!("privileged apply outcome remained unknown after retry; recovery or reboot reconciliation is required: {}", err);
                set(State::RecoveryRequired, Some(msg.clone()));
                st.require_recovery(msg.clone());
                return (st, Err(errorf!("{}", msg)));
            }
        };
        if !resp.success {
            let state = if resp.recovery_required {
                State::RecoveryRequired
            } else if resp.rolled_back {
                State::RolledBack
            } else {
                State::Rejected
            };
            let msg = format!("privileged apply failed: {}", resp.error);
            set(state, Some(msg.clone()));
            if state == State::RecoveryRequired {
                st.require_recovery(msg);
            }
            return (st, Err(errorf!("apply rejected by router-applyd: {}", resp.error)));
        }
        set(State::Applied, None);
        if !resp.verified {
            let msg = "router-applyd did not verify the active configuration; recovery is required".to_string();
            set(State::RecoveryRequired, Some(msg.clone()));
            st.require_recovery(msg.clone());
            return (st, Err(errorf!("{}", msg)));
        }
        set(State::Verified, None);
        lock(tx).config = new_cfg.clone();

        if apply_req.require_confirmation {
            let deadline = Time::now().add(GoDuration::from_std(CONFIRMATION_TIMEOUT));
            {
                let mut t = lock(tx);
                t.current_state = State::AwaitingConfirmation;
                t.confirmation_deadline = Some(deadline);
            }
            let id = tx_id.to_string();
            let this = self.this.clone();
            let timer = Timer::after(CONFIRMATION_TIMEOUT, move || {
                if let Some(engine) = this.upgrade() {
                    engine.rollback_expired(&id);
                }
            });
            st.pending = Some(PendingChange {
                tx: tx.clone(),
                previous: current,
                timer: Some(timer),
                rollback_attempts: 0,
                commit_attempts: 0,
                canonical_committed: false,
            });
            return (st, Ok(()));
        }
        if let Some(commit_config) = commit_config {
            faultinject::run(faultinject::PRE_SQLITE_COMMIT);
            if let Err(err) = commit_config(&new_cfg) {
                let mut msg = format!("failed to commit config store: {}", err);
                let mut state = State::RecoveryRequired;
                match build_apply_request(&format!("{}-rollback", tx_id), &st.current_config) {
                    Ok(rollback_req) => {
                        let (next, rollback) = self.privileged(st, PRIVILEGED_APPLY_TIMEOUT, &rollback_req);
                        st = next;
                        match rollback {
                            Ok(r) if r.success && r.verified && !r.recovery_required => {
                                state = State::RolledBack;
                                msg.push_str("; previous configuration was verified restored");
                            }
                            _ => msg.push_str("; rollback could not be verified and recovery is required"),
                        }
                    }
                    Err(_) => msg.push_str("; rollback request could not be generated and recovery is required"),
                }
                set(state, Some(msg.clone()));
                if state == State::RecoveryRequired {
                    st.require_recovery(msg);
                }
                return (st, Err(err));
            }
            faultinject::run(faultinject::POST_SQLITE_COMMIT);
            // The durable store now holds new_cfg: the in-memory canon must
            // follow immediately, before the helper's last-good acknowledgement,
            // so a later Reconcile rebuilds from what is committed and running.
            st.current_config = new_cfg.clone();
            if apply_req.defer_last_good {
                let ack = ApplyRequest {
                    id: format!("{}-commit-canonical-0", tx_id),
                    op: op::commit_confirmed(),
                    revision: new_cfg.revision,
                    config: new_cfg.clone(),
                    skip_wan_verify: true,
                    ..Default::default()
                };
                let (next, ack_result) = self.privileged(st, PRIVILEGED_APPLY_TIMEOUT, &ack);
                st = next;
                let failure = match &ack_result {
                    Err(e) => Some(format!("canonical configuration was committed but helper last-good acknowledgement is unknown: {}", e)),
                    Ok(r) if !r.success || !r.verified => {
                        Some(format!("canonical configuration was committed but the helper last-good acknowledgement failed: {}", r.error))
                    }
                    Ok(_) => None,
                };
                if let Some(msg) = failure {
                    set(State::RecoveryRequired, Some(msg.clone()));
                    st.require_recovery(msg);
                    return (st, Err(errorf!("helper commit acknowledgement failed")));
                }
            }
        }
        set(State::Committed, None);
        st.current_config = new_cfg;
        (st, Ok(()))
    }

    pub fn get_pending_transaction(&self) -> Option<Transaction> {
        let st = lock(&self.state);
        st.pending.as_ref().map(|p| lock(&p.tx).clone())
    }

    pub fn confirm_transaction(&self, tx_id: &str) -> (Option<Transaction>, Result<()>) {
        let _op = lock(&self.operation);
        let st = lock(&self.state);
        let Some(pending) = st.pending.as_ref() else {
            return (None, Err(errorf!("transaction is not awaiting confirmation")));
        };
        if lock(&pending.tx).id != tx_id {
            return (None, Err(errorf!("transaction is not awaiting confirmation")));
        }
        let tx = pending.tx.clone();
        let previous_revision = pending.previous.revision;
        let (st, result) = self.confirm_locked(st, &tx, tx_id);
        self.audit_transaction(&lock(&tx), previous_revision, "confirm");
        drop(st);
        let snapshot = lock(&tx).clone();
        (Some(snapshot), result)
    }

    fn confirm_locked<'a>(&'a self, mut st: MutexGuard<'a, EngineState>, tx: &TxRef, tx_id: &str) -> (MutexGuard<'a, EngineState>, Result<()>) {
        let (canonical_committed, previous) = {
            let p = st.pending.as_ref().expect("pending");
            (p.canonical_committed, p.previous.clone())
        };
        let candidate = lock(tx).config.clone();
        if !canonical_committed {
            if !equal_section(&previous.wg_client, &candidate.wg_client) && candidate.wg_client.enabled {
                let status_req = ApplyRequest {
                    id: format!("{}-wg1-status", tx_id),
                    op: op::wg_tunnel_status(),
                    config: candidate.clone(),
                    tunnel_interface: candidate.wg_client.interface.clone(),
                    ..Default::default()
                };
                let (next, status) = self.privileged(st, Duration::from_secs(15), &status_req);
                st = next;
                let tunnel = match status {
                    Ok(r) if r.success && r.tunnel_status.is_some() => r.tunnel_status,
                    _ => {
                        let msg = "WireGuard client confirmation failed: tunnel status is unavailable".to_string();
                        lock(tx).error = msg.clone();
                        return (st, Err(errorf!("{}", msg)));
                    }
                };
                if let Err(err) = verify_wg_client_status_for_confirmation(tunnel.as_ref(), candidate.wg_client.persistent_keepalive) {
                    lock(tx).error = format!("WireGuard client confirmation failed: {}", err);
                    return (st, Err(err));
                }
            }
            if let Some(store) = &self.store {
                faultinject::run(faultinject::PRE_SQLITE_COMMIT);
                if let Err(err) = store.save_config(candidate.clone()) {
                    let mut t = lock(tx);
                    t.current_state = State::RecoveryRequired;
                    t.error = "management-path confirmation succeeded but canonical configuration could not be committed; retry confirmation or allow verified rollback".into();
                    return (st, Err(wrapf("failed to commit confirmed configuration", err)));
                }
                faultinject::run(faultinject::POST_SQLITE_COMMIT);
            }
            let p = st.pending.as_mut().expect("pending");
            p.canonical_committed = true;
            if let Some(timer) = &p.timer {
                timer.stop();
            }
            st.current_config = candidate.clone();
        }

        let attempt = {
            let p = st.pending.as_mut().expect("pending");
            p.commit_attempts += 1;
            p.commit_attempts
        };
        let commit_req = ApplyRequest {
            id: format!("{}-commit-confirmed-{}", tx_id, attempt),
            op: op::commit_confirmed(),
            revision: candidate.revision,
            config: candidate.clone(),
            skip_wan_verify: true,
            ..Default::default()
        };
        let (next, commit) = self.privileged(st, PRIVILEGED_APPLY_TIMEOUT, &commit_req);
        st = next;
        let failure = match &commit {
            Err(e) => Some(format!("canonical configuration was committed but helper last-good acknowledgement is unknown: {}", e)),
            Ok(r) if !r.success || !r.verified => Some(format!("canonical configuration was committed but helper last-good acknowledgement failed: {}", r.error)),
            Ok(_) => None,
        };
        if let Some(msg) = failure {
            let mut t = lock(tx);
            t.current_state = State::RecoveryRequired;
            t.error = msg.clone();
            drop(t);
            st.require_recovery(msg);
            return (st, Err(errorf!("confirmed helper commit failed")));
        }

        let lan_changed = previous.lan.ip_address != candidate.lan.ip_address || previous.lan.cidr != candidate.lan.cidr;
        if lan_changed {
            let mut finalize = match build_apply_request(&format!("{}-finalize-lan", tx_id), &candidate) {
                Ok(req) => req,
                Err((_, err)) => {
                    let msg = format!("canonical LAN configuration was committed but final federation request could not be generated: {}", err);
                    let mut t = lock(tx);
                    t.current_state = State::RecoveryRequired;
                    t.error = msg.clone();
                    drop(t);
                    st.require_recovery(msg);
                    return (st, Err(err));
                }
            };
            finalize.op = op::reconcile();
            let (next, result) = self.privileged(st, PRIVILEGED_APPLY_TIMEOUT, &finalize);
            st = next;
            let failure = match &result {
                Err(e) => Some(format!("canonical LAN configuration was committed but final runtime reconciliation is unknown: {}", e)),
                Ok(r) if !r.success || !r.verified => {
                    Some(format!("canonical LAN configuration was committed but final runtime reconciliation failed: {}", r.error))
                }
                Ok(_) => None,
            };
            if let Some(msg) = failure {
                let mut t = lock(tx);
                t.current_state = State::RecoveryRequired;
                t.error = msg.clone();
                drop(t);
                st.require_recovery(msg);
                return (st, Err(errorf!("LAN runtime finalization failed")));
            }
        }

        {
            let mut t = lock(tx);
            t.confirmed_at = Some(Time::now());
            t.current_state = State::Committed;
        }
        st.current_config = candidate;
        st.pending = None;
        st.active_tx = Some(tx.clone());
        st.recovery_required = false;
        st.recovery_reason.clear();
        (st, Ok(()))
    }

    fn rollback_expired(&self, tx_id: &str) {
        let _op = lock(&self.operation);
        let mut st = lock(&self.state);
        let Some(pending) = st.pending.as_mut() else { return };
        if lock(&pending.tx).id != tx_id || pending.canonical_committed {
            return;
        }
        let tx = pending.tx.clone();
        let previous = pending.previous.clone();
        pending.rollback_attempts += 1;
        let attempts = pending.rollback_attempts;
        if attempts > MAXIMUM_ROLLBACK_ATTEMPTS {
            let msg = "confirmation deadline expired and automatic rollback attempts were exhausted".to_string();
            {
                let mut t = lock(&tx);
                t.current_state = State::RecoveryRequired;
                t.error = msg.clone();
            }
            st.require_recovery(msg);
            st.pending = None;
            self.audit_transaction(&lock(&tx), previous.revision, "timeout_rollback");
            return;
        }
        let rollback_id = format!("{}-timeout-rollback-{}", tx_id, attempts);
        match build_apply_request(&rollback_id, &previous) {
            Ok(req) => {
                let (next, result) = self.privileged(st, PRIVILEGED_APPLY_TIMEOUT, &req);
                st = next;
                if matches!(&result, Ok(r) if r.success && r.verified) {
                    {
                        let mut t = lock(&tx);
                        t.current_state = State::RolledBack;
                        t.error = "confirmation deadline expired; previous configuration restored".into();
                    }
                    if let Some(timer) = st.pending.as_ref().and_then(|p| p.timer.as_ref()) {
                        timer.stop();
                    }
                    st.pending = None;
                    st.recovery_required = false;
                    st.recovery_reason.clear();
                    self.audit_transaction(&lock(&tx), previous.revision, "timeout_rollback");
                    return;
                }
                let mut t = lock(&tx);
                t.current_state = State::RecoveryRequired;
                t.error = "confirmation deadline expired and privileged rollback failed; retry scheduled while candidate access remains available".into();
            }
            Err(_) => {
                let mut t = lock(&tx);
                t.current_state = State::RecoveryRequired;
                t.error = "confirmation deadline expired and rollback generation failed; retry scheduled while candidate access remains available".into();
            }
        }
        let delay = ROLLBACK_RETRY_DELAY * (1u32 << (attempts - 1));
        let id = tx_id.to_string();
        let this = self.this.clone();
        if let Some(p) = st.pending.as_mut() {
            p.timer = Some(Timer::after(delay, move || {
                if let Some(engine) = this.upgrade() {
                    engine.rollback_expired(&id);
                }
            }));
        }
        self.audit_transaction(&lock(&tx), previous.revision, "timeout_rollback");
    }

    pub fn reconcile(&self, deadline: Instant) -> Result<()> {
        let _op = lock(&self.operation);
        let mut st = lock(&self.state);
        let mut req = build_apply_request(&format!("boot-reconcile-{}", Time::now().unix_nano()), &st.current_config).map_err(|(_, e)| e)?;
        req.op = op::reconcile();
        st.applying = true;
        drop(st);
        let result = self.apply_privileged(deadline, &req);
        let mut st = lock(&self.state);
        st.applying = false;
        let resp = match result {
            Ok(resp) => resp,
            Err(err) => {
                let reason = format!("boot reconciliation failed: {}", err);
                st.require_recovery(reason.clone());
                return Err(errorf!("{}", reason));
            }
        };
        if !resp.success || !resp.verified || resp.recovery_required {
            let reason = format!("boot reconciliation was not verified: {}", resp.error);
            st.require_recovery(reason.clone());
            return Err(errorf!("{}", reason));
        }
        // A verified reconcile completes an already committed candidate, or
        // restores the canonical state when the candidate was unconfirmed.
        if let Some(pending) = st.pending.take() {
            if let Some(timer) = &pending.timer {
                timer.stop();
            }
            {
                let mut t = lock(&pending.tx);
                t.confirmation_deadline = None;
                if pending.canonical_committed {
                    t.confirmed_at = Some(Time::now());
                    t.current_state = State::Committed;
                    t.error.clear();
                } else {
                    t.current_state = State::RolledBack;
                    t.error = "unconfirmed candidate discarded; canonical configuration restored".into();
                }
            }
            st.active_tx = Some(pending.tx.clone());
        }
        st.recovery_required = false;
        st.recovery_reason.clear();
        Ok(())
    }

    /// A detached copy of the canonical in-memory configuration.
    pub fn get_current_config(&self) -> SystemConfig {
        lock(&self.state).current_config.clone()
    }

    pub fn get_status(&self) -> EngineStatus {
        let st = lock(&self.state);
        let mut status = EngineStatus {
            applying: st.applying,
            recovery_required: st.recovery_required,
            recovery_reason: st.recovery_reason.clone(),
            ..Default::default()
        };
        if let Some(tx) = &st.active_tx {
            let t = lock(tx);
            status.active_transaction_id = t.id.clone();
            status.active_state = Some(t.current_state);
        }
        status
    }

    pub(crate) fn action_allowed(&self) -> bool {
        let st = lock(&self.state);
        !st.applying && !st.recovery_required && st.pending.is_none()
    }

    pub(crate) fn operation_lock(&self) -> MutexGuard<'_, ()> {
        lock(&self.operation)
    }

    /// Runs `f` while the active runtime has QoS shaping removed, then
    /// restores the canonical configuration. The canonical state is never
    /// modified, and the restore runs even when the bypass or `f` fails.
    pub fn with_qos_bypassed(&self, f: impl FnOnce() -> Result<()>) -> Result<()> {
        let _op = lock(&self.operation);
        let canonical = {
            let st = lock(&self.state);
            if st.recovery_required {
                if st.recovery_reason.is_empty() {
                    return Err(errorf!("temporary QoS bypass is blocked while recovery is required"));
                }
                return Err(errorf!("temporary QoS bypass is blocked while recovery is required: {}", st.recovery_reason));
            }
            if st.pending.is_some() {
                return Err(errorf!("temporary QoS bypass is blocked while a configuration change awaits confirmation"));
            }
            st.current_config.clone()
        };
        if !canonical.qos.enabled {
            return f();
        }
        let mut bypass = canonical.clone();
        bypass.qos.enabled = false;
        let mut bypass_req = build_apply_request(&format!("speedtest-qos-bypass-{}", Time::now().unix_nano()), &bypass)
            .map_err(|(_, e)| wrapf("build temporary QoS bypass", e))?;
        bypass_req.require_confirmation = false;
        bypass_req.defer_last_good = true;

        // Never return directly from here: the canonical RECONCILE below is the
        // safety boundary that proves the runtime is back in sync.
        let bypass_err = self.apply_temporary_runtime(&bypass_req).err().map(|e| wrapf("activate temporary QoS bypass", e));
        let measurement_err = if bypass_err.is_none() { f().err() } else { None };

        // Restoration ignores request cancellation and also runs after an
        // unsuccessful or ambiguous bypass attempt.
        let restore_err = match build_apply_request(&format!("speedtest-qos-restore-{}", Time::now().unix_nano()), &canonical) {
            Err((_, e)) => Some(wrapf("build canonical QoS restore", e)),
            Ok(mut req) => {
                req.op = op::reconcile();
                req.require_confirmation = false;
                req.defer_last_good = false;
                self.apply_temporary_runtime(&req).err().map(|e| wrapf("restore canonical QoS runtime", e))
            }
        };
        if let Some(err) = &restore_err {
            lock(&self.state).require_recovery(format!("temporary QoS bypass could not restore the canonical runtime: {}", err));
        }
        let joined: Vec<String> = [bypass_err, measurement_err, restore_err].into_iter().flatten().map(|e| e.to_string()).collect();
        if joined.is_empty() { Ok(()) } else { Err(errorf!("{}", joined.join("\n"))) }
    }

    fn apply_temporary_runtime(&self, req: &ApplyRequest) -> Result<()> {
        lock(&self.state).applying = true;
        let result = self.apply_privileged(Instant::now() + PRIVILEGED_APPLY_TIMEOUT, req);
        lock(&self.state).applying = false;
        let resp = result?;
        if !resp.success || !resp.verified {
            if !resp.error.is_empty() {
                return Err(errorf!("router-applyd rejected temporary runtime: {}", resp.error));
            }
            return Err(errorf!("router-applyd did not verify temporary runtime"));
        }
        Ok(())
    }

    /// Audits outcomes separately from HTTP requests, including timer-driven
    /// rollback. Never persists errors, diffs, helper output or config.
    fn audit_transaction(&self, tx: &Transaction, previous: Revision, phase: &str) {
        let Some(store) = &self.store else { return };
        let mut reason = match tx.current_state {
            State::Committed => "configuration_committed",
            State::AwaitingConfirmation => "confirmation_required",
            State::Rejected => "configuration_rejected",
            State::RolledBack => "previous_configuration_restored",
            State::RecoveryRequired => "recovery_required",
            _ => "state_recorded",
        };
        if phase == "timeout_rollback" {
            reason = "confirmation_deadline_expired";
        }
        let mut details: BTreeMap<String, String> = [
            ("transaction_id", tx.id.clone()),
            ("state", tx.current_state.as_str().to_string()),
            ("phase", phase.to_string()),
            ("reason", reason.to_string()),
            ("previous_revision", previous.to_string()),
            ("candidate_revision", tx.config.revision.to_string()),
        ]
        .into_iter()
        .map(|(k, v)| (k.to_string(), v))
        .collect();
        if tx.firewall_changed {
            if let Err(err) = store.append_audit_event("firewall.transaction", "local", &details) {
                log::warn!("[AUDIT] failed to persist firewall outcome: {}", err);
            }
        }
        details.insert("has_error".into(), (!tx.error.is_empty()).to_string());
        if let Err(err) = store.append_audit_event("config.transaction", "local", &details) {
            log::warn!("[AUDIT] failed to persist transaction outcome: {}", err);
        }
    }
}

pub(crate) fn same_ipv4_network(a: &str, b: &str) -> bool {
    match (crate::goish::net::parse_cidr(a), crate::goish::net::parse_cidr(b)) {
        (Some((_, an)), Some((_, bn))) if an.ip.to4().is_some() && bn.ip.to4().is_some() => {
            let (a_ones, a_bits) = an.mask.size();
            let (b_ones, b_bits) = bn.mask.size();
            a_bits == 32 && b_bits == 32 && a_ones == b_ones && an.ip.equal(&bn.ip)
        }
        _ => false,
    }
}

fn verify_wg_client_status_for_confirmation(status: Option<&TunnelStatus>, keepalive: i64) -> Result<()> {
    let Some(status) = status.filter(|s| s.last_handshake > 0) else {
        return Err(errorf!("WireGuard client tunnel has no completed handshake"));
    };
    if keepalive <= 0 {
        return Ok(());
    }
    let max_age = GoDuration::minutes(3).max(GoDuration::secs(keepalive * 3));
    let age = Time::now().sub(&Time::from_unix(status.last_handshake, 0));
    if age > max_age {
        return Err(errorf!("WireGuard client handshake is stale (last {} ago)", age.round(GoDuration::SECOND)));
    }
    Ok(())
}

/// Renders every artifact for an APPLY_ALL request; the error names the
/// generator that failed.
pub(crate) fn build_apply_request(tx_id: &str, cfg: &SystemConfig) -> std::result::Result<ApplyRequest, (&'static str, crate::goish::errors::Error)> {
    let nftables = services::generate_nftables(cfg).map_err(|e| ("nftables", e))?;
    let pppoe = services::generate_pppoe(cfg).map_err(|e| ("pppd", e))?;
    let dnsmasq = services::generate_dnsmasq(cfg).map_err(|e| ("dnsmasq", e))?;
    let hostapd = services::generate_hostapd(cfg).map_err(|e| ("hostapd", e))?;
    let wireguard = services::generate_wireguard(&cfg.wireguard).map_err(|e| ("WireGuard", e))?;
    Ok(ApplyRequest {
        id: tx_id.into(),
        op: op::apply_all(),
        revision: cfg.revision,
        config: cfg.clone(),
        nftables,
        pppoe_peer: pppoe.peer_config,
        pppoe_secret: pppoe.chap_secrets,
        dnsmasq,
        hostapd,
        wireguard,
        ..Default::default()
    })
}

#[allow(dead_code)]
fn _assert_send_sync() {
    fn check<T: Send + Sync>() {}
    check::<Engine>();
    let _ = config::SNAPSHOT_KIND_AUTOMATIC;
}
