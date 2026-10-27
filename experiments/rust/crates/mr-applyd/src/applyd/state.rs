//! Durable helper state: last-good configuration, pending confirmation and the
//! single-transaction idempotency journal.

use regex::Regex;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::sync::LazyLock;

use super::sys::{atomic_write, clear_file_durably, is_not_found};
use crate::apply::{ApplyRequest, ApplyResponse};
use crate::config::SystemConfig;
use crate::errorf;
use crate::goish::errors::{wrapf, Result};
use crate::goish::json;
use crate::goish::time::Time;

pub(crate) const LAST_GOOD_PATH: &str = "/var/lib/minimalrouter-applyd/last-good.json";
pub(crate) const LAST_TX_PATH: &str = "/var/lib/minimalrouter-applyd/last-transaction.json";
pub(crate) const PENDING_PATH: &str = "/var/lib/minimalrouter-applyd/pending-confirmation.json";

pub(crate) static TRANSACTION_ID_PATTERN: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$").unwrap());
pub(crate) static INTERFACE_NAME_PATTERN: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,14}$").unwrap());

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub(crate) struct TransactionRecord {
    pub id: String,
    pub config_hash: String,
    pub response: ApplyResponse,
    pub started_at: Time,
    pub completed_at: Time,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub(crate) struct PendingConfirmation {
    pub config_hash: String,
    pub config: SystemConfig,
}

pub(crate) fn failure(id: &str, message: &str, rolled_back: bool) -> ApplyResponse {
    ApplyResponse { id: id.into(), success: false, verified: false, rolled_back, error: message.into(), ..Default::default() }
}

pub(crate) fn recovery_failure(id: &str, message: &str) -> ApplyResponse {
    ApplyResponse { id: id.into(), success: false, verified: false, recovery_required: true, error: message.into(), ..Default::default() }
}

/// Losing the durable idempotency result always blocks further mutation.
pub(crate) fn journal_persistence_failure(id: &str, previous: &ApplyResponse) -> ApplyResponse {
    let message = if previous.rolled_back && !previous.recovery_required {
        "transaction result could not be persisted; previous runtime was restored but durable idempotency is unavailable and canonical reconciliation is required"
    } else {
        "transaction result could not be persisted; durable idempotency is unavailable and canonical reconciliation is required"
    };
    recovery_failure(id, message)
}

pub(crate) fn persist_transaction_outcome(
    mut record: TransactionRecord,
    save: impl FnOnce(&TransactionRecord) -> Result<()>,
) -> (TransactionRecord, ApplyResponse) {
    let mut response = record.response.clone();
    if save(&record).is_err() {
        response = journal_persistence_failure(&record.id, &response);
        record.response = response.clone();
    }
    (record, response)
}

/// Decides whether a request is answered from the journal instead of run.
pub(crate) fn replay_transaction_response(
    id: &str,
    config_hash: &str,
    previous: Option<&TransactionRecord>,
    load_err: Option<&crate::goish::errors::Error>,
    canonical_reconcile: bool,
) -> Option<ApplyResponse> {
    if let Some(err) = load_err {
        if is_not_found(err) || canonical_reconcile {
            return None;
        }
        return Some(recovery_failure(id, "transaction journal could not be read; canonical reconciliation is required"));
    }
    let Some(previous) = previous else {
        if canonical_reconcile {
            return None;
        }
        return Some(recovery_failure(id, "transaction journal returned no record; canonical reconciliation is required"));
    };
    if previous.id != id {
        let unresolved = previous.completed_at.is_zero() || previous.response.recovery_required;
        if unresolved && !canonical_reconcile {
            return Some(recovery_failure(id, "a previous privileged transaction remains unresolved; canonical reconciliation is required"));
        }
        return None;
    }
    if previous.config_hash != config_hash {
        return Some(failure(id, "transaction ID was already used for different content", false));
    }
    if previous.completed_at.is_zero() {
        return Some(recovery_failure(id, "privileged transaction outcome is incomplete; canonical reconciliation is required"));
    }
    Some(previous.response.clone())
}

pub(crate) fn pending_load_failure(id: &str, err: &crate::goish::errors::Error) -> ApplyResponse {
    if is_not_found(err) {
        return failure(id, "no configuration is awaiting confirmation", false);
    }
    recovery_failure(id, "pending confirmation state could not be read; canonical reconciliation is required")
}

fn sha256_json<T: Serialize>(value: &T) -> Result<String> {
    Ok(hex::encode(Sha256::digest(json::marshal(value)?)))
}

pub(crate) fn hash_request(req: &ApplyRequest) -> Result<String> {
    sha256_json(req)
}

pub(crate) fn hash_config(cfg: &SystemConfig) -> Result<String> {
    sha256_json(cfg)
}

/// Reads and parses last-good without judging it against current rules: what
/// the appliance was verified to run is a separate question from whether a
/// newer release still accepts every value in it.
pub(crate) fn load_last_good_raw() -> Result<SystemConfig> {
    let data = std::fs::read(LAST_GOOD_PATH)?;
    let mut cfg: SystemConfig = json::unmarshal(&data)?;
    cfg.migrate_legacy_fields();
    Ok(cfg)
}

pub(crate) fn load_last_good() -> Result<SystemConfig> {
    let cfg = load_last_good_raw()?;
    cfg.validate()?;
    Ok(cfg)
}

/// None means first run (no last-good file); errors are anything else.
pub(crate) fn normalize_last_good(loaded: Result<SystemConfig>) -> Result<Option<SystemConfig>> {
    match loaded {
        Ok(cfg) => Ok(Some(cfg)),
        Err(err) if is_not_found(&err) => Ok(None),
        Err(err) => Err(wrapf("read last-good configuration", err)),
    }
}

pub(crate) fn save_last_good(cfg: &SystemConfig) -> Result<()> {
    cfg.validate().map_err(|e| wrapf("invalid last-good configuration", e))?;
    atomic_write(LAST_GOOD_PATH, &json::marshal(cfg)?, 0o600)
}

pub(crate) fn validate_transaction_record(record: &TransactionRecord) -> Result<()> {
    if !TRANSACTION_ID_PATTERN.is_match(&record.id) {
        return Err(errorf!("transaction record has an invalid ID"));
    }
    if hex::decode(&record.config_hash).map(|d| d.len() != 32).unwrap_or(true) {
        return Err(errorf!("transaction record has an invalid configuration fingerprint"));
    }
    if record.started_at.is_zero() {
        return Err(errorf!("transaction record has no start time"));
    }
    if record.completed_at.is_zero() {
        let r = &record.response;
        if !r.id.is_empty() || r.success || r.verified || r.rolled_back || r.recovery_required || !r.error.is_empty() {
            return Err(errorf!("incomplete transaction record contains a final response"));
        }
        return Ok(());
    }
    if record.completed_at.before(&record.started_at) {
        return Err(errorf!("transaction record completion precedes start"));
    }
    if record.response.id != record.id {
        return Err(errorf!("transaction record response ID does not match"));
    }
    record.response.validate().map_err(|e| wrapf("transaction record response is invalid", e))
}

pub(crate) fn load_last_transaction() -> Result<TransactionRecord> {
    let data = std::fs::read(LAST_TX_PATH)?;
    let record: TransactionRecord = json::unmarshal(&data)?;
    validate_transaction_record(&record)?;
    Ok(record)
}

pub(crate) fn save_last_transaction(record: &TransactionRecord) -> Result<()> {
    validate_transaction_record(record)?;
    atomic_write(LAST_TX_PATH, &json::marshal(record)?, 0o600)
}

fn validate_pending_confirmation(pending: &PendingConfirmation) -> Result<()> {
    pending.config.validate().map_err(|e| wrapf("pending configuration is invalid", e))?;
    let expected = hash_config(&pending.config)?;
    if pending.config_hash.is_empty() || pending.config_hash != expected {
        return Err(errorf!("pending configuration fingerprint does not match"));
    }
    Ok(())
}

pub(crate) fn save_pending_confirmation(pending: &PendingConfirmation) -> Result<()> {
    validate_pending_confirmation(pending)?;
    atomic_write(PENDING_PATH, &json::marshal(pending)?, 0o600)
}

pub(crate) fn load_pending_confirmation() -> Result<PendingConfirmation> {
    let data = std::fs::read(PENDING_PATH)?;
    let pending: PendingConfirmation = json::unmarshal(&data)?;
    validate_pending_confirmation(&pending)?;
    Ok(pending)
}

pub(crate) fn pending_confirmation_exists() -> Result<bool> {
    match std::fs::metadata(PENDING_PATH) {
        Ok(_) => Ok(true),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(false),
        Err(e) => Err(e.into()),
    }
}

/// Clearing the journal is an acknowledgement, so its removal is persisted
/// before success is reported.
pub(crate) fn clear_pending_confirmation() -> Result<()> {
    clear_file_durably(PENDING_PATH).map_err(Into::into)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn record(id: &str, completed: bool, recovery: bool) -> TransactionRecord {
        TransactionRecord {
            id: id.into(),
            config_hash: "a".repeat(64),
            response: if completed {
                ApplyResponse { id: id.into(), success: !recovery, verified: !recovery, recovery_required: recovery, ..Default::default() }
            } else {
                ApplyResponse::default()
            },
            started_at: Time::now(),
            completed_at: if completed { Time::now() } else { Time::zero() },
        }
    }

    #[test]
    fn replay_rules_match_go() {
        let hash = "a".repeat(64);
        let done = record("tx-1", true, false);
        assert!(replay_transaction_response("tx-1", &hash, Some(&done), None, false).unwrap().success);
        assert_eq!(
            replay_transaction_response("tx-1", "other", Some(&done), None, false).unwrap().error,
            "transaction ID was already used for different content"
        );
        assert!(replay_transaction_response("tx-2", &hash, Some(&done), None, false).is_none());
        let open = record("tx-1", false, false);
        assert!(replay_transaction_response("tx-2", &hash, Some(&open), None, false).unwrap().recovery_required);
        assert!(replay_transaction_response("tx-2", &hash, Some(&open), None, true).is_none());
        let missing = crate::goish::errors::Error::new(std::io::Error::from(std::io::ErrorKind::NotFound));
        assert!(replay_transaction_response("tx-2", &hash, None, Some(&missing), false).is_none());
        validate_transaction_record(&done).unwrap();
        validate_transaction_record(&open).unwrap();
        assert!(validate_transaction_record(&record("bad id!", true, false)).is_err());
    }
}
