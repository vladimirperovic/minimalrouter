//! Restore previews (backup, pfSense, snapshot), the durable restore
//! operation record and its reconciliation against authoritative state.

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use super::*;
use crate::apply::ChangePreview;

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

    pub(crate) fn recovery_preview(&self, r: &mut Request<'_>, mut candidate: SystemConfig, kind: &str, policy: Option<dnsfilter::Policy>, report: Option<config::PfSenseImportReport>) -> Response {
        let Ok(session) = self.request_session(r) else { return Response::error("Authenticated session required", 401) };
        let Some(entropy) = random_bytes::<24>() else { return Response::error("Preview unavailable", 500) };
        let id = base64::Engine::encode(&base64::engine::general_purpose::URL_SAFE_NO_PAD, entropy);
        candidate.revision = self.engine.get_current_config().revision;
        let expires = Time::now_utc().add(Duration::minutes(10));
        {
            let mut state = self.state();
            let now = Time::now();
            state.pending_imports.retain(|_, p| !now.after(&p.expires_at) && p.session_id != session.id);
            if state.pending_imports.len() >= 32 {
                return Response::error("Too many active previews; retry later", 503);
            }
            state.pending_imports.insert(id.clone(), PendingImport { session_id: session.id.clone(), config: candidate.clone(), expires_at: expires, kind: kind.into(), dns_policy: normalize_dns(policy.as_ref()) });
        }
        let mut result = json!({
            "import_id": id, "source": kind, "base_revision": candidate.revision, "expires_at": expires, "expires_in_seconds": 600,
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
                Some(p) if p.session_id == session.id && p.kind == kind && !Time::now().after(&p.expires_at) => state.pending_imports.remove(&id),
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

    fn reconcile_operation(&self, op: &mut RecoveryOperation) {
        if let Some(tx) = self.engine.get_pending_transaction().filter(|tx| tx.id == op.transaction_id) {
            op.state = "awaiting_confirmation".into();
            op.deadline = tx.confirmation_deadline;
            return;
        }
        let current = self.engine.get_current_config();
        if current.revision != op.target_revision || configuration_fingerprint(&current) != op.target_checksum {
            let status = self.engine.get_status();
            op.state = "needs_review".into();
            op.error = "The restored configuration is no longer active. Inspect the outcome before starting a new restore.".into();
            if status.active_transaction_id == op.transaction_id && status.active_state == Some(apply::State::RolledBack) {
                op.state = "rolled_back".into();
                op.error = "The network change was rolled back. DNS restoration was not continued.".into();
            }
            return;
        }
        op.deadline = None;
        if op.dns_policy.is_none() {
            op.state = "completed".into();
            return;
        }
        if op.state != "dns_running" {
            if op.state != "dns_failed" {
                op.state = "dns_pending".into();
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
        if status.updating {
            return;
        }
        let (mut actual, mut expected) = (normalize_dns(Some(&status.applied.policy)).unwrap_or_default(), normalize_dns(op.dns_policy.as_ref()).unwrap_or_default());
        actual.revision = 0;
        expected.revision = 0;
        if status.applied.healthy && status.applied.policy.revision > op.dns_base_revision && config::equal_section(&actual, &expected) {
            op.state = "completed".into();
            op.error.clear();
        } else {
            op.state = "dns_failed".into();
            op.error = if status.error.is_empty() { "DNS restoration did not finish with the expected healthy policy. Review DNS Filter, then retry.".into() } else { status.error.clone() };
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
        if apply_result.is_err() {
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
        let pending = self.engine.get_pending_transaction().map(|tx| json!({"id": tx.id, "state": tx.current_state, "confirmation_deadline": tx.confirmation_deadline, "management_access": tx.config.system.management_access}));
        write_json(
            200,
            &json!({
                "generated_at": Time::now_utc(), "revision": self.engine.get_current_config().revision,
                "last_backup_export_at": last, "backup_export_note": "Server export recorded; file storage and restore testing are not verified.",
                "snapshot_count": snapshots.len(), "retention": {"manual": 20, "automatic": 20}, "pending": pending, "operation": op, "engine": self.engine.get_status(),
            }),
        )
    }

    pub(crate) fn handle_recovery_dns(&self, r: &mut Request<'_>) -> Response {
        let _guard = self.recovery_mu.lock().unwrap_or_else(|e| e.into_inner());
        let id = r.path_value("id");
        let Some(mut op) = self.recovery_operation().ok().flatten().filter(|op| op.id == id) else {
            return Response::error("Restore operation unavailable", 404);
        };
        let engine = self.engine.get_status();
        if self.engine.get_pending_transaction().is_some() || engine.recovery_required || engine.applying || (op.state != "dns_pending" && op.state != "dns_failed") {
            return Response::error("Confirm the restored network configuration before continuing DNS restoration", 409);
        }
        let Some(filter) = self.dns_filter() else { return Response::error("DNS protection unavailable", 503) };
        let Ok(status) = filter.status(Instant::now() + std::time::Duration::from_secs(30)) else { return Response::error("DNS status unavailable", 503) };
        if status.updating {
            return Response::error("Wait for the current DNS operation to finish", 409);
        }
        op.dns_base_revision = status.applied.policy.revision;
        op.state = "dns_running".into();
        op.error.clear();
        op.updated_at = Time::now_utc();
        let Some(store) = self.engine_store() else { return Response::error("Cannot persist DNS continuation", 503) };
        if store.save_recovery_operation(&op).is_err() {
            return Response::error("Cannot persist DNS continuation", 503);
        }
        let mut policy = normalize_dns(op.dns_policy.as_ref()).unwrap_or_default();
        policy.revision = status.applied.policy.revision;
        if let Err(err) = filter.start(&policy, false) {
            op.state = "dns_failed".into();
            op.error = err.to_string();
            let _ = store.save_recovery_operation(&op);
            return Response::error(&err.to_string(), 409);
        }
        self.append_audit("recovery.dns_requested", &audit_actor(&r.remote_addr), details(&[("operation_id", &op.id)]));
        write_json(202, &op)
    }

    pub(crate) fn handle_recovery_dismiss(&self, r: &mut Request<'_>) -> Response {
        let _guard = self.recovery_mu.lock().unwrap_or_else(|e| e.into_inner());
        let id = r.path_value("id");
        let Some(mut op) = self.recovery_operation().ok().flatten().filter(|op| op.id == id) else {
            return Response::error("Restore operation unavailable", 404);
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
            Ok(cfg) => write_json(200, &json!({"snapshot_id": id, "source": "snapshot", "base_revision": cfg.revision, "candidate": redact_config(&cfg), "assessment": self.assess_recovery(&cfg, &r.remote_addr)})),
            Err(err) => snapshot_read_error(&err),
        }
    }
}
