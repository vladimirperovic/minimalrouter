use rusqlite::{Connection, OptionalExtension, params};
use std::collections::HashMap;
use std::sync::{Mutex, MutexGuard};

use super::*;
use crate::goish::errors::wrapf;
use crate::goish::json;

pub(crate) const RETENTION_PERIOD: Duration = Duration::hours(7 * 24);
const MAX_SAMPLE_ROWS: i64 = 41000;
const MAX_EVENT_ROWS: i64 = 2048;
/// 400 days keeps a full year plus slack in a trivially small table.
const HOURLY_RETENTION: Duration = Duration::hours(400 * 24);
const MAX_HOURLY_ROWS: i64 = 12000;
/// Just inside the raw retention, so a straddling request uses the rollup.
const RAW_HISTORY_HORIZON: Duration = Duration::hours(6 * 24);
const INSIGHT_RETENTION: Duration = Duration::hours(400 * 24);
/// Samples stamped ahead of the clock (a wrong RTC corrected by NTP) are
/// ignored instead of sorting as the newest evidence for hours.
pub(crate) const FUTURE_SAMPLE_TOLERANCE: Duration = Duration::MINUTE;

pub(crate) fn not_after() -> i64 {
    Time::now_utc().add(FUTURE_SAMPLE_TOLERANCE).unix()
}

pub struct Store {
    conn: Mutex<Connection>,
}

fn state_severity_case(column: &str) -> String {
    format!("(CASE {} WHEN 'offline' THEN 4 WHEN 'flapping' THEN 3 WHEN 'degraded' THEN 2 WHEN 'healthy' THEN 1 ELSE 0 END)", column)
}

impl Store {
    pub fn open(data_dir: &str) -> Result<Store> {
        std::fs::create_dir_all(data_dir).map_err(|e| wrapf("create gateway data directory", e))?;
        crate::config::set_mode(data_dir, 0o700).map_err(|e| wrapf("secure gateway data directory", e))?;
        let path = std::path::Path::new(data_dir).join("gateway-quality.db");
        let conn = Connection::open(&path).map_err(|e| wrapf("open gateway history", e))?;
        conn.execute_batch("PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA trusted_schema=OFF; PRAGMA secure_delete=ON; PRAGMA busy_timeout=3000; PRAGMA cache_size=-1000;")
            .map_err(|e| wrapf("initialize gateway history", e))?;
        crate::config::set_mode(&path.to_string_lossy(), 0o600).map_err(|e| wrapf("secure gateway history", e))?;
        migrate(&conn)?;
        Ok(Store { conn: Mutex::new(conn) })
    }

    pub(crate) fn lock(&self) -> MutexGuard<'_, Connection> {
        self.conn.lock().unwrap_or_else(|e| e.into_inner())
    }

    fn storage_directory(&self) -> Result<String> {
        let conn = self.lock();
        let mut stmt = conn.prepare("PRAGMA database_list")?;
        let mut rows = stmt.query([])?;
        while let Some(row) = rows.next()? {
            let name: String = row.get(1)?;
            let path: String = row.get(2)?;
            if name == "main" && !path.is_empty() {
                return Ok(std::path::Path::new(&path).parent().map(|p| p.to_string_lossy().into_owned()).unwrap_or_default());
            }
        }
        Err(errorf!("gateway database path is unavailable"))
    }

    pub fn storage_status(&self) -> crate::storage::Status {
        match self.storage_directory() {
            Ok(dir) => crate::storage::inspect(&dir),
            Err(_) => crate::storage::evaluate(0, 0),
        }
    }

    pub(crate) fn nonessential_writes_allowed(&self) -> bool {
        let status = self.storage_status();
        !status.available || status.nonessential_writes_allowed
    }

    pub(crate) fn require_durable_write(&self) -> Result<()> {
        let status = self.storage_status();
        if status.available && !status.durable_writes_allowed {
            return Err(crate::goish::errors::wrap(crate::storage::ErrCriticalPressure, format!("{}: gateway settings", crate::storage::ErrCriticalPressure)));
        }
        Ok(())
    }

    pub(crate) fn checkpoint_if_pressured(&self) {
        let status = self.storage_status();
        if status.available && status.level != crate::storage::PressureLevel::Normal {
            let _ = self.lock().query_row("PRAGMA wal_checkpoint(PASSIVE)", [], |_| Ok(()));
        }
    }

    pub fn settings(&self) -> Result<Settings> {
        let (enabled, targets_json, interval): (i64, String, i64) = self
            .lock()
            .query_row("SELECT enabled, targets_json, interval_seconds FROM gateway_settings WHERE id = 1", [], |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?)))
            .map_err(|e| wrapf("load gateway settings", e))?;
        let targets: List<String> = json::unmarshal(targets_json.as_bytes()).map_err(|e| wrapf("decode gateway settings", e))?;
        let settings = Settings { enabled: enabled != 0, targets, interval_seconds: interval };
        settings.validate().map_err(|e| wrapf("stored gateway settings are invalid", e))?;
        Ok(settings)
    }

    pub fn save_settings(&self, settings: &Settings) -> Result<()> {
        settings.validate()?;
        let targets = json::marshal_string(&settings.targets).map_err(|e| wrapf("encode gateway settings", e))?;
        self.lock()
            .execute(
                "UPDATE gateway_settings SET enabled = ?, targets_json = ?, interval_seconds = ? WHERE id = 1",
                params![settings.enabled as i64, targets, settings.interval_seconds],
            )
            .map_err(|e| wrapf("save gateway settings", e))?;
        Ok(())
    }

    pub fn save_sample(&self, sample: &Sample) -> Result<()> {
        let targets = json::marshal_string(&sample.targets).map_err(|e| wrapf("encode gateway targets", e))?;
        let peer = match &sample.peer_probe {
            Some(p) => json::marshal_string(p).map_err(|e| wrapf("encode PPPoE peer probe", e))?,
            None => String::new(),
        };
        let mut conn = self.lock();
        let tx = conn.transaction()?;
        let ts = sample.timestamp.unix();
        tx.execute(
            "INSERT INTO gateway_samples (timestamp, state, link_connected, local_ip, peer_ip, targets_json, peer_probe_json, latency_ms, jitter_ms, packet_loss_percent, pppoe_uptime_seconds) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
            params![ts, sample.state, sample.link.connected as i64, sample.link.local_ip, sample.link.peer_ip, targets, peer, sample.latency_ms, sample.jitter_ms, sample.packet_loss_percent, sample.pppoe_uptime],
        )
        .map_err(|e| wrapf("save gateway sample", e))?;
        let hour_start = ts - ts.rem_euclid(3600);
        tx.execute(
            &format!(
                "INSERT INTO gateway_hourly (hour_start, samples, latency_sum, jitter_sum, loss_sum, worst_state, uptime_max) VALUES (?, 1, ?, ?, ?, ?, ?)
                 ON CONFLICT(hour_start) DO UPDATE SET samples = samples + 1, latency_sum = latency_sum + excluded.latency_sum,
                 jitter_sum = jitter_sum + excluded.jitter_sum, loss_sum = loss_sum + excluded.loss_sum, uptime_max = MAX(uptime_max, excluded.uptime_max),
                 worst_state = CASE WHEN {} > {} THEN excluded.worst_state ELSE worst_state END",
                state_severity_case("excluded.worst_state"),
                state_severity_case("worst_state")
            ),
            params![hour_start, sample.latency_ms, sample.jitter_ms, sample.packet_loss_percent, sample.state, sample.pppoe_uptime],
        )
        .map_err(|e| wrapf("roll up gateway sample", e))?;
        tx.execute("DELETE FROM gateway_hourly WHERE hour_start < ?", params![sample.timestamp.add(Duration(-HOURLY_RETENTION.0)).unix()])
            .map_err(|e| wrapf("prune gateway rollup", e))?;
        tx.execute(
            "DELETE FROM gateway_hourly WHERE hour_start NOT IN (SELECT hour_start FROM gateway_hourly ORDER BY hour_start DESC LIMIT ?)",
            params![MAX_HOURLY_ROWS],
        )
        .map_err(|e| wrapf("bound gateway rollup", e))?;
        tx.execute("DELETE FROM gateway_samples WHERE timestamp < ?", params![sample.timestamp.add(Duration(-RETENTION_PERIOD.0)).unix()])
            .map_err(|e| wrapf("prune old gateway samples", e))?;
        tx.execute("DELETE FROM gateway_samples WHERE id NOT IN (SELECT id FROM gateway_samples ORDER BY id DESC LIMIT ?)", params![MAX_SAMPLE_ROWS])
            .map_err(|e| wrapf("bound gateway samples", e))?;
        tx.commit()?;
        Ok(())
    }

    pub fn add_reconnect(&self, at: Time) -> Result<()> {
        let mut conn = self.lock();
        let tx = conn.transaction()?;
        tx.execute("INSERT INTO gateway_events(timestamp, type) VALUES (?, 'reconnect')", params![at.unix()]).map_err(|e| wrapf("save reconnect event", e))?;
        tx.execute("DELETE FROM gateway_events WHERE timestamp < ?", params![at.add(Duration(-RETENTION_PERIOD.0)).unix()])
            .map_err(|e| wrapf("prune reconnect events", e))?;
        tx.execute("DELETE FROM gateway_events WHERE id NOT IN (SELECT id FROM gateway_events ORDER BY id DESC LIMIT ?)", params![MAX_EVENT_ROWS])
            .map_err(|e| wrapf("bound reconnect events", e))?;
        tx.commit()?;
        Ok(())
    }

    pub fn count_reconnects(&self, since: Time) -> Result<i64> {
        Ok(self.lock().query_row(
            "SELECT COUNT(*) FROM gateway_events WHERE type = 'reconnect' AND timestamp >= ? AND timestamp <= ?",
            params![since.unix(), not_after()],
            |r| r.get(0),
        )?)
    }

    /// The newest sample by insertion order, which survives a backward clock step.
    pub fn latest_sample(&self) -> Result<Option<Sample>> {
        let row = self
            .lock()
            .query_row(
                "SELECT timestamp, state, link_connected, local_ip, peer_ip, targets_json, peer_probe_json, latency_ms, jitter_ms, packet_loss_percent, pppoe_uptime_seconds FROM gateway_samples ORDER BY id DESC LIMIT 1",
                [],
                |r| {
                    Ok((
                        r.get::<_, i64>(0)?,
                        r.get::<_, String>(1)?,
                        r.get::<_, i64>(2)?,
                        r.get::<_, String>(3)?,
                        r.get::<_, String>(4)?,
                        r.get::<_, String>(5)?,
                        r.get::<_, String>(6)?,
                        r.get::<_, f64>(7)?,
                        r.get::<_, f64>(8)?,
                        r.get::<_, f64>(9)?,
                        r.get::<_, i64>(10)?,
                    ))
                },
            )
            .optional()?;
        let Some((ts, state, connected, local_ip, peer_ip, targets_json, peer_json, latency, jitter, loss, uptime)) = row else {
            return Ok(None);
        };
        let targets = json::unmarshal(targets_json.as_bytes()).map_err(|e| wrapf("decode stored gateway targets", e))?;
        let peer_probe =
            if peer_json.is_empty() { None } else { Some(json::unmarshal(peer_json.as_bytes()).map_err(|e| wrapf("decode stored PPPoE peer probe", e))?) };
        Ok(Some(Sample {
            timestamp: Time::from_unix(ts, 0),
            state,
            link: LinkStatus { connected: connected != 0, interface: "ppp0".into(), local_ip, peer_ip },
            targets,
            peer_probe,
            latency_ms: latency,
            jitter_ms: jitter,
            packet_loss_percent: loss,
            pppoe_uptime: uptime,
        }))
    }

    fn hourly_history(&self, since: Time, max_points: usize) -> Result<Vec<HistoryPoint>> {
        let conn = self.lock();
        let mut stmt = conn.prepare("SELECT hour_start, samples, latency_sum, jitter_sum, loss_sum, worst_state, uptime_max FROM gateway_hourly WHERE hour_start >= ? AND hour_start <= ? ORDER BY hour_start ASC")?;
        let since_hour = since.unix() - since.unix().rem_euclid(3600);
        let rows = stmt.query_map(params![since_hour, not_after()], |r| {
            Ok((
                r.get::<_, i64>(0)?,
                r.get::<_, i64>(1)?,
                r.get::<_, f64>(2)?,
                r.get::<_, f64>(3)?,
                r.get::<_, f64>(4)?,
                r.get::<_, String>(5)?,
                r.get::<_, i64>(6)?,
            ))
        })?;
        let mut raw = Vec::new();
        for row in rows {
            let (hour, samples, latency, jitter, loss, worst, uptime) = row?;
            if samples <= 0 {
                continue;
            }
            let n = samples as f64;
            raw.push(HistoryPoint {
                timestamp: Time::from_unix(hour, 0),
                state: worst,
                latency_ms: latency / n,
                jitter_ms: jitter / n,
                packet_loss_percent: loss / n,
                pppoe_uptime: uptime,
            });
        }
        Ok(aggregate_history(raw, since, max_points))
    }

    pub fn history(&self, since: Time, max_points: usize) -> Result<Vec<HistoryPoint>> {
        let max_points = max_points.clamp(1, 720);
        if Time::now().sub(&since) > RAW_HISTORY_HORIZON {
            return self.hourly_history(since, max_points);
        }
        let conn = self.lock();
        let mut stmt = conn.prepare("SELECT timestamp, state, latency_ms, jitter_ms, packet_loss_percent, pppoe_uptime_seconds FROM gateway_samples WHERE timestamp >= ? AND timestamp <= ? ORDER BY timestamp ASC")?;
        let rows = stmt.query_map(params![since.unix(), not_after()], |r| {
            Ok(HistoryPoint {
                timestamp: Time::from_unix(r.get(0)?, 0),
                state: r.get(1)?,
                latency_ms: r.get(2)?,
                jitter_ms: r.get(3)?,
                packet_loss_percent: r.get(4)?,
                pppoe_uptime: r.get(5)?,
            })
        })?;
        let raw: Vec<HistoryPoint> = rows.collect::<rusqlite::Result<_>>()?;
        Ok(aggregate_history(raw, since, max_points))
    }

    /// Installs the derived-history tables and trigger; existing raw history is
    /// backfilled once. Must run before the monitor starts collecting.
    pub fn ensure_insights_schema(&self) -> Result<()> {
        let conn = self.lock();
        conn.execute_batch(INSIGHTS_SCHEMA).map_err(|e| wrapf("migrate gateway insights", e))?;
        conn.execute_batch(
            "INSERT OR IGNORE INTO gateway_availability_hourly(hour_start, samples, up_samples, outage_starts)
             SELECT hour_start, COUNT(*), SUM(link_connected), SUM(outage_start) FROM (
               SELECT (timestamp / 3600) * 3600 AS hour_start, link_connected,
               CASE WHEN link_connected = 0 AND COALESCE(LAG(link_connected) OVER (ORDER BY id), 1) != 0 THEN 1 ELSE 0 END AS outage_start
               FROM gateway_samples) GROUP BY hour_start;",
        )
        .map_err(|e| wrapf("backfill gateway availability", e))?;
        conn.execute_batch(
            "INSERT OR IGNORE INTO gateway_public_ip_events(timestamp, old_ip, new_ip)
             SELECT timestamp, previous_ip, local_ip FROM (
               SELECT timestamp, local_ip, LAG(local_ip) OVER (ORDER BY id) AS previous_ip FROM gateway_samples WHERE local_ip != '')
             WHERE previous_ip IS NOT NULL AND previous_ip != local_ip;",
        )
        .map_err(|e| wrapf("backfill public IP history", e))?;
        Ok(())
    }

    pub fn insights(&self, window: Duration, ip_limit: i64) -> Result<Insights> {
        if window.0 <= 0 || window > INSIGHT_RETENTION {
            return Err(errorf!("gateway insight window is out of range"));
        }
        let ip_limit = if ip_limit <= 0 { 20 } else { ip_limit.min(100) };
        let since = Time::now_utc().add(Duration(-window.0)).unix();
        let mut result = Insights { window_days: window.0 / Duration::hours(24).0, ..Default::default() };
        let conn = self.lock();
        let (hours, samples, up, outages, first, last): (i64, i64, i64, i64, Option<i64>, Option<i64>) = conn
            .query_row(
                "SELECT COUNT(*), COALESCE(SUM(samples), 0), COALESCE(SUM(up_samples), 0), COALESCE(SUM(outage_starts), 0), MIN(hour_start), MAX(hour_start) FROM gateway_availability_hourly WHERE hour_start >= ? AND hour_start <= ?",
                params![since, not_after()],
                |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?, r.get(4)?, r.get(5)?)),
            )
            .map_err(|e| wrapf("read gateway availability", e))?;
        result.sampled_hours = hours;
        result.samples = samples;
        result.up_samples = up;
        result.outages = outages;
        if samples > 0 {
            result.available = true;
            result.uptime_percent = up as f64 * 100.0 / samples as f64;
        }
        result.first_sample = first.map(|h| Time::from_unix(h, 0));
        result.last_sample = last.map(|h| Time::from_unix(h + 3600, 0));
        let mut stmt = conn
            .prepare("SELECT timestamp, old_ip, new_ip FROM gateway_public_ip_events ORDER BY timestamp DESC LIMIT ?")
            .map_err(|e| wrapf("read public IP history", e))?;
        let rows = stmt
            .query_map(params![ip_limit], |r| Ok(PublicIPChange { timestamp: Time::from_unix(r.get(0)?, 0), old_ip: r.get(1)?, new_ip: r.get(2)? }))
            .map_err(|e| wrapf("read public IP history", e))?;
        for row in rows {
            result.public_ip_changes.push(row.map_err(|e| wrapf("scan public IP history", e))?);
        }
        Ok(result)
    }

    /// (timestamp, link_connected, state) rows within the outage horizon.
    pub(crate) fn outage_rows(&self, from: i64, to: i64) -> Result<Vec<(i64, bool, String)>> {
        let conn = self.lock();
        let mut stmt =
            conn.prepare("SELECT timestamp, link_connected, state FROM gateway_samples WHERE timestamp >= ? AND timestamp <= ? ORDER BY timestamp, id")?;
        let rows = stmt.query_map(params![from, to], |r| Ok((r.get::<_, i64>(0)?, r.get::<_, i64>(1)? != 0, r.get::<_, String>(2)?)))?;
        Ok(rows.collect::<rusqlite::Result<_>>()?)
    }
}

const INSIGHTS_SCHEMA: &str = "
CREATE TABLE IF NOT EXISTS gateway_availability_hourly (
    hour_start INTEGER PRIMARY KEY, samples INTEGER NOT NULL DEFAULT 0,
    up_samples INTEGER NOT NULL DEFAULT 0, outage_starts INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS gateway_public_ip_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp INTEGER NOT NULL, old_ip TEXT NOT NULL, new_ip TEXT NOT NULL,
    UNIQUE(timestamp, old_ip, new_ip));
CREATE INDEX IF NOT EXISTS idx_gateway_public_ip_events_timestamp ON gateway_public_ip_events(timestamp);
DROP TRIGGER IF EXISTS gateway_insights_after_sample;
CREATE TRIGGER gateway_insights_after_sample AFTER INSERT ON gateway_samples
BEGIN
    INSERT INTO gateway_availability_hourly(hour_start, samples, up_samples, outage_starts)
    VALUES ((NEW.timestamp / 3600) * 3600, 1,
        CASE WHEN NEW.link_connected != 0 THEN 1 ELSE 0 END,
        CASE WHEN NEW.link_connected = 0 AND COALESCE(
            (SELECT link_connected FROM gateway_samples WHERE id < NEW.id ORDER BY id DESC LIMIT 1), 1) != 0 THEN 1 ELSE 0 END)
    ON CONFLICT(hour_start) DO UPDATE SET samples = samples + 1,
        up_samples = up_samples + excluded.up_samples, outage_starts = outage_starts + excluded.outage_starts;
    INSERT OR IGNORE INTO gateway_public_ip_events(timestamp, old_ip, new_ip)
    SELECT NEW.timestamp, previous.prev_ip, NEW.local_ip FROM (
        SELECT local_ip AS prev_ip FROM gateway_samples WHERE id < NEW.id AND local_ip != '' ORDER BY id DESC LIMIT 1) AS previous
    WHERE NEW.local_ip != '' AND previous.prev_ip != NEW.local_ip;
    DELETE FROM gateway_availability_hourly WHERE hour_start < NEW.timestamp - 34560000;
    DELETE FROM gateway_public_ip_events WHERE timestamp < NEW.timestamp - 34560000;
    DELETE FROM gateway_public_ip_events WHERE id NOT IN (SELECT id FROM gateway_public_ip_events ORDER BY id DESC LIMIT 1024);
END;
";

fn migrate(conn: &Connection) -> Result<()> {
    conn.execute_batch(
        "
    CREATE TABLE IF NOT EXISTS gateway_samples (
        id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp INTEGER NOT NULL, state TEXT NOT NULL, link_connected INTEGER NOT NULL,
        local_ip TEXT NOT NULL DEFAULT '', peer_ip TEXT NOT NULL DEFAULT '', targets_json TEXT NOT NULL,
        peer_probe_json TEXT NOT NULL DEFAULT '', latency_ms REAL NOT NULL DEFAULT 0, jitter_ms REAL NOT NULL DEFAULT 0,
        packet_loss_percent REAL NOT NULL DEFAULT 100, pppoe_uptime_seconds INTEGER NOT NULL DEFAULT 0);
    CREATE INDEX IF NOT EXISTS idx_gateway_samples_timestamp ON gateway_samples(timestamp);
    CREATE TABLE IF NOT EXISTS gateway_events (
        id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp INTEGER NOT NULL, type TEXT NOT NULL CHECK(type IN ('reconnect')));
    CREATE INDEX IF NOT EXISTS idx_gateway_events_timestamp ON gateway_events(timestamp);
    CREATE TABLE IF NOT EXISTS gateway_hourly (
        hour_start INTEGER PRIMARY KEY, samples INTEGER NOT NULL DEFAULT 0, latency_sum REAL NOT NULL DEFAULT 0,
        jitter_sum REAL NOT NULL DEFAULT 0, loss_sum REAL NOT NULL DEFAULT 0, worst_state TEXT NOT NULL DEFAULT 'unknown',
        uptime_max INTEGER NOT NULL DEFAULT 0);
    CREATE TABLE IF NOT EXISTS gateway_settings (
        id INTEGER PRIMARY KEY CHECK(id = 1), enabled INTEGER NOT NULL, targets_json TEXT NOT NULL, interval_seconds INTEGER NOT NULL);
    ",
    )
    .map_err(|e| wrapf("migrate gateway history", e))?;
    let defaults = default_settings();
    let targets = json::marshal_string(&defaults.targets).map_err(|e| wrapf("encode default gateway settings", e))?;
    conn.execute(
        "INSERT OR IGNORE INTO gateway_settings (id, enabled, targets_json, interval_seconds) VALUES (1, ?, ?, ?)",
        params![defaults.enabled as i64, targets, defaults.interval_seconds],
    )
    .map_err(|e| wrapf("initialize gateway settings", e))?;
    Ok(())
}

pub(crate) fn aggregate_history(raw: Vec<HistoryPoint>, since: Time, max_points: usize) -> Vec<HistoryPoint> {
    if raw.len() <= max_points {
        return raw;
    }
    let mut window = raw.last().map(|p| p.timestamp.sub(&since)).unwrap_or_default();
    if window.0 <= 0 {
        window = Duration::HOUR;
    }
    let step = Duration((window.0 / max_points as i64).max(Duration::SECOND.0));
    struct Bucket {
        count: usize,
        latency: f64,
        jitter: f64,
        loss: f64,
        uptime: i64,
        state: String,
        timestamp: Time,
    }
    let mut buckets: HashMap<i64, Bucket> = HashMap::new();
    let mut order = Vec::new();
    let base = since.utc();
    for point in raw {
        let key = (point.timestamp.sub(&base).0 / step.0).min(max_points as i64 - 1);
        let item = buckets.entry(key).or_insert_with(|| {
            order.push(key);
            Bucket { count: 0, latency: 0.0, jitter: 0.0, loss: 0.0, uptime: 0, state: point.state.clone(), timestamp: base.add(Duration(key * step.0)) }
        });
        item.count += 1;
        item.latency += point.latency_ms;
        item.jitter += point.jitter_ms;
        item.loss += point.packet_loss_percent;
        item.uptime = point.pppoe_uptime;
        if state_severity(&point.state) > state_severity(&item.state) {
            item.state = point.state.clone();
        }
    }
    order
        .into_iter()
        .map(|key| {
            let b = &buckets[&key];
            let n = b.count as f64;
            HistoryPoint {
                timestamp: b.timestamp,
                state: b.state.clone(),
                latency_ms: b.latency / n,
                jitter_ms: b.jitter / n,
                packet_loss_percent: b.loss / n,
                pppoe_uptime: b.uptime,
            }
        })
        .collect()
}
