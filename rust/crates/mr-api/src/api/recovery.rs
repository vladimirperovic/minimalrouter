//! Restore previews (backup, pfSense, snapshot), the durable restore
//! operation record and its reconciliation against authoritative state.

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use super::*;
use crate::apply::ChangePreview;

const NETWORK_UNVERIFIED: &str = "The network outcome is unverified. Reconcile the router configuration before continuing the restore.";
const NETWORK_ROLLED_BACK: &str = "The network change was rolled back. DNS restoration was not continued.";

#[derive(Clone, Debug, Default, Serialize)]
pub(crate) struct RecoveryAssessment {
    #[serde(flatten)]
    pub preview: ChangePreview,
    pub can_apply: bool,
    pub blockers: Vec<String>,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub(crate) struct RecoveryOperation {
    pub id: String,
    pub source: String,
    pub state: String,
    pub transaction_id: String,
    pub target_revision: config::Revision,
    pub target_checksum: String,
    pub started_at: Time,
    pub updated_at: Time,
    #[serde(rename = "confirmation_deadline", skip_serializing_if = "Option::is_none")]
    pub deadline: Option<Time>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub dns_policy: Option<dnsfilter::Policy>,
    #[serde(skip_serializing_if = "is_zero_u64")]
    pub dns_base_revision: u64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
}

impl RecoveryOperation {
    fn matches_config(&self, cfg: &SystemConfig) -> bool {
        cfg.revision == self.target_revision && configuration_fingerprint(cfg) == self.target_checksum
    }
}

fn is_zero_u64(v: &u64) -> bool {
    *v == 0
}

pub(crate) fn normalize_dns(policy: Option<&dnsfilter::Policy>) -> Option<dnsfilter::Policy> {
    let policy = policy?;
    let mut copy = dnsfilter::default_policy();
    copy.revision = policy.revision;
    copy.categories.extend(policy.categories.iter().map(|(k, v)| (k.clone(), *v)));
    copy.exceptions.extend(policy.exceptions.iter().cloned());
    Some(copy)
}

fn configuration_fingerprint(cfg: &SystemConfig) -> String {
    let mut cfg = cfg.clone();
    cfg.revision = Default::default();
    cfg.updated_at = Time::zero();
    hex::encode(Sha256::digest(crate::goish::json::marshal(&cfg).unwrap_or_default()))
}

fn operation_finished(state: &str) -> bool {
    matches!(state, "completed" | "failed" | "rolled_back" | "needs_review" | "dismissed")
}

fn random_bytes<const N: usize>() -> Option<[u8; N]> {
    let mut bytes = [0u8; N];
    crate::goish::rand::fill(&mut bytes).ok()?;
    Some(bytes)
}

impl Server {
    pub(crate) fn assess_recovery(&self, candidate: &SystemConfig, remote: &str) -> RecoveryAssessment {
        let mut result = RecoveryAssessment::default();
        match apply::preview_transition(&self.engine.get_current_config(), candidate) {
            Ok(preview) => result.preview = preview,
            Err(err) => result.blockers.push(err.to_string()),
        }
        if let Err(err) = management_continuity_err(candidate, remote) {
            result.blockers.push(err);
        }
        if self.engine.get_pending_transaction().is_some() {
            result.blockers.push("Confirm or roll back the pending network change first.".into());
        }
        if self.engine.get_status().recovery_required {
            result.blockers.push("Canonical reconciliation is required before restoring.".into());
        }
        result.can_apply = result.blockers.is_empty();
        result
    }

    pub(crate) fn recovery_preview(
        &self,
        r: &mut Request<'_>,
        mut candidate: SystemConfig,
        kind: &str,
        policy: Option<dnsfilter::Policy>,
        report: Option<config::PfSenseImportReport>,
    ) -> Response {
        let Ok(session) = self.request_session(r) else { return Response::error("Authenticated session required", 401) };
        let Some(entropy) = random_bytes::<24>() else { return Response::error("Preview unavailable", 500) };
        let id = base64::Engine::encode(&base64::engine::general_purpose::URL_SAFE_NO_PAD, entropy);
        candidate.revision = self.engine.get_current_config().revision;
        let expires = Time::now().add(Duration::minutes(10));
        {
            let mut state = self.state();
            let now = Time::now();
            state.pending_imports.retain(|_, p| now.before(&p.expires_at) && p.session_id != session.id);
            if state.pending_imports.len() >= 32 {
                return Response::error("Too many active previews; retry later", 503);
            }
            state.pending_imports.insert(
                id.clone(),
                PendingImport {
                    session_id: session.id.clone(),
                    config: candidate.clone(),
                    expires_at: expires,
                    kind: kind.into(),
                    dns_policy: normalize_dns(policy.as_ref()),
                },
            );
        }
        let mut result = json!({
            "import_id": id, "source": kind, "base_revision": candidate.revision, "expires_at": expires.utc(), "expires_in_seconds": 600,
            "candidate": redact_config(&candidate), "assessment": self.assess_recovery(&candidate, &r.remote_addr), "dns_filter": normalize_dns(policy.as_ref()),
        });
        if let Some(mut report) = report {
            report.config = redact_config(&candidate);
            result["report"] = serde_json::to_value(&report).unwrap_or(Value::Null);
        }
        write_json(200, &result)
    }

    pub(crate) fn apply_recovery_preview(&self, r: &mut Request<'_>, kind: &str) -> Response {
        let Ok(session) = self.request_session(r) else { return Response::error("Authenticated session required", 401) };
        let id = r.path_value("id");
        let pending = {
            let mut state = self.state();
            // A wrong session must not consume another operator's preview.
            match state.pending_imports.get(&id) {
                Some(p) if p.session_id == session.id && p.kind == kind && Time::now().before(&p.expires_at) => state.pending_imports.remove(&id),
                _ => None,
            }
        };
        let Some(pending) = pending else { return Response::error("Preview expired or replaced; validate again", 404) };
        if pending.config.revision != self.engine.get_current_config().revision {
            return Response::error("Configuration changed after preview; validate again", 409);
        }
        let assessment = self.assess_recovery(&pending.config, &r.remote_addr);
        if !assessment.can_apply {
            return write_json(422, &json!({"error": "Restore is blocked", "assessment": assessment}));
        }
        self.perform_recovery(r, pending.config, kind, pending.dns_policy)
    }

    /// Reconciles operation metadata from authoritative state. Observed
    /// final outcomes are retained; status reads never start work. Callers
    /// hold `recovery_mu`.
    pub(crate) fn recovery_operation(&self) -> Result<Option<RecoveryOperation>> {
        let store = self.engine_store().ok_or_else(|| crate::errorf!("Recovery store unavailable"))?;
        let Some(mut op) = store.read_recovery_operation::<RecoveryOperation>()? else { return Ok(None) };
        if operation_finished(&op.state) {
            return Ok(Some(op));
        }
        let previous_state = op.state.clone();
        self.reconcile_operation(&mut op);
        if op.state != previous_state && (operation_finished(&op.state) || op.state == "dns_failed") {
            op.updated_at = Time::now_utc();
            store.save_recovery_operation(&op).map_err(|e| crate::goish::errors::wrapf("could not retain recovery outcome", e))?;
        }
        Ok(Some(op))
    }

    fn restored_network_active(&self, op: &mut RecoveryOperation) -> bool {
        let (current, status, pending) = self.engine.recovery_snapshot();
        if status.recovery_required || status.applying {
            if status.recovery_required && matches!(op.state.as_str(), "applying" | "awaiting_confirmation") {
                op.state = "applying".into();
                op.deadline = None;
                op.error = NETWORK_UNVERIFIED.into();
            }
            return false;
        }
        if let Some(tx) = pending.as_ref().filter(|tx| tx.id == op.transaction_id) {
            op.state = "awaiting_confirmation".into();
            op.deadline = tx.confirmation_deadline;
            return false;
        }
        op.deadline = None;
        if !op.matches_config(&current) {
            op.state = "needs_review".into();
            op.error = "The restored configuration is no longer active. Inspect the outcome before starting a new restore.".into();
            if status.active_transaction_id == op.transaction_id && status.active_state == Some(apply::State::RolledBack) {
                op.state = "rolled_back".into();
                op.error = NETWORK_ROLLED_BACK.into();
            }
            return false;
        }
        pending.is_none()
    }

    fn reconcile_operation(&self, op: &mut RecoveryOperation) {
        if !self.restored_network_active(op) {
            return;
        }
        if op.dns_policy.is_none() {
            op.state = "completed".into();
            op.error.clear();
            return;
        }
        if op.state != "dns_running" {
            if op.state != "dns_failed" {
                op.state = "dns_pending".into();
                op.error.clear();
            }
            return;
        }
        let Some(filter) = self.dns_filter() else {
            op.state = "dns_failed".into();
            op.error = "DNS protection unavailable. Retry when the service is ready.".into();
            return;
        };
        let Ok(status) = filter.status(Instant::now() + std::time::Duration::from_secs(30)) else {
            op.error = "DNS operation status unavailable; refresh to check its outcome.".into();
            return;
        };
        // A network change can finish during the DNS helper's status read.
        if status.updating || status.applied.health_checking || !self.restored_network_active(op) {
            return;
        }
        if status.applied.healthy
            && !status.applied.activation_pending
            && status.applied.policy.revision > op.dns_base_revision
            && op.dns_policy.as_ref().is_some_and(|expected| dnsfilter::policy_hash(&status.applied.policy) == dnsfilter::policy_hash(expected))
        {
            op.state = "completed".into();
            op.error.clear();
        } else {
            op.state = "dns_failed".into();
            op.error = if status.error.is_empty() {
                "DNS restoration did not finish with the expected healthy policy. Review DNS Filter, then retry.".into()
            } else {
                status.error.clone()
            };
        }
    }

    pub(crate) fn perform_recovery(&self, r: &mut Request<'_>, candidate: SystemConfig, source: &str, policy: Option<dnsfilter::Policy>) -> Response {
        let _guard = self.recovery_mu.lock().unwrap_or_else(|e| e.into_inner());
        let previous = match self.recovery_operation() {
            Ok(previous) => previous,
            Err(_) => return Response::error("Recovery state unavailable; no change applied", 503),
        };
        if previous.is_some_and(|p| !operation_finished(&p.state)) {
            return Response::error("Finish or dismiss the existing restore operation first", 409);
        }
        let Some(entropy) = random_bytes::<16>() else { return Response::error("Recovery unavailable", 500) };
        let id = format!("recovery-{}", hex::encode(entropy));
        let now = Time::now_utc();
        let mut op = RecoveryOperation {
            id: id.clone(),
            source: source.into(),
            state: "applying".into(),
            transaction_id: id.clone(),
            target_revision: candidate.revision + 1,
            target_checksum: configuration_fingerprint(&candidate),
            started_at: now,
            updated_at: now,
            dns_policy: normalize_dns(policy.as_ref()),
            ..Default::default()
        };
        let Some(store) = self.engine_store() else { return Response::error("Cannot persist restore operation; no change applied", 503) };
        if store.save_recovery_operation(&op).is_err() {
            return Response::error("Cannot persist restore operation; no change applied", 503);
        }
        let (tx, apply_result) = self.engine.process_transaction(&id, candidate);
        self.audit_config_request(r, Some(&tx));
        op.updated_at = Time::now_utc();
        let awaiting = tx.current_state == apply::State::AwaitingConfirmation;
        if tx.current_state == apply::State::RecoveryRequired {
            // SQLite may already contain the target. A later verified
            // reconciliation must still be able to resume this backup.
            op.state = "applying".into();
            op.error = NETWORK_UNVERIFIED.into();
        } else if tx.current_state == apply::State::RolledBack {
            op.state = "rolled_back".into();
            op.error = NETWORK_ROLLED_BACK.into();
        } else if apply_result.is_err() {
            op.state = "failed".into();
            op.error = "Configuration restore failed; inspect the transaction and retry a fresh preview.".into();
        } else if awaiting {
            op.state = "awaiting_confirmation".into();
            op.deadline = tx.confirmation_deadline;
        } else if policy.is_some() {
            op.state = "dns_pending".into();
        } else {
            op.state = "completed".into();
        }
        if store.save_recovery_operation(&op).is_err() {
            return Response::error("Restore was submitted but its record could not be updated; inspect Recovery status before retrying", 503);
        }
        if let Err(err) = apply_result {
            return write_json(422, &json!({"error": err.to_string(), "operation": op, "tx": redact_transaction(Some(&tx))}));
        }
        // Preserve the existing transaction response contract.
        write_json(if awaiting { 202 } else { 200 }, &redact_transaction(Some(&tx)))
    }

    pub(crate) fn handle_recovery_status(&self, _r: &mut Request<'_>) -> Response {
        let _guard = self.recovery_mu.lock().unwrap_or_else(|e| e.into_inner());
        let Ok(op) = self.recovery_operation() else { return Response::error("Recovery status unavailable", 503) };
        let Some(store) = self.engine_store() else { return Response::error("Recovery status unavailable", 503) };
        let Ok(last) = store.last_backup_export() else { return Response::error("Backup export history unavailable", 503) };
        let Ok(snapshots) = store.list_snapshots() else { return Response::error("Snapshot history unavailable", 503) };
        let (current, engine, pending) = self.engine.recovery_snapshot();
        let pending = pending.map(|tx| json!({"id": tx.id, "state": tx.current_state, "confirmation_deadline": tx.confirmation_deadline, "management_access": tx.config.system.management_access}));
        write_json(
            200,
            &json!({
                "generated_at": Time::now_utc(), "revision": current.revision,
                "last_backup_export_at": last, "backup_export_note": "Server export recorded; file storage and restore testing are not verified.",
                "snapshot_count": snapshots.len(), "retention": {"manual": 20, "automatic": 20}, "pending": pending, "operation": op, "engine": engine,
            }),
        )
    }

    pub(crate) fn handle_recovery_dns(&self, r: &mut Request<'_>) -> Response {
        let _guard = self.recovery_mu.lock().unwrap_or_else(|e| e.into_inner());
        let id = r.path_value("id");
        let op = match self.recovery_operation() {
            Ok(Some(op)) if op.id == id => op,
            Ok(_) => return Response::error("Restore operation unavailable", 404),
            Err(_) => return Response::error("Recovery status unavailable", 503),
        };
        let engine = self.engine.get_status();
        if self.engine.get_pending_transaction().is_some()
            || engine.recovery_required
            || engine.applying
            || (op.state != "dns_pending" && op.state != "dns_failed")
        {
            return Response::error("Confirm the restored network configuration before continuing DNS restoration", 409);
        }
        let Some(filter) = self.dns_filter() else { return Response::error("DNS protection unavailable", 503) };
        let Ok(status) = filter.status(Instant::now() + std::time::Duration::from_secs(30)) else { return Response::error("DNS status unavailable", 503) };
        if status.updating {
            return Response::error("Wait for the current DNS operation to finish", 409);
        }
        let conflict = || Response::error("Restored network configuration is no longer confirmed; check Recovery status", 409);
        self.engine
            .with_confirmed_config(|cfg| {
                if !op.matches_config(cfg) {
                    return conflict();
                }
                self.submit_recovery_dns(r, op, &filter, status.applied.policy.revision)
            })
            .unwrap_or_else(conflict)
    }

    fn submit_recovery_dns(&self, r: &Request<'_>, mut op: RecoveryOperation, filter: &Arc<dnsfilter::Service>, base_revision: u64) -> Response {
        op.dns_base_revision = base_revision;
        op.state = "dns_running".into();
        op.error.clear();
        op.updated_at = Time::now_utc();
        let Some(store) = self.engine_store() else { return Response::error("Cannot persist DNS continuation", 503) };
        if store.save_recovery_operation(&op).is_err() {
            return Response::error("Cannot persist DNS continuation", 503);
        }
        let mut policy = normalize_dns(op.dns_policy.as_ref()).unwrap_or_default();
        policy.revision = base_revision;
        if let Err(err) = filter.start(&policy, false) {
            op.state = "dns_failed".into();
            op.error = err.to_string();
            if store.save_recovery_operation(&op).is_err() {
                return Response::error("Could not record the DNS failure; check Recovery status", 503);
            }
            return Response::error(&err.to_string(), 409);
        }
        self.append_audit("recovery.dns_requested", &audit_actor(&r.remote_addr), details(&[("operation_id", &op.id)]));
        write_json(202, &op)
    }

    pub(crate) fn handle_recovery_dismiss(&self, r: &mut Request<'_>) -> Response {
        let _guard = self.recovery_mu.lock().unwrap_or_else(|e| e.into_inner());
        let id = r.path_value("id");
        let mut op = match self.recovery_operation() {
            Ok(Some(op)) if op.id == id => op,
            Ok(_) => return Response::error("Restore operation unavailable", 404),
            Err(_) => return Response::error("Recovery status unavailable", 503),
        };
        if self.engine.get_pending_transaction().is_some() || op.state == "dns_running" || op.state == "applying" {
            return Response::error("Wait for the active operation to finish", 409);
        }
        op.state = "dismissed".into();
        op.dns_policy = None;
        op.error = "DNS continuation dismissed by the operator.".into();
        op.updated_at = Time::now_utc();
        match self.engine_store().map(|s| s.save_recovery_operation(&op)) {
            Some(Ok(())) => write_json(200, &op),
            _ => Response::error("Could not dismiss operation", 503),
        }
    }

    fn snapshot_candidate(&self, id: &str) -> Result<SystemConfig> {
        let store = self.engine_store().ok_or_else(|| crate::errorf!("Snapshot store unavailable"))?;
        let snap = store.get_snapshot(id)?;
        let mut cfg: SystemConfig = crate::goish::json::unmarshal(snap.config_json.as_bytes()).map_err(|_| crate::errorf!("Snapshot is corrupted"))?;
        cfg.migrate_legacy_fields();
        cfg.revision = self.engine.get_current_config().revision;
        Ok(cfg)
    }

    pub(crate) fn handle_snapshot_preview(&self, r: &mut Request<'_>) -> Response {
        let id = r.path_value("id");
        match self.snapshot_candidate(&id) {
            Ok(cfg) => write_json(
                200,
                &json!({"snapshot_id": id, "source": "snapshot", "base_revision": cfg.revision, "candidate": redact_config(&cfg), "assessment": self.assess_recovery(&cfg, &r.remote_addr)}),
            ),
            Err(err) => snapshot_read_error(&err),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    struct NetworkClient {
        fail_commit: bool,
    }

    impl apply::Client for NetworkClient {
        fn apply(&self, _deadline: Instant, req: &apply::ApplyRequest) -> Result<apply::ApplyResponse> {
            let success = !(self.fail_commit && req.op == apply::op::commit_confirmed());
            Ok(apply::ApplyResponse {
                id: req.id.clone(),
                success,
                verified: success,
                error: if success { String::new() } else { "commit not acknowledged".into() },
                ..Default::default()
            })
        }
    }

    #[derive(Default)]
    struct DnsHelper {
        applied: Mutex<dnsfilter::Applied>,
        request_thread: Option<std::thread::ThreadId>,
        on_status: Mutex<Option<Box<dyn FnOnce() + Send>>>,
    }

    impl dnsfilter::Helper for DnsHelper {
        fn call(
            &self,
            _deadline: Instant,
            req: dnsfilter::Request,
            _emit: Option<dnsfilter::Emit<'_>>,
        ) -> Result<dnsfilter::Applied, (dnsfilter::Applied, crate::goish::errors::Error)> {
            if req.operation == "status"
                && self.request_thread == Some(std::thread::current().id())
                && let Some(change) = self.on_status.lock().unwrap().take()
            {
                change();
            }
            Ok(self.applied.lock().unwrap().clone())
        }
    }

    fn server(tag: &str, fail_commit: bool) -> (Arc<Server>, std::path::PathBuf) {
        let dir = std::env::temp_dir().join(format!("mr-restore-{tag}-{}-{}", std::process::id(), Time::now().unix_nano()));
        let store = Arc::new(SQLiteStore::new(dir.to_str().unwrap()).unwrap());
        let cfg = store.get_latest_config().unwrap();
        (Server::new(Engine::with_client(cfg, Some(store), Arc::new(NetworkClient { fail_commit }))), dir)
    }

    fn dns_restore(server: &Server, state: &str, policy: dnsfilter::Policy) {
        let cfg = server.engine.get_current_config();
        server
            .engine_store()
            .unwrap()
            .save_recovery_operation(&RecoveryOperation {
                id: "restore".into(),
                state: state.into(),
                target_revision: cfg.revision,
                target_checksum: configuration_fingerprint(&cfg),
                dns_policy: Some(policy),
                dns_base_revision: 4,
                ..Default::default()
            })
            .unwrap();
    }

    #[test]
    fn interrupted_network_commit_resumes_the_backup_after_verified_reconciliation() {
        let (server, dir) = server("commit", true);
        let mut cfg = server.engine.get_current_config();
        cfg.system.hostname = "restored-router".into();
        let mut body = std::io::Cursor::new(Vec::new());
        let mut req = Request::synthetic("POST", "/restore", &mut body, 0);
        let resp = server.perform_recovery(&mut req, cfg, "backup", Some(dnsfilter::default_policy()));
        assert!(resp.status >= 400);
        assert!(server.engine.get_status().recovery_required);
        assert_eq!(server.recovery_operation().unwrap().unwrap().state, "applying");
        server.engine.reconcile(Instant::now() + std::time::Duration::from_secs(2)).unwrap();
        let reopened = Server::new(server.engine.clone());
        assert_eq!(reopened.recovery_operation().unwrap().unwrap().state, "dns_pending");
        drop(reopened);
        drop(server);
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn dns_restore_waits_for_the_probe_and_recognizes_equivalent_policy() {
        let (server, dir) = server("dns", false);
        let mut policy = dnsfilter::default_policy();
        policy.categories.insert("ads".into(), false);
        dns_restore(&server, "dns_running", policy);
        let helper = Arc::new(DnsHelper::default());
        {
            let mut applied = helper.applied.lock().unwrap();
            applied.policy.revision = 5;
            applied.health_checking = true;
        }
        server.configure_dns_filter(Some(dnsfilter::Service::open(dir.to_str().unwrap(), helper.clone(), None).unwrap()));
        assert_eq!(server.recovery_operation().unwrap().unwrap().state, "dns_running");
        {
            let mut applied = helper.applied.lock().unwrap();
            applied.health_checking = false;
            applied.healthy = true;
        }
        assert_eq!(server.recovery_operation().unwrap().unwrap().state, "completed");
        let mut different = dnsfilter::default_policy();
        different.categories.insert("ads".into(), true);
        dns_restore(&server, "dns_running", different);
        assert_eq!(server.recovery_operation().unwrap().unwrap().state, "dns_failed");
        server.dns_filter().unwrap().close();
        drop(server);
        std::fs::remove_dir_all(dir).unwrap();
    }

    fn change_network_on_dns_read(server: &Server, dir: &std::path::Path) {
        let engine = server.engine.clone();
        // The DNS service also polls in the background. Advance the network
        // only inside this handler's status read.
        let helper = Arc::new(DnsHelper { request_thread: Some(std::thread::current().id()), ..Default::default() });
        *helper.on_status.lock().unwrap() = Some(Box::new(move || {
            let mut cfg = engine.get_current_config();
            cfg.system.hostname = "changed-during-dns-check".into();
            engine.process_transaction("other-save", cfg).1.unwrap();
        }));
        {
            let mut applied = helper.applied.lock().unwrap();
            applied.healthy = true;
            applied.policy.revision = 5;
        }
        server.configure_dns_filter(Some(dnsfilter::Service::open(dir.to_str().unwrap(), helper, None).unwrap()));
    }

    #[test]
    fn superseded_network_restore_cannot_start_dns_after_a_slow_status_read() {
        let (server, dir) = server("stale", false);
        dns_restore(&server, "dns_pending", dnsfilter::default_policy());
        change_network_on_dns_read(&server, &dir);
        let mut body = std::io::Cursor::new(Vec::new());
        let mut req = Request::synthetic("POST", "/restore/dns", &mut body, 0);
        req.params.push(("id".into(), "restore".into()));
        assert_eq!(server.handle_recovery_dns(&mut req).status, 409);
        assert_eq!(server.recovery_operation().unwrap().unwrap().state, "needs_review");
        assert!(server.dns_filter().unwrap().history().is_empty());
        server.dns_filter().unwrap().close();
        drop(server);
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn completed_dns_cannot_finalize_a_superseded_network_restore() {
        let (server, dir) = server("dns-outcome", false);
        dns_restore(&server, "dns_running", dnsfilter::default_policy());
        change_network_on_dns_read(&server, &dir);
        assert_eq!(server.recovery_operation().unwrap().unwrap().state, "needs_review");
        server.dns_filter().unwrap().close();
        drop(server);
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn backup_preview_lifetime_survives_wall_clock_changes_in_both_directions() {
        let (server, dir) = server("preview", false);
        let mut body = std::io::Cursor::new(Vec::new());
        let mut req = Request::synthetic("POST", "/backup/preview", &mut body, 0);
        req.session = Some(auth::Session { id: "local-preview".into(), ..Default::default() });
        for expired in [false, true] {
            let cfg = server.engine.get_current_config();
            assert_eq!(server.recovery_preview(&mut req, cfg.clone(), "backup", None, None).status, 200);
            let id = {
                let mut state = server.state();
                let (id, preview) = state.pending_imports.iter_mut().next().unwrap();
                if expired {
                    preview.expires_at = Time::now().add(Duration::secs(-1));
                    preview.expires_at.0 = Time::now_utc().add(Duration::hours(1)).0;
                } else {
                    preview.expires_at.0 = Time::from_unix(1000, 0).0;
                }
                id.clone()
            };
            req.params = vec![("id".into(), id)];
            let result = server.apply_recovery_preview(&mut req, "backup");
            assert_eq!(result.status, if expired { 404 } else { 200 });
            assert_eq!(server.engine.get_current_config().revision, cfg.revision + u64::from(!expired));
        }
        drop(server);
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn unreadable_restore_record_is_reported_as_unavailable() {
        let (server, dir) = server("unavailable", false);
        let mut body = std::io::Cursor::new(Vec::new());
        let mut req = Request::synthetic("POST", "/restore", &mut body, 0);
        req.params.push(("id".into(), "restore".into()));
        assert_eq!(server.handle_recovery_dns(&mut req).status, 404);
        let db = rusqlite::Connection::open(dir.join("minimalrouter.db")).unwrap();
        db.execute("INSERT INTO recovery_state(key,value) VALUES ('operation','incomplete')", []).unwrap();
        assert_eq!(server.handle_recovery_dns(&mut req).status, 503);
        assert_eq!(server.handle_recovery_dismiss(&mut req).status, 503);
        drop(db);
        drop(server);
        std::fs::remove_dir_all(dir).unwrap();
    }
}
