use rusqlite::{params, Connection, OptionalExtension, Transaction};
use std::collections::HashMap;
use std::sync::{Mutex, MutexGuard};

use super::*;
use crate::goish::errors::wrapf;
use crate::goish::time::Duration;

/// Just over a year, so "same month last year" works without unbounded growth.
const DEFAULT_RETENTION_MONTHS: i64 = 13;
/// Matches the nftables set size so neither side can outgrow the other.
pub(crate) const MAX_TRACKED_DEVICES: i64 = 512;
const RECENT_DAYS: i64 = 32;

/// Monthly per-device totals plus the last raw kernel counter for each host,
/// so a routerd restart cannot double-count traffic already recorded.
pub struct Store {
    conn: Mutex<Connection>,
}

impl Store {
    pub fn open(data_dir: &str) -> Result<Store> {
        std::fs::create_dir_all(data_dir).map_err(|e| wrapf("create accounting data directory", e))?;
        let path = std::path::Path::new(data_dir).join("accounting.db");
        let conn = Connection::open(&path).map_err(|e| wrapf("open accounting store", e))?;
        conn.execute_batch("PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA trusted_schema=OFF; PRAGMA secure_delete=ON; PRAGMA busy_timeout=3000; PRAGMA cache_size=-1000;")
            .map_err(|e| wrapf("initialize accounting store", e))?;
        crate::config::set_mode(&path.to_string_lossy(), 0o600).map_err(|e| wrapf("secure accounting store", e))?;
        migrate(&conn)?;
        Ok(Store { conn: Mutex::new(conn) })
    }

    pub(crate) fn lock(&self) -> MutexGuard<'_, Connection> {
        self.conn.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// Folds one collection round into the monthly buckets. `generation`
    /// identifies the counter set the values were read from.
    pub fn record(&self, now: Time, rx: &[Counter], tx: &[Counter], retention_months: i64, generation: u64) -> Result<()> {
        let retention_months = if retention_months <= 0 { DEFAULT_RETENTION_MONTHS } else { retention_months };
        let month = month_key(now);
        let mut conn = self.lock();
        let txn = conn.transaction()?;
        let mut recent: HashMap<String, DeviceUsage> = HashMap::new();
        let mut apply = |direction: &str, counters: &[Counter]| -> rusqlite::Result<()> {
            for counter in counters {
                let cursor: Option<(u64, u64)> = txn
                    .query_row("SELECT last_raw, generation FROM device_cursor WHERE address = ? AND direction = ?", params![counter.address, direction], |r| Ok((r.get(0)?, r.get(1)?)))
                    .optional()?;
                let (previous, previous_generation) = cursor.unwrap_or((0, generation));
                let added = delta_across_generations(previous_generation, generation, previous, counter.bytes);
                txn.execute(
                    "INSERT INTO device_cursor(address, direction, last_raw, generation) VALUES (?, ?, ?, ?) ON CONFLICT(address, direction) DO UPDATE SET last_raw = excluded.last_raw, generation = excluded.generation",
                    params![counter.address, direction, counter.bytes, generation],
                )?;
                if added == 0 {
                    continue;
                }
                let usage = recent.entry(counter.address.clone()).or_insert_with(|| DeviceUsage { address: counter.address.clone(), ..Default::default() });
                let column = if direction == "rx" {
                    usage.rx_bytes += added;
                    "rx_bytes"
                } else {
                    usage.tx_bytes += added;
                    "tx_bytes"
                };
                txn.execute(
                    &format!("INSERT INTO device_month(address, month, {c}, last_seen) VALUES (?, ?, ?, ?) ON CONFLICT(address, month) DO UPDATE SET {c} = {c} + excluded.{c}, last_seen = excluded.last_seen", c = column),
                    params![counter.address, month, added, now.utc().unix()],
                )?;
            }
            Ok(())
        };
        apply("rx", rx).map_err(|e| wrapf("record download counters", e))?;
        apply("tx", tx).map_err(|e| wrapf("record upload counters", e))?;
        record_insights(&txn, now, &recent).map_err(|e| wrapf("record recent traffic", e))?;
        let cutoff = now.utc().add_date(0, -retention_months, 0).format("%Y-%m");
        txn.execute("DELETE FROM device_month WHERE month < ?", params![cutoff]).map_err(|e| wrapf("prune accounting history", e))?;
        txn.execute("DELETE FROM device_cursor WHERE address NOT IN (SELECT address FROM device_month)", []).map_err(|e| wrapf("prune accounting cursors", e))?;
        txn.execute(
            "DELETE FROM device_month WHERE rowid NOT IN (SELECT rowid FROM device_month ORDER BY month DESC, rx_bytes + tx_bytes DESC LIMIT ?)",
            params![MAX_TRACKED_DEVICES * retention_months],
        )
        .map_err(|e| wrapf("bound accounting rows", e))?;
        txn.commit()?;
        Ok(())
    }

    /// The newest `limit` calendar months, devices sorted by total descending.
    pub fn months(&self, limit: i64) -> Result<List<MonthUsage>> {
        let limit = if limit <= 0 { 3 } else { limit };
        let conn = self.lock();
        let mut stmt = conn.prepare(
            "SELECT address, month, rx_bytes, tx_bytes, last_seen FROM device_month WHERE month IN (SELECT DISTINCT month FROM device_month ORDER BY month DESC LIMIT ?) ORDER BY month DESC",
        )?;
        let rows = stmt.query_map(params![limit], |r| {
            let (rx, tx): (u64, u64) = (r.get(2)?, r.get(3)?);
            Ok((r.get::<_, String>(1)?, DeviceUsage { address: r.get(0)?, rx_bytes: rx, tx_bytes: tx, total_bytes: rx + tx, last_seen_utc: r.get(4)?, ..Default::default() }))
        })?;
        let mut order: Vec<String> = Vec::new();
        let mut by_month: HashMap<String, Vec<DeviceUsage>> = HashMap::new();
        for row in rows {
            let (month, usage) = row?;
            if !by_month.contains_key(&month) {
                order.push(month.clone());
            }
            by_month.entry(month).or_default().push(usage);
        }
        let mut out = List::empty();
        for month in order {
            let mut devices = by_month.remove(&month).unwrap_or_default();
            devices.sort_by(|a, b| b.total_bytes.cmp(&a.total_bytes));
            let total = devices.iter().map(|d| d.total_bytes).sum();
            out.push(MonthUsage { month, total_bytes: total, devices: devices.into() });
        }
        Ok(out)
    }

    /// Clears all accounting history when the operator disables the feature.
    pub fn reset(&self) -> Result<()> {
        self.clear_history()
    }

    /// Deletes every per-device record and durably marks the disable as
    /// carried out in one transaction, so a failed clear is retried.
    pub fn clear_history(&self) -> Result<()> {
        let mut conn = self.lock();
        let txn = conn.transaction()?;
        for table in ["traffic_hour", "device_day", "traffic_clock", "device_month", "device_cursor"] {
            txn.execute(&format!("DELETE FROM {}", table), [])?;
        }
        txn.execute("INSERT INTO accounting_state(id, history_cleared) VALUES (1, 1) ON CONFLICT(id) DO UPDATE SET history_cleared = 1", [])?;
        txn.commit()?;
        Ok(())
    }

    /// Whether a disable has already been honored, so a restarted collector
    /// does not have to guess.
    pub fn history_cleared(&self) -> Result<bool> {
        let conn = self.lock();
        match conn.query_row("SELECT history_cleared FROM accounting_state WHERE id = 1", [], |r| r.get::<_, i64>(0)).optional()? {
            Some(cleared) => Ok(cleared != 0),
            // A store carrying rows from a release that predates the flag
            // still has something to clear.
            None => Ok(conn.query_row("SELECT COUNT(*) FROM device_month", [], |r| r.get::<_, i64>(0))? == 0),
        }
    }

    pub fn mark_history_live(&self) -> Result<()> {
        self.lock().execute("INSERT INTO accounting_state(id, history_cleared) VALUES (1, 0) ON CONFLICT(id) DO UPDATE SET history_cleared = 0", [])?;
        Ok(())
    }

    /// A bounded inventory projection with no destinations or DNS history.
    pub fn device_activity(&self, now: Time) -> Result<Vec<DeviceActivity>> {
        let now = now.utc();
        let month = now.format("%Y-%m");
        let previous = Time::date_utc(now.year(), now.month(), 1).add_date(0, -1, 0).format("%Y-%m");
        let conn = self.lock();
        let mut stmt = conn.prepare(
            "SELECT address, MAX(last_seen), SUM(CASE WHEN month = ? THEN rx_bytes + tx_bytes ELSE 0 END), MAX(CASE WHEN month = ? THEN 1 ELSE 0 END)
             FROM device_month WHERE month >= ? AND month <= ? GROUP BY address ORDER BY MAX(last_seen) DESC, address LIMIT ?",
        )?;
        let rows = stmt.query_map(params![month, previous, previous, month, MAX_TRACKED_DEVICES], |r| {
            Ok(DeviceActivity { address: r.get(0)?, last_seen_epoch: r.get(1)?, month_bytes: r.get(2)?, new_this_month: r.get::<_, i64>(3)? == 0 })
        })?;
        Ok(rows.collect::<rusqlite::Result<_>>()?)
    }

    /// Starts from a baseline after startup/reset or a collection gap; no
    /// pre-upgrade totals are assigned to the last 24 hours.
    pub fn record_firewall(&self, now: Time, generation: &str, seen: u64, accepted: u64) -> Result<()> {
        let mut conn = self.lock();
        let txn = conn.transaction()?;
        let clock: Option<(i64, String, u64, u64)> =
            txn.query_row("SELECT at,generation,seen,accepted FROM firewall_clock WHERE id=1", [], |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?))).optional()?;
        let (at, previous, old_seen, old_accepted) = clock.unwrap_or_default();
        let elapsed = now.unix() - at;
        if !generation.is_empty() && generation == previous && elapsed > 0 && elapsed <= 180 && seen >= old_seen && accepted >= old_accepted && seen - old_seen >= accepted - old_accepted {
            let allowed = accepted - old_accepted;
            let blocked = seen - old_seen - allowed;
            txn.execute(
                "INSERT INTO firewall_minute(minute,allowed,blocked,samples) VALUES(?,?,?,1) ON CONFLICT(minute) DO UPDATE SET allowed=allowed+excluded.allowed,blocked=blocked+excluded.blocked,samples=samples+1",
                params![now.utc().truncate(Duration::MINUTE).unix(), allowed, blocked],
            )?;
        }
        txn.execute(
            "INSERT INTO firewall_clock(id,at,generation,seen,accepted) VALUES(1,?,?,?,?) ON CONFLICT(id) DO UPDATE SET at=excluded.at,generation=excluded.generation,seen=excluded.seen,accepted=excluded.accepted",
            params![now.unix(), generation, seen, accepted],
        )?;
        txn.execute("DELETE FROM firewall_minute WHERE minute<? OR minute>?", params![now.add(Duration::hours(-25)).unix(), now.add(Duration::MINUTE).unix()])?;
        txn.commit()?;
        Ok(())
    }

    pub fn firewall_activity(&self, now: Time) -> Result<FirewallActivity> {
        let until = now.utc().truncate(Duration::MINUTE).add(Duration::MINUTE);
        let from = until.add(Duration::hours(-24));
        let mut out = FirewallActivity { from, until: now.utc(), ..Default::default() };
        let mut conn = self.lock();
        let txn = conn.transaction()?;
        let last: i64 = txn.query_row("SELECT at FROM firewall_clock WHERE id=1", [], |r| r.get(0)).optional()?.unwrap_or(0);
        if last > 0 {
            out.collected_at = Some(Time::from_unix(last, 0));
            out.available = now.unix() - last >= 0 && now.unix() - last <= 180;
        }
        out.points = (0..48).map(|i| FirewallPoint { start: from.add(Duration::minutes(30 * i)), ..Default::default() }).collect();
        {
            let mut stmt = txn.prepare("SELECT minute,allowed,blocked,samples FROM firewall_minute WHERE minute>=? AND minute<? ORDER BY minute")?;
            let rows = stmt.query_map(params![from.unix(), until.unix()], |r| Ok((r.get::<_, i64>(0)?, r.get::<_, u64>(1)?, r.get::<_, u64>(2)?, r.get::<_, i64>(3)?)))?;
            for row in rows {
                let (minute, allowed, blocked, samples) = row?;
                let point = &mut out.points[((minute - from.unix()) / 1800) as usize];
                point.allowed += allowed;
                point.blocked += blocked;
                point.samples += samples;
                out.allowed += allowed;
                out.blocked += blocked;
            }
        }
        txn.commit()?;
        Ok(out)
    }

    pub fn insights(&self, now: Time, period: &str) -> Result<Insights> {
        let mut result = Insights { period: period.into(), ..Default::default() };
        let (from, until, step) = insights_range(now, period)?;
        result.from = from;
        result.until = until;
        // One transaction, so panels agree during collection.
        let mut conn = self.lock();
        let txn = conn.transaction()?;
        let clock: Option<(i64, i64)> = txn.query_row("SELECT started_at,last_at FROM traffic_clock WHERE id=1", [], |r| Ok((r.get(0)?, r.get(1)?))).optional()?;
        let (started, last) = clock.unwrap_or_default();
        if started > 0 {
            result.history_started_at = Some(Time::from_unix(started, 0));
        }
        if last > 0 {
            result.collected_at = Some(Time::from_unix(last, 0));
        }
        let point_end = if period == "today" { from.add(Duration::hours(24)) } else { until };
        let mut points: HashMap<i64, UsagePoint> = HashMap::new();
        let mut starts = Vec::new();
        let mut at = from;
        while at.before(&point_end) {
            points.insert(at.unix(), UsagePoint { start: at, ..Default::default() });
            starts.push(at.unix());
            at = at.add(step);
        }
        {
            let mut stmt = txn.prepare("SELECT hour,rx_bytes,tx_bytes,samples,observed_seconds,peak_mbps FROM traffic_hour WHERE hour>=? AND hour<? ORDER BY hour")?;
            let rows = stmt.query_map(params![from.unix(), until.unix()], |r| {
                Ok((r.get::<_, i64>(0)?, r.get::<_, u64>(1)?, r.get::<_, u64>(2)?, r.get::<_, i64>(3)?, r.get::<_, i64>(4)?, r.get::<_, f64>(5)?))
            })?;
            let step_seconds = step.0 / 1_000_000_000;
            for row in rows {
                let (hour, rx, upload, samples, seconds, peak) = row?;
                let key = from.unix() + (hour - from.unix()) / step_seconds * step_seconds;
                let Some(point) = points.get_mut(&key) else { continue };
                point.rx_bytes += rx;
                point.tx_bytes += upload;
                point.total_bytes += rx + upload;
                point.samples += samples;
                point.observed_seconds += seconds;
                result.rx_bytes += rx;
                result.tx_bytes += upload;
                result.total_bytes += rx + upload;
                result.observed_seconds += seconds;
                if result.peak_sample_mbps.is_none_or(|current| peak > current) {
                    result.peak_sample_mbps = Some(peak);
                }
            }
        }
        result.points = starts.iter().filter_map(|start| points.remove(start)).collect();
        {
            let mut stmt = txn.prepare(
                "SELECT address,SUM(rx_bytes),SUM(tx_bytes),MAX(last_seen) FROM device_day WHERE day>=? AND day<=? GROUP BY address ORDER BY SUM(rx_bytes)+SUM(tx_bytes) DESC,address LIMIT ?",
            )?;
            let rows = stmt.query_map(params![from.format("%Y-%m-%d"), until.add(Duration(-1)).format("%Y-%m-%d"), MAX_TRACKED_DEVICES], |r| {
                let (rx, tx): (u64, u64) = (r.get(1)?, r.get(2)?);
                Ok(DeviceUsage { address: r.get(0)?, rx_bytes: rx, tx_bytes: tx, total_bytes: rx + tx, last_seen_utc: r.get(3)?, ..Default::default() })
            })?;
            result.devices = rows.collect::<rusqlite::Result<_>>()?;
        }
        txn.commit()?;
        result.available = true;
        result.enabled = true;
        Ok(result)
    }
}

/// A bounded inventory projection, not a second copy of the monthly report.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DeviceActivity {
    pub address: String,
    pub last_seen_epoch: i64,
    pub month_bytes: u64,
    pub new_this_month: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FirewallPoint {
    pub start: Time,
    pub allowed: u64,
    pub blocked: u64,
    pub samples: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FirewallActivity {
    pub available: bool,
    pub from: Time,
    pub until: Time,
    pub collected_at: Option<Time>,
    pub points: Vec<FirewallPoint>,
    pub allowed: u64,
    pub blocked: u64,
}

/// Aggregate byte deltas observed in a UTC hour/day. A point with no samples
/// is a gap, not proof of zero traffic.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct UsagePoint {
    pub start: Time,
    pub rx_bytes: u64,
    pub tx_bytes: u64,
    pub total_bytes: u64,
    pub samples: i64,
    pub observed_seconds: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Insights {
    pub available: bool,
    pub enabled: bool,
    pub period: String,
    pub from: Time,
    pub until: Time,
    pub history_started_at: Option<Time>,
    pub collected_at: Option<Time>,
    pub points: Vec<UsagePoint>,
    pub devices: Vec<DeviceUsage>,
    pub rx_bytes: u64,
    pub tx_bytes: u64,
    pub total_bytes: u64,
    pub peak_sample_mbps: Option<f64>,
    pub observed_seconds: i64,
}

/// UTC calendar boundaries, consistently with monthly usage.
pub fn insights_range(now: Time, period: &str) -> Result<(Time, Time, Duration)> {
    let now = now.utc();
    let today = Time::date_utc(now.year(), now.month(), now.day());
    match period {
        "today" => Ok((today, now, Duration::HOUR)),
        "yesterday" => Ok((today.add_date(0, 0, -1), today, Duration::HOUR)),
        "7d" => Ok((today.add_date(0, 0, -6), now, Duration::hours(24))),
        "30d" => Ok((today.add_date(0, 0, -29), now, Duration::hours(24))),
        _ => Err(errorf!("period must be today, yesterday, 7d or 30d")),
    }
}

/// Recent history begins with a fresh baseline: counters spanning an outage
/// cannot be assigned to an hour. Monthly totals keep those deltas.
fn record_insights(txn: &Transaction<'_>, now: Time, devices: &HashMap<String, DeviceUsage>) -> rusqlite::Result<()> {
    let last: i64 = txn.query_row("SELECT last_at FROM traffic_clock WHERE id=1", [], |r| r.get(0)).optional()?.unwrap_or(0);
    let epoch = now.utc().unix();
    let elapsed = epoch - last;
    if last > 0 && elapsed > 0 && elapsed <= 600 {
        let (mut rx, mut upload) = (0u64, 0u64);
        for device in devices.values() {
            rx += device.rx_bytes;
            upload += device.tx_bytes;
            if device.rx_bytes + device.tx_bytes == 0 {
                continue;
            }
            txn.execute(
                "INSERT INTO device_day(address,day,rx_bytes,tx_bytes,last_seen) VALUES(?,?,?,?,?) ON CONFLICT(address,day) DO UPDATE SET rx_bytes=rx_bytes+excluded.rx_bytes, tx_bytes=tx_bytes+excluded.tx_bytes,last_seen=excluded.last_seen",
                params![device.address, now.utc().format("%Y-%m-%d"), device.rx_bytes, device.tx_bytes, epoch],
            )?;
        }
        let peak = (rx + upload) as f64 * 8.0 / elapsed as f64 / 1e6;
        txn.execute(
            "INSERT INTO traffic_hour(hour,rx_bytes,tx_bytes,samples,observed_seconds,peak_mbps) VALUES(?,?,?,1,?,?) ON CONFLICT(hour) DO UPDATE SET rx_bytes=rx_bytes+excluded.rx_bytes,tx_bytes=tx_bytes+excluded.tx_bytes, samples=samples+1,observed_seconds=observed_seconds+excluded.observed_seconds,peak_mbps=MAX(peak_mbps,excluded.peak_mbps)",
            params![now.utc().truncate(Duration::HOUR).unix(), rx, upload, elapsed, peak],
        )?;
    }
    txn.execute("INSERT INTO traffic_clock(id,started_at,last_at) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET last_at=excluded.last_at", params![epoch, epoch])?;
    let cutoff = now.utc().add_date(0, 0, -RECENT_DAYS);
    txn.execute("DELETE FROM traffic_hour WHERE hour < ? OR hour > ?", params![cutoff.unix(), now.utc().add(Duration::HOUR).unix()])?;
    txn.execute("DELETE FROM device_day WHERE day < ? OR day > ?", params![cutoff.format("%Y-%m-%d"), now.utc().format("%Y-%m-%d")])?;
    txn.execute("DELETE FROM device_day WHERE rowid NOT IN (SELECT rowid FROM device_day ORDER BY day DESC, rx_bytes+tx_bytes DESC LIMIT ?)", params![MAX_TRACKED_DEVICES * RECENT_DAYS])?;
    Ok(())
}

fn migrate(conn: &Connection) -> Result<()> {
    conn.execute_batch(
        "
    CREATE TABLE IF NOT EXISTS device_month (
        address TEXT NOT NULL, month TEXT NOT NULL, rx_bytes INTEGER NOT NULL DEFAULT 0, tx_bytes INTEGER NOT NULL DEFAULT 0,
        last_seen INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (address, month));
    CREATE INDEX IF NOT EXISTS idx_device_month_month ON device_month(month);
    CREATE TABLE IF NOT EXISTS device_cursor (
        address TEXT NOT NULL, direction TEXT NOT NULL CHECK(direction IN ('rx','tx')), last_raw INTEGER NOT NULL DEFAULT 0,
        generation INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (address, direction));
    CREATE TABLE IF NOT EXISTS accounting_state (id INTEGER PRIMARY KEY CHECK(id = 1), history_cleared INTEGER NOT NULL DEFAULT 0);
    ",
    )
    .map_err(|e| wrapf("migrate accounting store", e))?;
    // Older databases predate the generation column.
    if let Err(err) = conn.execute("ALTER TABLE device_cursor ADD COLUMN generation INTEGER NOT NULL DEFAULT 0", []) {
        if !err.to_string().contains("duplicate column name") {
            return Err(wrapf("migrate accounting cursors", err));
        }
    }
    conn.execute_batch(
        "
    CREATE TABLE IF NOT EXISTS traffic_clock(id INTEGER PRIMARY KEY CHECK(id=1), started_at INTEGER NOT NULL, last_at INTEGER NOT NULL);
    CREATE TABLE IF NOT EXISTS traffic_hour(hour INTEGER PRIMARY KEY, rx_bytes INTEGER NOT NULL DEFAULT 0, tx_bytes INTEGER NOT NULL DEFAULT 0,
      samples INTEGER NOT NULL DEFAULT 0, observed_seconds INTEGER NOT NULL DEFAULT 0, peak_mbps REAL NOT NULL DEFAULT 0);
    CREATE TABLE IF NOT EXISTS device_day(address TEXT NOT NULL, day TEXT NOT NULL, rx_bytes INTEGER NOT NULL DEFAULT 0,
      tx_bytes INTEGER NOT NULL DEFAULT 0, last_seen INTEGER NOT NULL, PRIMARY KEY(address,day));
    CREATE INDEX IF NOT EXISTS idx_device_day_day ON device_day(day);
    CREATE TABLE IF NOT EXISTS firewall_clock(id INTEGER PRIMARY KEY CHECK(id=1), at INTEGER NOT NULL, generation TEXT NOT NULL, seen INTEGER NOT NULL, accepted INTEGER NOT NULL);
    CREATE TABLE IF NOT EXISTS firewall_minute(minute INTEGER PRIMARY KEY,allowed INTEGER NOT NULL,blocked INTEGER NOT NULL,samples INTEGER NOT NULL);
    ",
    )?;
    super::dns::migrate_dns(conn)
}
