//! Canonical SQLite persistence and snapshot management.
//!
//! The schema, SQL statements and stored text formats match the Go store, so a
//! database written by either implementation can be opened by the other.

use rusqlite::{Connection, OptionalExtension, Transaction, params};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
use std::path::Path;
use std::sync::{LazyLock, Mutex, MutexGuard};

use super::model::{Revision, SystemConfig, default_config};
use crate::goish::errors::{self, Result, wrapf};
use crate::goish::json;
use crate::goish::time::{Duration, Time};
use crate::{errorf, sentinel};

sentinel!(ErrSnapshotNotFound, "snapshot not found");
sentinel!(ErrSnapshotCorrupt, "snapshot integrity check failed");
sentinel!(
    /// `sql.ErrNoRows`: callers distinguish "nothing stored yet" from failures.
    ErrNoRows,
    "sql: no rows in result set"
);

/// Automatic restore points (pre-apply, recovery undo) and operator-created
/// ones are retained in separate pools.
pub const SNAPSHOT_KIND_AUTOMATIC: &str = "automatic";
pub const SNAPSHOT_KIND_MANUAL: &str = "manual";

const SNAPSHOT_RETENTION_PER_KIND: usize = 20;
pub(crate) const AUDIT_RETENTION: usize = 5000;
/// Bounds the share of the audit log that request rejections may occupy.
const HIGH_VOLUME_AUDIT_RETENTION: usize = 1000;

/// Recorded for requests rejected before authentication or before any
/// privileged work, so their rate is chosen by whoever sends the requests.
pub const HIGH_VOLUME_AUDIT_EVENT_TYPES: [&str; 7] = [
    "access.trusted_network_rejected",
    "auth.unauthorized",
    "auth.login_rate_limited",
    "auth.cross_site_rejected",
    "auth.origin_rejected",
    "auth.csrf_rejected",
    "auth.read_only_rejected",
];

pub fn is_high_volume_audit_event(event_type: &str) -> bool {
    HIGH_VOLUME_AUDIT_EVENT_TYPES.contains(&event_type)
}

/// Renders fixed, compile-time identifiers as an SQL string list. Never use
/// it for caller-supplied values.
fn quoted_sql_list(values: &[&str]) -> String {
    values.iter().map(|v| format!("'{}'", v.replace('\'', "''"))).collect::<Vec<_>>().join(", ")
}

// Snapshots are ordered by the revision they captured, which only grows, and
// only then by wall-clock time: after NTP steps a wrong RTC back, created_at
// alone would put stale snapshots ahead of the real newest ones.
pub(crate) static PRUNE_AUTOMATIC_SNAPSHOTS_SQL: LazyLock<String> = LazyLock::new(|| {
    format!(
        "\n\t\tDELETE FROM snapshots\n\t\tWHERE kind <> '{0}' AND id NOT IN (\n\t\t\tSELECT id FROM snapshots WHERE kind <> '{0}' ORDER BY revision DESC, created_at DESC, id DESC LIMIT {1}\n\t\t)",
        SNAPSHOT_KIND_MANUAL, SNAPSHOT_RETENTION_PER_KIND
    )
});
pub(crate) static PRUNE_MANUAL_SNAPSHOTS_SQL: LazyLock<String> = LazyLock::new(|| {
    format!(
        "\n\t\tDELETE FROM snapshots\n\t\tWHERE kind = '{0}' AND id NOT IN (\n\t\t\tSELECT id FROM snapshots WHERE kind = '{0}' ORDER BY revision DESC, created_at DESC, id DESC LIMIT {1}\n\t\t)",
        SNAPSHOT_KIND_MANUAL, SNAPSHOT_RETENTION_PER_KIND
    )
});
pub(crate) static PRUNE_AUDIT_SQL: LazyLock<String> = LazyLock::new(|| {
    format!(
        "\n\t\tDELETE FROM audit_events\n\t\tWHERE id NOT IN (\n\t\t\tSELECT id FROM audit_events ORDER BY timestamp DESC, id DESC LIMIT {}\n\t\t)",
        AUDIT_RETENTION
    )
});
pub(crate) static PRUNE_HIGH_VOLUME_AUDIT_SQL: LazyLock<String> = LazyLock::new(|| {
    format!(
        "\n\t\tDELETE FROM audit_events\n\t\tWHERE event_type IN ({0}) AND id NOT IN (\n\t\t\tSELECT id FROM audit_events WHERE event_type IN ({0}) ORDER BY timestamp DESC, id DESC LIMIT {1}\n\t\t)",
        quoted_sql_list(&HIGH_VOLUME_AUDIT_EVENT_TYPES),
        HIGH_VOLUME_AUDIT_RETENTION
    )
});
pub(crate) const PRUNE_CONFIG_REVISIONS_SQL: &str =
    "DELETE FROM config_revisions WHERE revision NOT IN (SELECT revision FROM config_revisions ORDER BY revision DESC LIMIT 100)";

fn prune_snapshots_sql(kind: &str) -> &'static str {
    if kind == SNAPSHOT_KIND_MANUAL { &PRUNE_MANUAL_SNAPSHOTS_SQL } else { &PRUNE_AUTOMATIC_SNAPSHOTS_SQL }
}

/// An immutable, checksummed point-in-time configuration state.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Snapshot {
    pub id: String,
    pub revision: Revision,
    pub created_at: String,
    pub checksum: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub kind: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub label: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub config_json: String,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AuditEvent {
    #[serde(skip_serializing_if = "String::is_empty")]
    pub category: String,
    pub id: String,
    pub event_type: String,
    pub actor: String,
    pub timestamp: Time,
    pub details: BTreeMap<String, String>,
}

/// Canonical SQLite persistence. One connection behind a mutex replaces Go's
/// `sync.RWMutex` plus a four-connection pool: SQLite serialises writers
/// anyway, and the appliance never needs concurrent readers for throughput.
pub struct SQLiteStore {
    pub(crate) conn: Mutex<Connection>,
}

pub type FileStore = SQLiteStore;

pub(crate) fn sql_err(err: rusqlite::Error) -> errors::Error {
    match err {
        rusqlite::Error::QueryReturnedNoRows => errors::Error::new(ErrNoRows),
        other => errors::Error::new(other),
    }
}

fn rfc3339_now_utc() -> String {
    Time::now_utc().rfc3339()
}

impl SQLiteStore {
    pub fn new_file_store(dir_path: &str) -> Result<SQLiteStore> {
        Self::new(dir_path)
    }

    /// Initialises canonical persistence: creates the database, runs schema
    /// migrations and stores the default configuration on first use.
    pub fn new(dir_path: &str) -> Result<SQLiteStore> {
        std::fs::create_dir_all(dir_path).map_err(|e| wrapf("failed to create private data directory", e))?;
        set_mode(dir_path, 0o700).map_err(|e| wrapf("failed to secure data directory", e))?;
        let db_path = Path::new(dir_path).join("minimalrouter.db");
        let conn = Connection::open(&db_path).map_err(|e| wrapf(format!("failed to open SQLite database at {}", db_path.display()), e))?;
        // The same security and durability policy the Go DSN applies.
        conn.execute_batch(
            "PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON; PRAGMA trusted_schema=OFF; \
             PRAGMA secure_delete=ON; PRAGMA busy_timeout=5000; PRAGMA cache_size=-2000;",
        )
        .map_err(|e| wrapf("failed to initialize SQLite connection", e))?;
        set_mode(&db_path.to_string_lossy(), 0o600).map_err(|e| wrapf("failed to secure SQLite database", e))?;
        let integrity: rusqlite::Result<String> = conn.query_row("PRAGMA quick_check", [], |r| r.get(0));
        if integrity.as_deref() != Ok("ok") {
            return Err(errorf!("SQLite integrity check failed"));
        }
        run_migrations(&conn).map_err(|e| wrapf("failed to run migrations", e))?;

        let store = SQLiteStore { conn: Mutex::new(conn) };
        let count: i64 =
            store.lock().query_row("SELECT COUNT(*) FROM config_revisions", [], |r| r.get(0)).map_err(|e| wrapf("failed to query config_revisions", e))?;
        if count == 0 {
            store.save_config(default_config()).map_err(|e| wrapf("failed to initialize default config", e))?;
        }
        Ok(store)
    }

    pub(crate) fn lock(&self) -> MutexGuard<'_, Connection> {
        self.conn.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// Reads the most recent canonical configuration.
    pub fn get_latest_config(&self) -> Result<SystemConfig> {
        let config_json: String = self
            .lock()
            .query_row("SELECT config_json FROM config_revisions ORDER BY revision DESC LIMIT 1", [], |r| r.get(0))
            .map_err(|e| wrapf("failed to read latest config", sql_err(e)))?;
        let mut cfg: SystemConfig = json::unmarshal(config_json.as_bytes()).map_err(|e| wrapf("failed to parse config JSON", e))?;
        cfg.migrate_legacy_fields();
        cfg.validate().map_err(|e| wrapf("stored configuration failed validation", e))?;
        Ok(cfg)
    }

    /// Saves an updated configuration revision atomically.
    pub fn save_config(&self, mut cfg: SystemConfig) -> Result<()> {
        let conn = self.lock();
        cfg.validate().map_err(|e| wrapf("refusing to persist invalid configuration", e))?;
        let current: Option<String> = conn
            .query_row("SELECT config_json FROM config_revisions ORDER BY revision DESC LIMIT 1", [], |r| r.get(0))
            .optional()
            .map_err(|e| wrapf("failed to inspect current revision", e))?;
        match current {
            Some(text) => {
                let advances =
                    json::unmarshal::<SystemConfig>(text.as_bytes()).map(|current| cfg.revision == current.revision.wrapping_add(1)).unwrap_or(false);
                if !advances {
                    return Err(errorf!("configuration revision must advance exactly once"));
                }
            }
            None if cfg.revision != 1 => return Err(errorf!("initial configuration revision must be 1")),
            None => {}
        }

        cfg.updated_at = Time::now();
        let data = json::marshal_string(&cfg).map_err(|e| wrapf("failed to serialize config", e))?;
        let mut conn = conn;
        let tx = conn.transaction().map_err(|e| wrapf("failed to begin transaction", e))?;
        tx.execute("INSERT INTO config_revisions (updated_at, config_json) VALUES (?, ?)", params![cfg.updated_at.rfc3339(), data])
            .map_err(|e| wrapf("failed to insert config revision", e))?;
        tx.execute_batch(
            "\n\t\tDELETE FROM config_revisions\n\t\tWHERE revision NOT IN (\n\t\t\tSELECT revision FROM config_revisions ORDER BY revision DESC LIMIT 100\n\t\t)\n\t",
        )
        .map_err(|e| wrapf("failed to prune config revisions", e))?;
        tx.commit().map_err(|e| wrapf("failed to commit config transaction", e))?;
        Ok(())
    }

    /// Creates an immutable checksummed automatic restore point.
    pub fn create_snapshot(&self, cfg: &SystemConfig) -> Result<Snapshot> {
        self.create_snapshot_kind(cfg, SNAPSHOT_KIND_AUTOMATIC, "")
    }

    /// Stores an operator-requested restore point, retained independently of
    /// the automatic pre-apply snapshots.
    pub fn create_manual_snapshot(&self, cfg: &SystemConfig, label: &str) -> Result<Snapshot> {
        let name = label.trim();
        super::recovery_status::validate_snapshot_label(name)?;
        self.create_snapshot_kind(cfg, SNAPSHOT_KIND_MANUAL, name)
    }

    fn create_snapshot_kind(&self, cfg: &SystemConfig, kind: &str, label: &str) -> Result<Snapshot> {
        let mut conn = self.lock();
        let data = json::marshal_string(cfg).map_err(|e| wrapf("failed to marshal config for snapshot", e))?;
        let checksum = hex::encode(Sha256::digest(data.as_bytes()));
        let entropy = crate::goish::rand::bytes(4).map_err(|e| wrapf("generate snapshot ID", errorf!("{}", e)))?;
        let id = format!("snap-{}-{}", Time::now().unix_nano(), hex::encode(&entropy));
        // UTC keeps created_at ordering consistent with recovery snapshots,
        // which SQLite and the recovery console compare as text.
        let created_at = rfc3339_now_utc();

        let tx = conn.transaction().map_err(|e| wrapf("failed to begin snapshot transaction", e))?;
        tx.execute(
            "INSERT INTO snapshots (id, revision, created_at, checksum, config_json, kind, label) VALUES (?, ?, ?, ?, ?, ?, ?)",
            params![id, cfg.revision as i64, created_at, checksum, data, kind, label],
        )
        .map_err(|e| wrapf("failed to insert snapshot", e))?;
        tx.execute_batch(prune_snapshots_sql(kind)).map_err(|e| wrapf("failed to prune snapshots", e))?;
        tx.commit().map_err(|e| wrapf("failed to commit snapshot", e))?;

        Ok(Snapshot { id, revision: cfg.revision, created_at, checksum, kind: kind.into(), label: label.into(), config_json: String::new() })
    }

    /// All stored snapshots, newest first. An unreadable row fails the listing.
    pub fn list_snapshots(&self) -> Result<Vec<Snapshot>> {
        let conn = self.lock();
        let mut stmt = conn
            .prepare("SELECT id, revision, created_at, checksum, kind, label FROM snapshots ORDER BY revision DESC, created_at DESC, id DESC")
            .map_err(|e| wrapf("failed to query snapshots", e))?;
        let rows = stmt
            .query_map([], |r| {
                Ok(Snapshot {
                    id: r.get(0)?,
                    revision: r.get::<_, i64>(1)? as Revision,
                    created_at: r.get(2)?,
                    checksum: r.get(3)?,
                    kind: r.get(4)?,
                    label: r.get(5)?,
                    config_json: String::new(),
                })
            })
            .map_err(|e| wrapf("failed to query snapshots", e))?;
        let mut snapshots = Vec::new();
        for row in rows {
            snapshots.push(row.map_err(|e| wrapf("failed to read snapshot", e))?);
        }
        Ok(snapshots)
    }

    /// Removes a single restore point.
    pub fn delete_snapshot(&self, id: &str) -> Result<()> {
        let deleted = self.lock().execute("DELETE FROM snapshots WHERE id = ?", params![id]).map_err(|e| wrapf("failed to delete snapshot", e))?;
        if deleted == 0 {
            return Err(errors::Error::new(ErrSnapshotNotFound));
        }
        Ok(())
    }

    /// A specific snapshot including its full config JSON, integrity-checked.
    pub fn get_snapshot(&self, id: &str) -> Result<Snapshot> {
        let row = self.lock().query_row("SELECT id, revision, created_at, checksum, kind, label, config_json FROM snapshots WHERE id = ?", params![id], |r| {
            Ok(Snapshot {
                id: r.get(0)?,
                revision: r.get::<_, i64>(1)? as Revision,
                created_at: r.get(2)?,
                checksum: r.get(3)?,
                kind: r.get(4)?,
                label: r.get(5)?,
                config_json: r.get(6)?,
            })
        });
        let snap = match row {
            Ok(snap) => snap,
            Err(rusqlite::Error::QueryReturnedNoRows) => return Err(errors::Error::new(ErrSnapshotNotFound)),
            Err(e) => return Err(wrapf("snapshot storage unavailable", e)),
        };
        let actual = Sha256::digest(snap.config_json.as_bytes());
        let valid = hex::decode(&snap.checksum)
            .map(|expected| expected.len() == 32 && subtle::ConstantTimeEq::ct_eq(&actual[..], &expected[..]).into())
            .unwrap_or(false);
        if !valid {
            return Err(errors::Error::new(ErrSnapshotCorrupt));
        }
        Ok(snap)
    }

    /// Stores metadata only. Callers must never include request bodies,
    /// credentials, keys, tokens, or generated configuration in details.
    pub fn append_audit_event(&self, event_type: &str, actor: &str, details: &BTreeMap<String, String>) -> Result<()> {
        if event_type.is_empty() || event_type.len() > 96 || actor.is_empty() || actor.len() > 255 {
            return Err(errorf!("invalid audit event metadata"));
        }
        let details_json = match json::marshal_string(details) {
            Ok(text) if text.len() <= 4096 => text,
            _ => return Err(errorf!("invalid audit event details")),
        };
        let id_bytes = crate::goish::rand::bytes(18).map_err(|e| wrapf("generate audit event ID", errorf!("{}", e)))?;
        let id = format!("audit-{}", base64_raw_url(&id_bytes));

        let mut conn = self.lock();
        let tx = conn.transaction().map_err(|e| wrapf("begin audit event", e))?;
        tx.execute(
            "INSERT INTO audit_events (id, event_type, actor, timestamp, details_json) VALUES (?, ?, ?, ?, ?)",
            params![id, event_type, actor, Time::now_utc().go_string(), details_json],
        )
        .map_err(|e| wrapf("insert audit event", e))?;
        if event_type == "api.mutation"
            && details.get("path").map(String::as_str) == Some("/api/v1/backup/export")
            && details.get("status").is_some_and(|s| s.starts_with('2'))
        {
            tx.execute(
                "INSERT INTO backup_export_status(id,exported_at) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET exported_at=excluded.exported_at",
                params![Time::now_utc().go_string()],
            )
            .map_err(|e| wrapf("record backup export", e))?;
        }
        // Request rejections are pruned within their own pool first, so their
        // volume can never push other events out of the retained window.
        if is_high_volume_audit_event(event_type) {
            tx.execute_batch(&PRUNE_HIGH_VOLUME_AUDIT_SQL).map_err(|e| wrapf("prune high-volume audit events", e))?;
        }
        tx.execute_batch(&PRUNE_AUDIT_SQL).map_err(|e| wrapf("prune audit events", e))?;
        tx.commit().map_err(|e| wrapf("commit audit event", e))?;
        Ok(())
    }

    pub fn list_audit_events(&self, limit: i64) -> Result<Vec<AuditEvent>> {
        let limit = if limit <= 0 { 100 } else { limit.min(500) };
        let conn = self.lock();
        let mut stmt = conn
            .prepare("SELECT id, event_type, actor, timestamp, details_json\n\t\t FROM audit_events ORDER BY timestamp DESC, id DESC LIMIT ?")
            .map_err(|e| wrapf("list audit events", e))?;
        let rows = stmt
            .query_map(params![limit], |r| {
                Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?, r.get::<_, String>(2)?, r.get::<_, String>(3)?, r.get::<_, String>(4)?))
            })
            .map_err(|e| wrapf("list audit events", e))?;
        let mut events = Vec::with_capacity(limit as usize);
        for row in rows {
            let (id, event_type, actor, timestamp, details_json) = row.map_err(|e| wrapf("read audit event", e))?;
            let timestamp = Time::parse_sqlite(&timestamp).ok_or_else(|| wrapf("read audit event", errorf!("unparseable timestamp")))?;
            let details: Option<BTreeMap<String, String>> = json::unmarshal(details_json.as_bytes()).map_err(|e| wrapf("decode audit event details", e))?;
            events.push(AuditEvent { category: String::new(), id, event_type, actor, timestamp, details: details.unwrap_or_default() });
        }
        Ok(events)
    }

    /// Invalidates every authenticated session after credential changes.
    pub fn delete_all_sessions(&self) -> Result<()> {
        self.lock().execute("DELETE FROM sessions", []).map_err(sql_err)?;
        Ok(())
    }

    /// The stored Argon2id admin password hash.
    pub fn get_admin_hash(&self) -> Result<String> {
        self.lock().query_row("SELECT password_hash FROM admin_credentials WHERE id = 1", [], |r| r.get(0)).map_err(sql_err)
    }

    /// The current authentication epoch. Sessions issued under an older epoch
    /// are invalid even if their row remains.
    pub fn get_auth_generation(&self) -> Result<u64> {
        get_auth_generation(&self.lock()).map_err(sql_err)
    }

    /// The password hash and authentication epoch as one consistent snapshot.
    /// A caller verifying a password against this hash must pass this same
    /// generation to [`SQLiteStore::create_session_if_generation_current`].
    pub fn get_admin_auth_state(&self) -> Result<(String, u64)> {
        self.lock()
            .query_row("SELECT password_hash, auth_generation FROM admin_credentials WHERE id = 1", [], |r| Ok((r.get(0)?, r.get::<_, i64>(1)? as u64)))
            .map_err(sql_err)
    }

    /// Atomically changes the credential and advances the auth epoch.
    pub fn set_admin_hash(&self, hash: &str) -> Result<()> {
        self.update_authentication(|tx, next, now| {
            tx.execute(
                "\n\t\t\tINSERT INTO admin_credentials (id, password_hash, auth_generation, updated_at)\n\t\t\tVALUES (1, ?, ?, ?)\n\t\t\tON CONFLICT(id) DO UPDATE SET\n\t\t\t\tpassword_hash = excluded.password_hash,\n\t\t\t\tauth_generation = excluded.auth_generation,\n\t\t\t\tupdated_at = excluded.updated_at",
                params![hash, next as i64, now],
            )?;
            Ok(())
        })
    }

    pub fn get_admin_totp_secret(&self) -> Result<String> {
        let secret: Option<String> = self.lock().query_row("SELECT totp_secret FROM admin_credentials WHERE id = 1", [], |r| r.get(0)).map_err(sql_err)?;
        Ok(secret.unwrap_or_default())
    }

    /// Atomically enables TOTP and advances the auth epoch.
    pub fn set_admin_totp_secret(&self, secret: &str) -> Result<()> {
        self.update_authentication(|tx, next, now| {
            let rows = tx.execute(
                "\n\t\t\tUPDATE admin_credentials\n\t\t\tSET totp_secret = ?, auth_generation = ?, updated_at = ?\n\t\t\tWHERE id = 1",
                params![secret, next as i64, now],
            )?;
            if rows != 1 {
                return Err(rusqlite::Error::QueryReturnedNoRows);
            }
            Ok(())
        })
    }

    /// Atomically disables TOTP and advances the auth epoch.
    pub fn clear_admin_totp_secret(&self) -> Result<()> {
        self.update_authentication(|tx, next, now| {
            let rows = tx.execute(
                "\n\t\t\tUPDATE admin_credentials\n\t\t\tSET totp_secret = NULL, auth_generation = ?, updated_at = ?\n\t\t\tWHERE id = 1",
                params![next as i64, now],
            )?;
            if rows != 1 {
                return Err(rusqlite::Error::QueryReturnedNoRows);
            }
            Ok(())
        })
    }

    fn update_authentication(&self, update: impl FnOnce(&Transaction<'_>, u64, &str) -> rusqlite::Result<()>) -> Result<()> {
        let mut conn = self.lock();
        let tx = conn.transaction().map_err(sql_err)?;
        let generation = match get_auth_generation(&tx) {
            Ok(g) => g,
            Err(rusqlite::Error::QueryReturnedNoRows) => 0,
            Err(e) => return Err(sql_err(e)),
        };
        if generation == u64::MAX {
            return Err(errorf!("authentication generation exhausted"));
        }
        let next = match generation + 1 {
            0 => 1,
            n => n,
        };
        update(&tx, next, &rfc3339_now_utc()).map_err(sql_err)?;
        tx.execute("DELETE FROM sessions", []).map_err(sql_err)?;
        tx.commit().map_err(sql_err)?;
        Ok(())
    }

    /// Atomically stores the first network revision and the administrator
    /// credential, after the runtime configuration has been verified.
    pub fn commit_initial_setup(&self, mut cfg: SystemConfig, password_hash: &str) -> Result<()> {
        cfg.validate().map_err(|e| wrapf("refusing invalid initial configuration", e))?;
        let mut conn = self.lock();
        let tx = conn.transaction().map_err(sql_err)?;
        let existing: i64 = tx.query_row("SELECT COUNT(*) FROM admin_credentials WHERE id = 1", [], |r| r.get(0)).map_err(sql_err)?;
        if existing != 0 {
            return Err(errorf!("administrator is already configured"));
        }
        let current_json: String =
            tx.query_row("SELECT config_json FROM config_revisions ORDER BY revision DESC LIMIT 1", [], |r| r.get(0)).map_err(sql_err)?;
        let current: SystemConfig = json::unmarshal(current_json.as_bytes())?;
        if cfg.revision != current.revision.wrapping_add(1) {
            return Err(errorf!("initial configuration revision must advance exactly once"));
        }
        cfg.updated_at = Time::now_utc();
        let data = json::marshal_string(&cfg)?;
        let stamp = cfg.updated_at.rfc3339();
        tx.execute("INSERT INTO config_revisions (updated_at, config_json) VALUES (?, ?)", params![stamp, data]).map_err(sql_err)?;
        tx.execute(
            "\n\t\tINSERT INTO admin_credentials (id, password_hash, auth_generation, updated_at)\n\t\tVALUES (1, ?, 1, ?)",
            params![password_hash, stamp],
        )
        .map_err(sql_err)?;
        tx.execute("DELETE FROM sessions", []).map_err(sql_err)?;
        tx.commit().map_err(sql_err)?;
        Ok(())
    }

    // === Session persistence ===

    pub fn create_session(&self, session_id: &str, csrf_token: &str, read_only: bool, auth_generation: u64, created_at: Time, last_seen: Time) -> Result<()> {
        self.lock()
            .execute(
                "INSERT INTO sessions (id, csrf_token, read_only, auth_generation, created_at, last_seen) VALUES (?, ?, ?, ?, ?, ?)",
                params![session_id, csrf_token, read_only, auth_generation as i64, created_at.rfc3339(), last_seen.rfc3339()],
            )
            .map_err(sql_err)?;
        Ok(())
    }

    /// Inserts a session only if the authentication epoch is still
    /// `expected_generation` at the moment of insertion, under one transaction.
    pub fn create_session_if_generation_current(
        &self,
        session_id: &str,
        csrf_token: &str,
        read_only: bool,
        expected_generation: u64,
        created_at: Time,
        last_seen: Time,
    ) -> Result<bool> {
        let mut conn = self.lock();
        let tx = conn.transaction().map_err(sql_err)?;
        let current = get_auth_generation(&tx).map_err(sql_err)?;
        if current != expected_generation {
            return Ok(false);
        }
        tx.execute(
            "INSERT INTO sessions (id, csrf_token, read_only, auth_generation, created_at, last_seen) VALUES (?, ?, ?, ?, ?, ?)",
            params![session_id, csrf_token, read_only, expected_generation as i64, created_at.rfc3339(), last_seen.rfc3339()],
        )
        .map_err(sql_err)?;
        tx.commit().map_err(sql_err)?;
        Ok(true)
    }

    /// Returns (csrf_token, read_only, auth_generation, created_at, last_seen).
    pub fn get_session(&self, session_id: &str) -> Result<(String, bool, u64, Time, Time)> {
        let (csrf, read_only, generation, created, last_seen): (String, bool, i64, String, String) = self
            .lock()
            .query_row("SELECT csrf_token, read_only, auth_generation, created_at, last_seen FROM sessions WHERE id = ?", params![session_id], |r| {
                Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?, r.get(4)?))
            })
            .map_err(sql_err)?;
        let created_at = Time::parse_rfc3339(&created).ok_or_else(|| errorf!("parsing time {:?} as RFC3339", created))?;
        let last_seen_at = Time::parse_rfc3339(&last_seen).ok_or_else(|| errorf!("parsing time {:?} as RFC3339", last_seen))?;
        Ok((csrf, read_only, generation as u64, created_at, last_seen_at))
    }

    pub fn update_session_last_seen(&self, session_id: &str, last_seen: Time) -> Result<()> {
        self.lock().execute("UPDATE sessions SET last_seen = ? WHERE id = ?", params![last_seen.rfc3339(), session_id]).map_err(sql_err)?;
        Ok(())
    }

    pub fn delete_session(&self, session_id: &str) -> Result<()> {
        self.lock().execute("DELETE FROM sessions WHERE id = ?", params![session_id]).map_err(sql_err)?;
        Ok(())
    }

    /// Removes expired sessions (called periodically).
    pub fn clean_expired_sessions(&self, idle_timeout: Duration, absolute_timeout: Duration) -> Result<()> {
        let now = Time::now();
        let idle_cutoff = now.add(Duration(-idle_timeout.0)).rfc3339();
        let abs_cutoff = now.add(Duration(-absolute_timeout.0)).rfc3339();
        self.lock().execute("DELETE FROM sessions WHERE last_seen < ? OR created_at < ?", params![idle_cutoff, abs_cutoff]).map_err(sql_err)?;
        Ok(())
    }

    // === Rate limiter persistence ===

    pub fn get_rate_limit_bucket(&self, ip: &str) -> Result<(i64, Time)> {
        let (attempts, window): (i64, String) = self
            .lock()
            .query_row("SELECT attempts, window_start FROM rate_limit_buckets WHERE ip = ?", params![ip], |r| Ok((r.get(0)?, r.get(1)?)))
            .map_err(sql_err)?;
        Ok((attempts, Time::parse_rfc3339(&window).unwrap_or_default()))
    }

    pub fn set_rate_limit_bucket(&self, ip: &str, attempts: i64, window_start: Time) -> Result<()> {
        self.lock()
            .execute(
                "INSERT INTO rate_limit_buckets (ip, attempts, window_start) VALUES (?, ?, ?)\n\t\t ON CONFLICT(ip) DO UPDATE SET attempts = excluded.attempts, window_start = excluded.window_start",
                params![ip, attempts, window_start.rfc3339()],
            )
            .map_err(sql_err)?;
        Ok(())
    }

    pub fn clean_expired_rate_limit_buckets(&self, window: Duration) -> Result<()> {
        let cutoff = Time::now().add(Duration(-window.0)).rfc3339();
        self.lock().execute("DELETE FROM rate_limit_buckets WHERE window_start < ?", params![cutoff]).map_err(sql_err)?;
        Ok(())
    }
}

fn get_auth_generation(conn: &Connection) -> rusqlite::Result<u64> {
    conn.query_row("SELECT auth_generation FROM admin_credentials WHERE id = 1", [], |r| r.get::<_, i64>(0)).map(|g| g as u64)
}

pub(crate) fn base64_raw_url(data: &[u8]) -> String {
    use base64::Engine;
    base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(data)
}

/// Executes the schema creation SQL (idempotent).
fn run_migrations(conn: &Connection) -> rusqlite::Result<()> {
    conn.execute_batch(
        "
	CREATE TABLE IF NOT EXISTS config_revisions (
		revision INTEGER PRIMARY KEY AUTOINCREMENT,
		updated_at DATETIME NOT NULL,
		config_json TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS snapshots (
		id TEXT PRIMARY KEY,
		revision INTEGER NOT NULL,
		created_at DATETIME NOT NULL,
		checksum TEXT NOT NULL,
		config_json TEXT NOT NULL,
		kind TEXT NOT NULL DEFAULT 'automatic'
	);

	CREATE TABLE IF NOT EXISTS admin_credentials (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		password_hash TEXT NOT NULL,
		totp_secret TEXT,
		auth_generation INTEGER NOT NULL DEFAULT 1,
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		csrf_token TEXT NOT NULL,
		read_only INTEGER NOT NULL DEFAULT 0,
		auth_generation INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL,
		last_seen DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS rate_limit_buckets (
		ip TEXT PRIMARY KEY,
		attempts INTEGER NOT NULL DEFAULT 0,
		window_start DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS audit_events (
		id TEXT PRIMARY KEY,
		event_type TEXT NOT NULL,
		actor TEXT NOT NULL,
		timestamp DATETIME NOT NULL,
		details_json TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS recovery_state (key TEXT PRIMARY KEY, value TEXT NOT NULL);
	CREATE TABLE IF NOT EXISTS backup_export_status (id INTEGER PRIMARY KEY CHECK(id=1), exported_at DATETIME NOT NULL);

	-- Every audit append prunes by recency; without these indexes each write
	-- sorted the whole retained log.
	CREATE INDEX IF NOT EXISTS idx_audit_events_time ON audit_events(timestamp, id);
	CREATE INDEX IF NOT EXISTS idx_audit_events_type_time ON audit_events(event_type, timestamp, id);
	",
    )?;

    // Columns introduced after the initial public schema. SQLite lacks ADD
    // COLUMN IF NOT EXISTS, so inspect the table before each migration.
    let automatic_default = format!("kind TEXT NOT NULL DEFAULT '{}'", SNAPSHOT_KIND_AUTOMATIC);
    let migrations: [(&str, &str, &str); 5] = [
        ("sessions", "read_only", "read_only INTEGER NOT NULL DEFAULT 0"),
        ("sessions", "auth_generation", "auth_generation INTEGER NOT NULL DEFAULT 1"),
        ("admin_credentials", "auth_generation", "auth_generation INTEGER NOT NULL DEFAULT 1"),
        ("snapshots", "kind", &automatic_default),
        ("snapshots", "label", "label TEXT NOT NULL DEFAULT ''"),
    ];
    for (table, column, definition) in migrations {
        if !sqlite_column_exists(conn, table, column)? {
            conn.execute_batch(&format!("ALTER TABLE {} ADD COLUMN {}", table, definition))?;
        }
    }
    // Seed older appliances from the complete retained history.
    conn.execute_batch(
        "INSERT OR IGNORE INTO backup_export_status(id, exported_at)
		SELECT 1, timestamp FROM audit_events WHERE event_type='api.mutation'
		AND json_extract(details_json,'$.path')='/api/v1/backup/export'
		AND json_extract(details_json,'$.status') LIKE '2%'
		ORDER BY timestamp DESC, id DESC LIMIT 1",
    )
}

fn sqlite_column_exists(conn: &Connection, table: &str, column: &str) -> rusqlite::Result<bool> {
    let mut stmt = conn.prepare(&format!("PRAGMA table_info({})", table))?;
    let mut rows = stmt.query([])?;
    while let Some(row) = rows.next()? {
        let name: String = row.get(1)?;
        if name == column {
            return Ok(true);
        }
    }
    Ok(false)
}

#[cfg(unix)]
pub fn set_mode(path: &str, mode: u32) -> std::io::Result<()> {
    use std::os::unix::fs::PermissionsExt;
    std::fs::set_permissions(path, std::fs::Permissions::from_mode(mode))
}

#[cfg(not(unix))]
pub fn set_mode(_path: &str, _mode: u32) -> std::io::Result<()> {
    Ok(())
}
