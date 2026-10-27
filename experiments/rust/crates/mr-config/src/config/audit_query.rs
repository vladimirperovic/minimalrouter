use rusqlite::types::Value;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

use super::store::{sql_err, AuditEvent, SQLiteStore, AUDIT_RETENTION};
use crate::errorf;
use crate::goish::errors::Result;
use crate::goish::json;
use crate::goish::time::Time;

/// Classification shared by API consumers rather than guessed by each UI.
/// Only api.mutation uses its route; rejected access stays a security event.
const AUDIT_CATEGORY_SQL: &str = "CASE
 WHEN event_type GLOB 'auth.*' OR event_type GLOB 'access.*' OR event_type GLOB 'audit.*' THEN 'security'
 WHEN event_type GLOB 'recovery.*' OR event_type GLOB 'firmware.*' OR event_type GLOB 'service.*' OR event_type GLOB 'gateway.auto_recovery*'
   OR (event_type = 'config.transaction' AND json_extract(details_json, '$.state') IN ('RolledBack','RecoveryRequired')) THEN 'recovery'
 WHEN event_type GLOB 'dns*' OR event_type GLOB 'device.*' OR event_type GLOB 'gateway.*'
   OR event_type GLOB 'wireguard.*' OR event_type GLOB 'firewall.*' OR event_type GLOB 'dhcp.*'
   OR event_type GLOB 'wifi.*' OR event_type GLOB 'qos.*' OR event_type GLOB 'squid.*' THEN 'network'
 WHEN event_type IN ('api.mutation','config.request') THEN CASE
   WHEN json_extract(details_json, '$.path') GLOB '/api/v1/auth/*' THEN 'security'
   WHEN json_extract(details_json, '$.path') GLOB '/api/v1/backup/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/recovery/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/import/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/snapshots*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/firmware/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/system/actions/*' THEN 'recovery'
   WHEN json_extract(details_json, '$.path') GLOB '/api/v1/dns*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/devices/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/wireguard/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/firewall/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/gateway/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/qos/*' THEN 'network'
   ELSE 'configuration' END
 ELSE 'configuration' END";

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct AuditQuery {
    pub limit: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub category: String,
    #[serde(rename = "q", skip_serializing_if = "String::is_empty")]
    pub search: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub actor: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub event_type: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub since: Option<Time>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub until: Option<Time>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub cursor: String,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct AuditPage {
    pub events: Vec<AuditEvent>,
    pub filters: AuditQuery,
    pub has_more: bool,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub next_cursor: String,
    pub matching_count: i64,
    pub category_counts: BTreeMap<String, i64>,
    pub retained_count: i64,
    pub retention_limit: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub oldest_at: Option<Time>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub newest_at: Option<Time>,
    pub generated_at: Time,
    pub notice: String,
}

/// The raw SQLite timestamp preserves the indexed key exactly, including
/// fractional precision. No OFFSET means concurrent inserts do not duplicate
/// older pages.
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
struct AuditCursor {
    #[serde(rename = "t")]
    timestamp: String,
    id: String,
}

fn decode_audit_cursor(value: &str) -> Result<AuditCursor> {
    use base64::Engine;
    let invalid = || errorf!("invalid cursor");
    if value.len() > 512 {
        return Err(invalid());
    }
    let data = base64::engine::general_purpose::URL_SAFE_NO_PAD.decode(value).map_err(|_| invalid())?;
    let cursor: AuditCursor = json::unmarshal(&data).map_err(|_| invalid())?;
    if cursor.timestamp.is_empty() || cursor.timestamp.len() > 96 || !cursor.id.starts_with("audit-") || cursor.id.len() > 128 {
        return Err(invalid());
    }
    Ok(cursor)
}

impl AuditQuery {
    pub fn validate(&self) -> Result<()> {
        if self.limit < 1 || self.limit > 500 {
            return Err(errorf!("limit must be between 1 and 500"));
        }
        if !matches!(self.category.as_str(), "" | "all" | "security" | "configuration" | "network" | "recovery") {
            return Err(errorf!("invalid category"));
        }
        if self.search.len() > 256 || self.actor.len() > 255 || self.event_type.len() > 96 {
            return Err(errorf!("audit filter is too long"));
        }
        if let (Some(since), Some(until)) = (&self.since, &self.until) {
            if since.after(until) {
                return Err(errorf!("since must be before until"));
            }
        }
        if !self.cursor.is_empty() {
            decode_audit_cursor(&self.cursor)?;
        }
        Ok(())
    }
}

impl SQLiteStore {
    pub fn query_audit_events(&self, q: &AuditQuery) -> Result<AuditPage> {
        let mut page = AuditPage {
            events: Vec::new(),
            filters: q.clone(),
            retention_limit: AUDIT_RETENTION as i64,
            generated_at: Time::now_utc(),
            notice: "Local bounded history; older events may have been pruned. Request rejections are rate limited and share a 1000-event retention pool; audit.throttled and audit.suppressed describe suppression. Counts are stored records, not incident totals.".into(),
            ..Default::default()
        };
        q.validate()?;
        let mut conn = self.lock();
        let tx = conn.transaction().map_err(sql_err)?;
        let mut clauses: Vec<&str> = vec!["1=1"];
        let mut args: Vec<Value> = Vec::new();
        if !q.search.is_empty() {
            // instr treats SQL wildcards literally and searches every metadata field.
            clauses.push(
                "(instr(lower(event_type || ' ' || actor), lower(?)) > 0 OR EXISTS\n\t\t (SELECT 1 FROM json_each(details_json) WHERE instr(lower(key || ' ' || coalesce(value,'')), lower(?)) > 0))",
            );
            args.push(Value::Text(q.search.clone()));
            args.push(Value::Text(q.search.clone()));
        }
        if !q.actor.is_empty() {
            clauses.push("actor = ?");
            args.push(Value::Text(q.actor.clone()));
        }
        if !q.event_type.is_empty() {
            clauses.push("event_type = ?");
            args.push(Value::Text(q.event_type.clone()));
        }
        if let Some(since) = &q.since {
            clauses.push("timestamp >= ?");
            args.push(Value::Text(since.utc().go_string()));
        }
        if let Some(until) = &q.until {
            clauses.push("timestamp <= ?");
            args.push(Value::Text(until.utc().go_string()));
        }
        let base = format!(" FROM (SELECT *, {} AS category FROM audit_events) WHERE ", AUDIT_CATEGORY_SQL);
        {
            let sql = format!("SELECT category, count(*){}{} GROUP BY category", base, clauses.join(" AND "));
            let mut stmt = tx.prepare(&sql).map_err(sql_err)?;
            let rows = stmt
                .query_map(rusqlite::params_from_iter(args.iter()), |r| Ok((r.get::<_, String>(0)?, r.get::<_, i64>(1)?)))
                .map_err(sql_err)?;
            for row in rows {
                let (category, count) = row.map_err(sql_err)?;
                page.category_counts.insert(category.clone(), count);
                if q.category.is_empty() || q.category == "all" || q.category == category {
                    page.matching_count += count;
                }
            }
        }
        page.retained_count = tx.query_row("SELECT count(*) FROM audit_events", [], |r| r.get(0)).map_err(sql_err)?;
        if page.retained_count > 0 {
            let oldest: String = tx
                .query_row("SELECT timestamp FROM audit_events ORDER BY timestamp, id LIMIT 1", [], |r| r.get(0))
                .map_err(sql_err)?;
            let newest: String = tx
                .query_row("SELECT timestamp FROM audit_events ORDER BY timestamp DESC, id DESC LIMIT 1", [], |r| r.get(0))
                .map_err(sql_err)?;
            page.oldest_at = Time::parse_sqlite(&oldest);
            page.newest_at = Time::parse_sqlite(&newest);
        }
        if !q.category.is_empty() && q.category != "all" {
            clauses.push("category = ?");
            args.push(Value::Text(q.category.clone()));
        }
        if !q.cursor.is_empty() {
            let cursor = decode_audit_cursor(&q.cursor)?;
            clauses.push("(timestamp < ? OR (timestamp = ? AND id < ?))");
            args.push(Value::Text(cursor.timestamp.clone()));
            args.push(Value::Text(cursor.timestamp));
            args.push(Value::Text(cursor.id));
        }
        args.push(Value::Integer(q.limit + 1));
        let mut last = AuditCursor::default();
        {
            let sql = format!(
                "SELECT id,event_type,actor,timestamp,details_json,category,CAST(timestamp AS TEXT){}{} ORDER BY timestamp DESC,id DESC LIMIT ?",
                base,
                clauses.join(" AND ")
            );
            let mut stmt = tx.prepare(&sql).map_err(sql_err)?;
            let mut rows = stmt.query(rusqlite::params_from_iter(args.iter())).map_err(sql_err)?;
            while let Some(row) = rows.next().map_err(sql_err)? {
                let id: String = row.get(0).map_err(sql_err)?;
                let event_type: String = row.get(1).map_err(sql_err)?;
                let actor: String = row.get(2).map_err(sql_err)?;
                let timestamp: String = row.get(3).map_err(sql_err)?;
                let details: String = row.get(4).map_err(sql_err)?;
                let category: String = row.get(5).map_err(sql_err)?;
                let raw_time: String = row.get(6).map_err(sql_err)?;
                if page.events.len() as i64 == q.limit {
                    page.has_more = true;
                    break;
                }
                let details: Option<BTreeMap<String, String>> = json::unmarshal(details.as_bytes())?;
                page.events.push(AuditEvent {
                    category,
                    id: id.clone(),
                    event_type,
                    actor,
                    timestamp: Time::parse_sqlite(&timestamp).ok_or_else(|| errorf!("unparseable audit timestamp"))?,
                    details: details.unwrap_or_default(),
                });
                last = AuditCursor { timestamp: raw_time, id };
            }
        }
        if page.has_more {
            let data = json::marshal(&last)?;
            page.next_cursor = super::store::base64_raw_url(&data);
        }
        tx.commit().map_err(sql_err)?;
        Ok(page)
    }
}
