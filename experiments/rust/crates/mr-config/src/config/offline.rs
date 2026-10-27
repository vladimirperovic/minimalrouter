use rusqlite::{params, Connection, OpenFlags};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::sync::Mutex;

use super::model::{Revision, SystemConfig};
use super::policy::validate_live_candidate;
use super::store::{sql_err, SQLiteStore};
use crate::errorf;
use crate::goish::errors::{wrapf, Result};
use crate::goish::json;

/// Preserves the exact canonical bytes, including invalid legacy JSON. Only
/// the root offline recovery protocol may use this validation bypass.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct OfflineConfig {
    pub revision: i64,
    #[serde(with = "base64_bytes")]
    pub json: Vec<u8>,
}

/// Go encodes `[]byte` as standard base64.
pub(crate) mod base64_bytes {
    use base64::Engine;
    use serde::{Deserialize, Deserializer, Serializer};

    pub fn serialize<S: Serializer>(value: &[u8], s: S) -> Result<S::Ok, S::Error> {
        s.serialize_str(&base64::engine::general_purpose::STANDARD.encode(value))
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(d: D) -> Result<Vec<u8>, D::Error> {
        let text = Option::<String>::deserialize(d)?.unwrap_or_default();
        base64::engine::general_purpose::STANDARD.decode(text).map_err(serde::de::Error::custom)
    }
}

impl SQLiteStore {
    /// Opens an existing database without schema migration, default
    /// initialisation, or permission changes before the durable fence exists.
    pub fn open_offline_migration_store(dir: &str) -> Result<SQLiteStore> {
        let path = std::path::absolute(std::path::Path::new(dir).join("minimalrouter.db"))?;
        let conn = Connection::open_with_flags(&path, OpenFlags::SQLITE_OPEN_READ_WRITE | OpenFlags::SQLITE_OPEN_URI)?;
        conn.execute_batch("PRAGMA synchronous=FULL; PRAGMA foreign_keys=ON; PRAGMA trusted_schema=OFF; PRAGMA busy_timeout=5000;")?;
        let integrity: rusqlite::Result<String> = conn.query_row("PRAGMA quick_check", [], |r| r.get(0));
        if integrity.as_deref() != Ok("ok") {
            return Err(errorf!("offline database integrity check failed"));
        }
        let store = SQLiteStore { conn: Mutex::new(conn) };
        store
            .read_offline_config()
            .map_err(|e| wrapf("offline migration requires an existing canonical revision", e))?;
        Ok(store)
    }

    pub fn read_offline_config(&self) -> Result<OfflineConfig> {
        self.lock()
            .query_row("SELECT revision, config_json FROM config_revisions ORDER BY revision DESC LIMIT 1", [], |r| {
                let json: rusqlite::types::Value = r.get(1)?;
                let bytes = match json {
                    rusqlite::types::Value::Text(t) => t.into_bytes(),
                    rusqlite::types::Value::Blob(b) => b,
                    _ => Vec::new(),
                };
                Ok(OfflineConfig { revision: r.get(0)?, json: bytes })
            })
            .map_err(sql_err)
    }

    /// An idempotent compare-and-swap for a previously journaled, fully
    /// validated replacement. It never re-admits the invalid source.
    pub fn commit_offline_migration(&self, old: &OfflineConfig, target: &[u8]) -> Result<()> {
        let cfg: SystemConfig = json::unmarshal(target)?;
        validate_offline_candidate(&cfg)?;
        if old.revision < 1 || old.revision == i64::MAX || cfg.revision != (old.revision + 1) as Revision {
            return Err(errorf!("offline migration revision must advance exactly once"));
        }
        let mut conn = self.lock();
        let tx = conn.transaction().map_err(sql_err)?;
        let current: (i64, String) = tx
            .query_row("SELECT revision, config_json FROM config_revisions ORDER BY revision DESC LIMIT 1", [], |r| {
                Ok((r.get(0)?, r.get(1)?))
            })
            .map_err(sql_err)?;
        if current.0 == old.revision + 1 && current.1.as_bytes() == target {
            return Ok(()); // A prior attempt committed before its caller observed success.
        }
        if current.0 != old.revision || current.1.as_bytes() != old.json.as_slice() {
            return Err(errorf!("canonical configuration changed since offline migration was prepared"));
        }
        // Explicit row IDs keep the JSON revision and SQLite revision identical.
        let target_text = String::from_utf8_lossy(target).into_owned();
        tx.execute(
            "INSERT INTO config_revisions (revision, updated_at, config_json) VALUES (?, ?, ?)",
            params![old.revision + 1, cfg.updated_at.go_string(), target_text],
        )
        .map_err(sql_err)?;
        tx.execute("DELETE FROM sessions", []).map_err(sql_err)?;
        tx.commit().map_err(sql_err)?;
        Ok(())
    }
}

/// Requires real private values even for disabled features. API redaction
/// sentinels are never interpreted as literal secrets.
pub fn validate_offline_candidate(cfg: &SystemConfig) -> Result<()> {
    let mut secrets: BTreeMap<String, &str> = BTreeMap::new();
    secrets.insert("wan.password".into(), &cfg.wan.password);
    secrets.insert("wireguard.private_key".into(), &cfg.wireguard.private_key);
    secrets.insert("wg_client.private_key".into(), &cfg.wg_client.private_key);
    secrets.insert("wg_client.preshared_key".into(), &cfg.wg_client.preshared_key);
    secrets.insert("cloudflare.api_token".into(), &cfg.cloudflare.api_token);
    secrets.insert("cloudflare.tunnel_token".into(), &cfg.cloudflare.tunnel_token);
    secrets.insert("squid_proxy.password".into(), &cfg.squid_proxy.password);
    secrets.insert("wifi.passphrase".into(), &cfg.wifi.passphrase);
    for (i, peer) in cfg.wireguard.peers.iter().enumerate() {
        secrets.insert(format!("wireguard.peers[{}].preshared_key", i), &peer.preshared_key);
    }
    for (field, value) in secrets {
        if value.trim() == "[REDACTED]" {
            return Err(errorf!("{} contains a redaction placeholder; supply a complete private configuration", field));
        }
    }
    validate_live_candidate(cfg, None)
}
