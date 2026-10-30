use rusqlite::{OptionalExtension, params};
use serde::Serialize;
use serde::de::DeserializeOwned;
use sha2::{Digest, Sha256};

use super::model::SystemConfig;
use super::store::{
    PRUNE_AUDIT_SQL, PRUNE_AUTOMATIC_SNAPSHOTS_SQL, PRUNE_CONFIG_REVISIONS_SQL, PRUNE_HIGH_VOLUME_AUDIT_SQL, PRUNE_MANUAL_SNAPSHOTS_SQL, SQLiteStore, Snapshot,
    sql_err,
};
use crate::errorf;
use crate::goish::errors::{Result, wrapf};
use crate::goish::json;
use crate::goish::time::Time;
use crate::storage;

pub fn validate_snapshot_label(label: &str) -> Result<()> {
    if label.chars().count() > 80 {
        return Err(errorf!("snapshot name must be at most 80 characters"));
    }
    if label.chars().any(char::is_control) {
        return Err(errorf!("snapshot name cannot contain control characters"));
    }
    Ok(())
}

impl SQLiteStore {
    pub fn last_backup_export(&self) -> Result<Option<Time>> {
        let value: Option<String> =
            self.lock().query_row("SELECT exported_at FROM backup_export_status WHERE id=1", [], |r| r.get(0)).optional().map_err(sql_err)?;
        match value {
            None => Ok(None),
            Some(text) => Time::parse_sqlite(&text).map(Some).ok_or_else(|| errorf!("unparseable export timestamp")),
        }
    }

    /// Recovery state contains operation metadata and a DNS category policy
    /// only. Never store candidate configurations, tokens, credentials or sessions.
    pub fn save_recovery_operation<T: Serialize>(&self, value: &T) -> Result<()> {
        let data = json::marshal_string(value)?;
        if data.len() > 128 << 10 {
            return Err(errorf!("recovery operation too large"));
        }
        self.lock()
            .execute("INSERT INTO recovery_state(key,value) VALUES('operation',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", params![data])
            .map_err(sql_err)?;
        Ok(())
    }

    /// Reads the stored recovery operation; `Ok(None)` when none exists.
    pub fn read_recovery_operation<T: DeserializeOwned>(&self) -> Result<Option<T>> {
        let data: Option<String> =
            self.lock().query_row("SELECT value FROM recovery_state WHERE key='operation'", [], |r| r.get(0)).optional().map_err(sql_err)?;
        match data {
            None => Ok(None),
            Some(text) => Ok(Some(json::unmarshal(text.as_bytes())?)),
        }
    }

    pub fn storage_status(&self) -> storage::Status {
        let conn = self.lock();
        let Ok(mut stmt) = conn.prepare("PRAGMA database_list") else {
            return storage::evaluate(0, 0);
        };
        let Ok(rows) = stmt.query_map([], |r| Ok((r.get::<_, String>(1)?, r.get::<_, String>(2)?))) else {
            return storage::evaluate(0, 0);
        };
        for row in rows {
            let Ok((name, path)) = row else {
                return storage::evaluate(0, 0);
            };
            if name == "main" && !path.is_empty() {
                let dir = std::path::Path::new(&path).parent().map(|p| p.to_string_lossy().into_owned()).unwrap_or_default();
                return storage::inspect(&dir);
            }
        }
        storage::evaluate(0, 0)
    }

    /// Reapplies all bounded-retention guarantees and checkpoints the WAL
    /// without vacuuming or taking the router offline.
    pub fn maintain_storage(&self) -> Result<()> {
        let mut conn = self.lock();
        let tx = conn.transaction().map_err(|e| wrapf("begin storage maintenance", e))?;
        for statement in [
            PRUNE_CONFIG_REVISIONS_SQL,
            PRUNE_AUTOMATIC_SNAPSHOTS_SQL.as_str(),
            PRUNE_MANUAL_SNAPSHOTS_SQL.as_str(),
            PRUNE_HIGH_VOLUME_AUDIT_SQL.as_str(),
            PRUNE_AUDIT_SQL.as_str(),
        ] {
            tx.execute_batch(statement).map_err(|e| wrapf("prune bounded local state", e))?;
        }
        tx.commit().map_err(|e| wrapf("commit storage maintenance", e))?;
        conn.query_row("PRAGMA wal_checkpoint(PASSIVE)", [], |_| Ok(())).optional().map_err(|e| wrapf("checkpoint SQLite WAL", e))?;
        Ok(())
    }

    /// Changes administrator credentials and revokes all sessions in one
    /// transaction. A failure leaves the old credentials and sessions intact.
    pub fn recovery_reset_authentication(&self, password_hash: &str, disable_totp: bool) -> Result<()> {
        let mut conn = self.lock();
        let tx = conn.transaction().map_err(|e| wrapf("begin authentication recovery", e))?;
        let now = Time::now_utc().rfc3339();
        store_recovered_credentials(&tx, password_hash, disable_totp, &now).map_err(|e| wrapf("store recovered administrator credentials", e))?;
        tx.execute("DELETE FROM sessions", []).map_err(|e| wrapf("revoke sessions during authentication recovery", e))?;
        tx.commit().map_err(|e| wrapf("commit authentication recovery", e))?;
        Ok(())
    }

    /// Atomically creates an undo snapshot, stores one validated revision,
    /// optionally resets administrator credentials/TOTP, and revokes all
    /// sessions. Used only by the local recovery console.
    pub fn recovery_save_config(&self, current: &SystemConfig, next: &SystemConfig, password_hash: Option<&str>, clear_totp: bool) -> Result<Snapshot> {
        current.validate().map_err(|e| wrapf("current recovery configuration is invalid", e))?;
        next.validate().map_err(|e| wrapf("recovery configuration is invalid", e))?;
        if next.revision != current.revision.wrapping_add(1) {
            return Err(errorf!("recovery configuration revision must advance exactly once"));
        }
        let current_data = json::marshal_string(current).map_err(|e| wrapf("marshal recovery snapshot", e))?;
        let mut next = next.clone();
        next.updated_at = Time::now_utc();
        let next_data = json::marshal_string(&next).map_err(|e| wrapf("marshal recovered configuration", e))?;
        let snapshot = Snapshot {
            id: format!("snap-{}", Time::now().unix_nano()),
            revision: current.revision,
            created_at: Time::now_utc().rfc3339(),
            checksum: hex::encode(Sha256::digest(current_data.as_bytes())),
            kind: String::new(),
            label: String::new(),
            config_json: current_data,
        };

        let mut conn = self.lock();
        let tx = conn.transaction().map_err(|e| wrapf("begin recovery transaction", e))?;
        let stored_json: String = tx
            .query_row("SELECT config_json FROM config_revisions ORDER BY revision DESC LIMIT 1", [], |r| r.get(0))
            .map_err(|e| wrapf("inspect current recovery revision", sql_err(e)))?;
        let unchanged = json::unmarshal::<SystemConfig>(stored_json.as_bytes()).map(|stored| stored.revision == current.revision).unwrap_or(false);
        if !unchanged {
            return Err(errorf!("canonical configuration changed during recovery"));
        }
        tx.execute(
            "INSERT INTO snapshots (id, revision, created_at, checksum, config_json) VALUES (?, ?, ?, ?, ?)",
            params![snapshot.id, snapshot.revision as i64, snapshot.created_at, snapshot.checksum, snapshot.config_json],
        )
        .map_err(|e| wrapf("insert recovery snapshot", e))?;
        tx.execute_batch(&PRUNE_AUTOMATIC_SNAPSHOTS_SQL).map_err(|e| wrapf("prune recovery snapshots", e))?;
        tx.execute("INSERT INTO config_revisions (updated_at, config_json) VALUES (?, ?)", params![next.updated_at.rfc3339(), next_data])
            .map_err(|e| wrapf("insert recovered configuration", e))?;
        tx.execute_batch(PRUNE_CONFIG_REVISIONS_SQL).map_err(|e| wrapf("prune recovered configuration revisions", e))?;
        if let Some(hash) = password_hash {
            let now = Time::now_utc().rfc3339();
            store_recovered_credentials(&tx, hash, clear_totp, &now).map_err(|e| wrapf("store factory-reset administrator credentials", e))?;
        }
        tx.execute("DELETE FROM sessions", []).map_err(|e| wrapf("revoke sessions during recovery", e))?;
        tx.commit().map_err(|e| wrapf("commit recovery transaction", e))?;
        Ok(snapshot)
    }
}

fn store_recovered_credentials(tx: &rusqlite::Transaction<'_>, hash: &str, clear_totp: bool, now: &str) -> rusqlite::Result<usize> {
    if clear_totp {
        tx.execute(
            "
			INSERT INTO admin_credentials (id, password_hash, totp_secret, auth_generation, updated_at)
			VALUES (1, ?, NULL, 1, ?)
			ON CONFLICT(id) DO UPDATE SET
				password_hash = excluded.password_hash,
				totp_secret = NULL,
				auth_generation = admin_credentials.auth_generation + 1,
				updated_at = excluded.updated_at",
            params![hash, now],
        )
    } else {
        tx.execute(
            "
			INSERT INTO admin_credentials (id, password_hash, auth_generation, updated_at)
			VALUES (1, ?, 1, ?)
			ON CONFLICT(id) DO UPDATE SET
				password_hash = excluded.password_hash,
				auth_generation = admin_credentials.auth_generation + 1,
				updated_at = excluded.updated_at",
            params![hash, now],
        )
    }
}
