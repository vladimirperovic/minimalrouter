//! DNS activity history.
//!
//! Unlike byte accounting this is browsing history, so it is opt-in and kept
//! deliberately coarse on disk: one row per UTC day, device and site with a
//! lookup count and first/last time seen, plus hourly lookup totals per device.
//! Individual queries and full hostnames exist only in memory.

use rusqlite::types::Value as SqlValue;
use rusqlite::{params, params_from_iter, Connection, OptionalExtension};
use std::collections::HashMap;

use super::*;
use crate::goish::errors::wrapf;
use crate::goish::time::Duration;

/// Distinct device/site pairs stored per UTC day; new pairs beyond it are
/// counted as unitemized.
const MAX_DNS_DAILY_ROWS: i64 = 20000;
/// The whole daily table; the oldest days go first.
const MAX_DNS_TOTAL_ROWS: i64 = 1_000_000;

pub const DEFAULT_DNS_RETENTION_DAYS: i64 = 30;
pub const MAX_DNS_RETENTION_DAYS: i64 = 90;

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct DNSDailyKey {
    /// UTC midnight, Unix seconds.
    pub day: i64,
    pub address: String,
    pub site: String,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct DNSDailyCount {
    pub lookups: u64,
    pub first_seen: i64,
    pub last_seen: i64,
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct DNSHourlyKey {
    /// UTC hour start, Unix seconds.
    pub hour: i64,
    pub address: String,
}

/// One batch of aggregated lookups written in a single transaction.
#[derive(Clone, Debug, Default)]
pub struct DNSFlush {
    pub observed_hours: HashMap<i64, bool>,
    pub daily: HashMap<DNSDailyKey, DNSDailyCount>,
    pub hourly: HashMap<DNSHourlyKey, u64>,
    /// UTC day -> lookups not attributed to a site.
    pub unitemized: HashMap<i64, u64>,
}

impl DNSFlush {
    pub(crate) fn is_empty(&self) -> bool {
        self.daily.is_empty() && self.hourly.is_empty() && self.unitemized.is_empty() && self.observed_hours.is_empty()
    }
}

/// A bucket before history started or after the last collection is a gap.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DNSPoint {
    pub start: Time,
    pub lookups: u64,
    pub observed: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DNSDeviceUsage {
    pub address: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub hostname: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub mac: String,
    pub lookups: u64,
    pub sites: i64,
    pub last_seen: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DNSSiteUsage {
    pub site: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub category: String,
    pub lookups: u64,
    pub devices: i64,
    pub first_seen: i64,
    pub last_seen: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DNSDeviceSite {
    pub address: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub hostname: String,
    pub site: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub category: String,
    pub lookups: u64,
    pub first_seen: i64,
    pub last_seen: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DNSActivity {
    pub collection: DNSCollectionStatus,
    pub available: bool,
    pub enabled: bool,
    pub retention_days: i64,
    pub period: String,
    pub from: Time,
    pub until: Time,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub device: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub search: String,
    pub history_started_at: Option<Time>,
    pub collected_at: Option<Time>,
    pub total_lookups: u64,
    pub unitemized_lookups: u64,
    pub site_count: i64,
    pub points: Vec<DNSPoint>,
    pub devices: Vec<DNSDeviceUsage>,
    pub sites: Vec<DNSSiteUsage>,
    pub flagged: Vec<DNSDeviceSite>,
}

#[derive(Clone, Debug, Default)]
pub struct DNSQuery {
    pub period: String,
    pub device: String,
    pub search: String,
    /// Matched exactly against stored sites; the store knows no categories.
    pub flagged_sites: Vec<String>,
    pub site_limit: i64,
}

pub(super) fn migrate_dns(conn: &Connection) -> Result<()> {
    conn.execute_batch(
        "
    CREATE TABLE IF NOT EXISTS dns_daily (day INTEGER NOT NULL, address TEXT NOT NULL, site TEXT NOT NULL, lookups INTEGER NOT NULL,
        first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL, PRIMARY KEY (day, address, site)) WITHOUT ROWID;
    CREATE INDEX IF NOT EXISTS idx_dns_daily_site ON dns_daily(day, site);
    CREATE TABLE IF NOT EXISTS dns_hourly (hour INTEGER NOT NULL, address TEXT NOT NULL, lookups INTEGER NOT NULL, PRIMARY KEY (hour, address)) WITHOUT ROWID;
    CREATE TABLE IF NOT EXISTS dns_unitemized (day INTEGER PRIMARY KEY, lookups INTEGER NOT NULL);
    CREATE TABLE IF NOT EXISTS dns_clock (id INTEGER PRIMARY KEY CHECK(id = 1), started_at INTEGER NOT NULL, last_at INTEGER NOT NULL);
    CREATE TABLE IF NOT EXISTS dns_observed (hour INTEGER PRIMARY KEY);
    CREATE TABLE IF NOT EXISTS dns_settings (id INTEGER PRIMARY KEY CHECK(id = 1), enabled INTEGER NOT NULL, retention_days INTEGER NOT NULL);
    ",
    )
    .map_err(|e| wrapf("migrate DNS activity store", e))
}

impl Store {
    /// Recording is off until enabled.
    pub fn dns_settings(&self) -> Result<DNSSettings> {
        let row: Option<(i64, i64)> = self.lock().query_row("SELECT enabled, retention_days FROM dns_settings WHERE id = 1", [], |r| Ok((r.get(0)?, r.get(1)?))).optional()?;
        Ok(match row {
            Some((enabled, retention_days)) => DNSSettings { enabled: enabled != 0, retention_days },
            None => DNSSettings { enabled: false, retention_days: DEFAULT_DNS_RETENTION_DAYS },
        })
    }

    pub fn set_dns_settings(&self, settings: &DNSSettings) -> Result<()> {
        if settings.retention_days < 1 || settings.retention_days > MAX_DNS_RETENTION_DAYS {
            return Err(errorf!("retention_days must be between 1 and {}", MAX_DNS_RETENTION_DAYS));
        }
        self.lock().execute(
            "INSERT INTO dns_settings(id, enabled, retention_days) VALUES (1, ?, ?) ON CONFLICT(id) DO UPDATE SET enabled = excluded.enabled, retention_days = excluded.retention_days",
            params![settings.enabled as i64, settings.retention_days],
        )?;
        Ok(())
    }

    /// Writes one flush in a single transaction. A day at MAX_DNS_DAILY_ROWS
    /// only grows existing pairs; lookups for new pairs become unitemized.
    pub fn record_dns(&self, now: Time, flush: &DNSFlush) -> Result<()> {
        if flush.is_empty() {
            return Ok(());
        }
        let mut conn = self.lock();
        let txn = conn.transaction()?;
        let mut rows_per_day: HashMap<i64, i64> = HashMap::new();
        let mut unitemized = flush.unitemized.clone();
        let mut keys: Vec<&DNSDailyKey> = flush.daily.keys().collect();
        // Busy pairs first, so a day at the bound keeps its significant sites.
        keys.sort_by(|a, b| flush.daily[*b].lookups.cmp(&flush.daily[*a].lookups));
        for key in keys {
            let count = flush.daily[key];
            let changed = txn
                .execute(
                    "UPDATE dns_daily SET lookups = lookups + ?, first_seen = MIN(first_seen, ?), last_seen = MAX(last_seen, ?) WHERE day = ? AND address = ? AND site = ?",
                    params![count.lookups, count.first_seen, count.last_seen, key.day, key.address, key.site],
                )
                .map_err(|e| wrapf("update DNS activity", e))?;
            if changed > 0 {
                continue;
            }
            let rows = match rows_per_day.get(&key.day) {
                Some(rows) => *rows,
                None => txn.query_row("SELECT COUNT(*) FROM dns_daily WHERE day = ?", params![key.day], |r| r.get(0)).map_err(|e| wrapf("count DNS activity rows", e))?,
            };
            if rows >= MAX_DNS_DAILY_ROWS {
                rows_per_day.insert(key.day, rows);
                *unitemized.entry(key.day).or_default() += count.lookups;
                continue;
            }
            txn.execute(
                "INSERT INTO dns_daily(day, address, site, lookups, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?)",
                params![key.day, key.address, key.site, count.lookups, count.first_seen, count.last_seen],
            )
            .map_err(|e| wrapf("insert DNS activity", e))?;
            rows_per_day.insert(key.day, rows + 1);
        }
        for (key, lookups) in &flush.hourly {
            txn.execute("INSERT INTO dns_hourly(hour, address, lookups) VALUES (?, ?, ?) ON CONFLICT(hour, address) DO UPDATE SET lookups = lookups + excluded.lookups", params![key.hour, key.address, lookups])
                .map_err(|e| wrapf("record hourly DNS activity", e))?;
        }
        for (day, lookups) in unitemized {
            if lookups == 0 {
                continue;
            }
            txn.execute("INSERT INTO dns_unitemized(day, lookups) VALUES (?, ?) ON CONFLICT(day) DO UPDATE SET lookups = lookups + excluded.lookups", params![day, lookups])
                .map_err(|e| wrapf("record unitemized DNS activity", e))?;
        }
        for hour in flush.observed_hours.keys() {
            txn.execute("INSERT OR IGNORE INTO dns_observed(hour) VALUES(?)", params![hour])?;
        }
        let epoch = now.utc().unix();
        txn.execute("INSERT INTO dns_clock(id, started_at, last_at) VALUES (1, ?, ?) ON CONFLICT(id) DO UPDATE SET last_at = excluded.last_at", params![epoch, epoch])
            .map_err(|e| wrapf("record DNS activity clock", e))?;
        txn.commit()?;
        Ok(())
    }

    /// Removes days older than the retention window, future-dated rows left
    /// by a clock correction, and the oldest days beyond MAX_DNS_TOTAL_ROWS.
    pub fn prune_dns(&self, now: Time, retention_days: i64) -> Result<()> {
        let retention_days = if retention_days <= 0 { DEFAULT_DNS_RETENTION_DAYS } else { retention_days };
        let today = utc_day(now);
        let cutoff = today - (retention_days - 1) * 86400;
        let tomorrow = today + 86400;
        let mut conn = self.lock();
        let txn = conn.transaction()?;
        for statement in [
            "DELETE FROM dns_daily WHERE day < ? OR day >= ?",
            "DELETE FROM dns_hourly WHERE hour < ? OR hour >= ?",
            "DELETE FROM dns_unitemized WHERE day < ? OR day >= ?",
            "DELETE FROM dns_observed WHERE hour < ? OR hour >= ?",
        ] {
            txn.execute(statement, params![cutoff, tomorrow]).map_err(|e| wrapf("prune DNS activity", e))?;
        }
        let mut total: i64 = txn.query_row("SELECT COUNT(*) FROM dns_daily", [], |r| r.get(0))?;
        while total > MAX_DNS_TOTAL_ROWS {
            let oldest: i64 = txn.query_row("SELECT MIN(day) FROM dns_daily", [], |r| r.get(0))?;
            let removed = txn.execute("DELETE FROM dns_daily WHERE day = ?", params![oldest])? as i64;
            if removed == 0 {
                break;
            }
            total -= removed;
            txn.execute("DELETE FROM dns_hourly WHERE hour < ?", params![oldest + 86400])?;
            txn.execute("DELETE FROM dns_observed WHERE hour < ?", params![oldest + 86400])?;
            txn.execute("DELETE FROM dns_unitemized WHERE day <= ?", params![oldest])?;
        }
        txn.commit()?;
        Ok(())
    }

    pub fn has_dns_history(&self) -> Result<bool> {
        let exists: i64 = self.lock().query_row(
            "SELECT EXISTS(SELECT 1 FROM dns_daily) OR EXISTS(SELECT 1 FROM dns_hourly) OR EXISTS(SELECT 1 FROM dns_unitemized) OR EXISTS(SELECT 1 FROM dns_clock)",
            [],
            |r| r.get(0),
        )?;
        Ok(exists != 0)
    }

    pub fn clear_dns(&self) -> Result<()> {
        let mut conn = self.lock();
        let txn = conn.transaction()?;
        for table in ["dns_daily", "dns_hourly", "dns_unitemized", "dns_clock", "dns_observed"] {
            txn.execute(&format!("DELETE FROM {}", table), [])?;
        }
        txn.commit()?;
        Ok(())
    }

    /// One period; days and hours are UTC, matching the traffic insights.
    pub fn dns_activity(&self, now: Time, query: &DNSQuery) -> Result<DNSActivity> {
        let (from, until, step) = insights_range(now, &query.period)?;
        let mut result = DNSActivity { available: true, period: query.period.clone(), from, until, device: query.device.clone(), search: query.search.clone(), ..Default::default() };
        let limit = if query.site_limit <= 0 || query.site_limit > 500 { 200 } else { query.site_limit };
        let conn = self.lock();
        let clock: Option<(Option<i64>, Option<i64>)> = conn.query_row("SELECT started_at, last_at FROM dns_clock WHERE id = 1", [], |r| Ok((r.get(0)?, r.get(1)?))).optional()?;
        let (started, last) = clock.unwrap_or_default();
        result.history_started_at = started.map(|s| Time::from_unix(s, 0));
        result.collected_at = last.map(|s| Time::from_unix(s, 0));

        // Day keys are UTC midnights; until is exclusive.
        let (from_day, until_epoch) = (from.unix(), until.unix());
        let mut filter = "day >= ? AND day < ?".to_string();
        let mut args: Vec<SqlValue> = vec![from_day.into(), until_epoch.into()];
        if !query.device.is_empty() {
            filter += " AND address = ?";
            args.push(query.device.clone().into());
        }
        let (total, sites): (u64, i64) = conn.query_row(&format!("SELECT COALESCE(SUM(lookups), 0), COUNT(DISTINCT site) FROM dns_daily WHERE {}", filter), params_from_iter(args.iter()), |r| Ok((r.get(0)?, r.get(1)?)))?;
        result.total_lookups = total;
        result.site_count = sites;
        if query.device.is_empty() {
            result.unitemized_lookups = conn.query_row("SELECT COALESCE(SUM(lookups), 0) FROM dns_unitemized WHERE day >= ? AND day < ?", params![from_day, until_epoch], |r| r.get(0))?;
            result.total_lookups += result.unitemized_lookups;
        }
        result.points = dns_points(&conn, from, until, step, &query.device)?;

        let mut stmt = conn.prepare(&format!("SELECT address, SUM(lookups), COUNT(DISTINCT site), MAX(last_seen) FROM dns_daily WHERE {} GROUP BY address ORDER BY SUM(lookups) DESC LIMIT 100", filter))?;
        result.devices = stmt
            .query_map(params_from_iter(args.iter()), |r| Ok(DNSDeviceUsage { address: r.get(0)?, lookups: r.get(1)?, sites: r.get(2)?, last_seen: r.get(3)?, ..Default::default() }))?
            .collect::<rusqlite::Result<_>>()?;

        let mut site_filter = filter.clone();
        let mut site_args = args.clone();
        if !query.search.is_empty() {
            site_filter += " AND site LIKE ? ESCAPE '\\'";
            site_args.push(format!("%{}%", escape_like(&query.search)).into());
        }
        site_args.push(limit.into());
        let mut stmt = conn.prepare(&format!(
            "SELECT site, SUM(lookups), COUNT(DISTINCT address), MIN(first_seen), MAX(last_seen) FROM dns_daily WHERE {} GROUP BY site ORDER BY SUM(lookups) DESC, site LIMIT ?",
            site_filter
        ))?;
        result.sites = stmt
            .query_map(params_from_iter(site_args.iter()), |r| Ok(DNSSiteUsage { site: r.get(0)?, lookups: r.get(1)?, devices: r.get(2)?, first_seen: r.get(3)?, last_seen: r.get(4)?, ..Default::default() }))?
            .collect::<rusqlite::Result<_>>()?;

        if !query.flagged_sites.is_empty() {
            let placeholders = vec!["?"; query.flagged_sites.len()].join(",");
            let mut flagged_args = args.clone();
            flagged_args.extend(query.flagged_sites.iter().map(|s| SqlValue::from(s.clone())));
            let mut stmt = conn.prepare(&format!(
                "SELECT address, site, SUM(lookups), MIN(first_seen), MAX(last_seen) FROM dns_daily WHERE {} AND site IN ({}) GROUP BY address, site ORDER BY MAX(last_seen) DESC LIMIT 200",
                filter, placeholders
            ))?;
            result.flagged = stmt
                .query_map(params_from_iter(flagged_args.iter()), |r| Ok(DNSDeviceSite { address: r.get(0)?, site: r.get(1)?, lookups: r.get(2)?, first_seen: r.get(3)?, last_seen: r.get(4)?, ..Default::default() }))?
                .collect::<rusqlite::Result<_>>()?;
        }
        Ok(result)
    }
}

fn dns_points(conn: &Connection, from: Time, until: Time, step: Duration, device: &str) -> Result<Vec<DNSPoint>> {
    let mut sql = "SELECT hour, SUM(lookups) FROM dns_hourly WHERE hour >= ? AND hour < ?".to_string();
    let mut args: Vec<SqlValue> = vec![from.unix().into(), until.unix().into()];
    if !device.is_empty() {
        sql += " AND address = ?";
        args.push(device.to_string().into());
    }
    let step_seconds = step.0 / 1_000_000_000;
    let bucket = |hour: i64| from.unix() + (hour - from.unix()) / step_seconds * step_seconds;
    let mut by_bucket: HashMap<i64, u64> = HashMap::new();
    let mut stmt = conn.prepare(&(sql + " GROUP BY hour"))?;
    for row in stmt.query_map(params_from_iter(args.iter()), |r| Ok((r.get::<_, i64>(0)?, r.get::<_, u64>(1)?)))? {
        let (hour, lookups) = row?;
        *by_bucket.entry(bucket(hour)).or_default() += lookups;
    }
    let mut observed: HashMap<i64, bool> = HashMap::new();
    let mut stmt = conn.prepare("SELECT hour FROM dns_observed WHERE hour>=? AND hour<?")?;
    for row in stmt.query_map(params![from.unix(), until.unix()], |r| r.get::<_, i64>(0))? {
        observed.insert(bucket(row?), true);
    }
    let mut points = Vec::new();
    let mut start = from;
    while start.before(&until) {
        let lookups = by_bucket.get(&start.unix()).copied().unwrap_or(0);
        points.push(DNSPoint { start, lookups, observed: observed.contains_key(&start.unix()) || lookups > 0 });
        start = start.add(step);
    }
    Ok(points)
}

fn escape_like(value: &str) -> String {
    value.replace('\\', "\\\\").replace('%', "\\%").replace('_', "\\_")
}

pub(crate) fn utc_day(at: Time) -> i64 {
    at.utc().truncate(Duration::hours(24)).unix()
}

const SECOND_LEVEL_LABELS: [&str; 17] = ["ac", "co", "com", "edu", "gob", "gov", "go", "in", "ltd", "mil", "ne", "net", "nic", "or", "org", "plc", "sch"];

/// Groups a hostname under the registrable domain a person would recognize:
/// two labels, or three when a two-letter country code is preceded by a common
/// second-level label. A deliberate approximation of the public suffix list.
pub fn site_of(name: &str) -> String {
    let labels: Vec<&str> = name.split('.').collect();
    let count = labels.len();
    if count <= 2 {
        return name.to_string();
    }
    if labels[count - 1].len() == 2 && SECOND_LEVEL_LABELS.contains(&labels[count - 2]) {
        return labels[count - 3..].join(".");
    }
    labels[count - 2..].join(".")
}
