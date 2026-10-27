//! Classifies observed DNS names against public category lists. It never
//! changes DNS answers, firewall policy or the privileged helper.

mod alerts;
mod feeds;

pub use alerts::{Alert, AlertPage, Exception};
pub use feeds::FeedStatus;

use feeds::{candidates, feed_path, open_feed, Feed, SOURCES};
use rusqlite::types::Value as SqlValue;
use rusqlite::{params, params_from_iter, Connection, OptionalExtension};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc::{sync_channel, Receiver, RecvTimeoutError, SyncSender, TrySendError};
use std::sync::{Mutex, MutexGuard, RwLock, RwLockReadGuard, RwLockWriteGuard};
use std::time::{Duration as StdDuration, Instant};

use crate::errorf;
use crate::goish::errors::{Error, Result};
use crate::goish::time::{Duration, Time};

const MAX_ALERTS: i64 = 10000;

#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Lookup {
    pub name: String,
    pub address: String,
    pub count: u32,
    pub at: i64,
}

/// An empty batch only wakes the queue loop for shutdown.
#[derive(Default)]
struct Batch {
    lookups: Vec<Lookup>,
    generation: u64,
}

#[derive(Clone)]
struct Match {
    domain: String,
    category: &'static str,
    severity: &'static str,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Summary {
    pub available: bool,
    pub enabled: bool,
    pub new_count: i64,
    pub total: i64,
    pub last_checked_at: i64,
    pub dropped_lookups: u64,
    pub removed_alerts: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
    pub sources: Vec<FeedStatus>,
}

#[derive(Default)]
struct RunState {
    last_checked: i64,
    process_error: String,
    last_refresh: Option<Instant>,
}

type SettingsFn = Box<dyn Fn() -> Result<(bool, i64)> + Send + Sync>;
type AllowWriteFn = Box<dyn Fn() -> bool + Send + Sync>;

pub struct Service {
    dir: String,
    db: Mutex<Connection>,
    client: ureq::Agent,
    settings: SettingsFn,
    allow_write: AllowWriteFn,
    feeds: RwLock<Vec<Feed>>,
    /// Serializes clear/disable with classification and commit.
    history: Mutex<()>,
    queue: SyncSender<Batch>,
    queue_rx: Mutex<Receiver<Batch>>,
    refresh: crate::shutdown::Wake,
    generation: AtomicU64,
    dropped: AtomicU64,
    state: Mutex<RunState>,
}

crate::sentinel!(ErrListsNotLoaded, "no category lists loaded");

/// Why a batch went unchecked: lookups recorded while the lists are still
/// downloading are a normal start-up gap, not a failure.
fn process_error_message(err: &Error) -> String {
    if crate::goish::errors::is::<ErrListsNotLoaded>(err) {
        "DNS risk lists are still downloading; lookups seen before they finished loading were not checked".into()
    } else {
        "DNS risk checks failed; coverage is incomplete".into()
    }
}

impl Service {
    pub fn open(data_dir: &str, settings: SettingsFn, allow_write: Option<AllowWriteFn>) -> Result<Service> {
        let dir = std::path::Path::new(data_dir).join("dns-risk").to_string_lossy().into_owned();
        std::fs::create_dir_all(&dir)?;
        crate::config::set_mode(&dir, 0o700)?;
        let db = Connection::open(std::path::Path::new(&dir).join("alerts.db"))?;
        db.execute_batch(
            "PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA trusted_schema=OFF; PRAGMA secure_delete=ON; PRAGMA busy_timeout=3000; PRAGMA cache_size=-1024;
             CREATE TABLE IF NOT EXISTS feeds(category TEXT PRIMARY KEY,slot INTEGER NOT NULL,entries INTEGER NOT NULL,updated_at INTEGER NOT NULL,attempted_at INTEGER NOT NULL,etag TEXT NOT NULL,error TEXT NOT NULL);
             CREATE TABLE IF NOT EXISTS alerts(id INTEGER PRIMARY KEY AUTOINCREMENT,day INTEGER NOT NULL,domain TEXT NOT NULL,category TEXT NOT NULL,severity TEXT NOT NULL,first_seen INTEGER NOT NULL,last_seen INTEGER NOT NULL,lookups INTEGER NOT NULL,last_address TEXT NOT NULL,acknowledged_at INTEGER NOT NULL DEFAULT 0,UNIQUE(day,domain,category));
             CREATE INDEX IF NOT EXISTS alerts_recent ON alerts(last_seen DESC,id DESC);
             CREATE TABLE IF NOT EXISTS exceptions(id INTEGER PRIMARY KEY,domain TEXT NOT NULL,category TEXT NOT NULL,UNIQUE(domain,category));
             CREATE TABLE IF NOT EXISTS state(id INTEGER PRIMARY KEY CHECK(id=1),removed_alerts INTEGER NOT NULL DEFAULT 0);
             INSERT OR IGNORE INTO state(id) VALUES(1);",
        )?;
        let mut feeds = Vec::new();
        for source in SOURCES {
            let mut feed = Feed {
                source,
                db: None,
                slot: 0,
                etag: String::new(),
                status: FeedStatus { category: source.id.into(), label: source.label.into(), source: "Block List Project".into(), url: feeds::source_url(&source), ..Default::default() },
            };
            let row: Option<(i64, i64, i64, i64, String, String)> = db
                .query_row("SELECT slot,entries,updated_at,attempted_at,etag,error FROM feeds WHERE category=?", params![source.id], |r| {
                    Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?, r.get(4)?, r.get(5)?))
                })
                .optional()?;
            if let Some((slot, entries, updated_at, attempted_at, etag, error)) = row {
                feed.slot = slot;
                feed.status.entries = entries;
                feed.status.updated_at = updated_at;
                feed.status.attempted_at = attempted_at;
                feed.etag = etag;
                feed.status.error = error;
            }
            if feed.status.entries > 0 && (feed.slot == 0 || feed.slot == 1) {
                match open_feed(&feed_path(&dir, source.id, feed.slot)) {
                    Ok(conn) => feed.db = Some(Mutex::new(conn)),
                    Err(_) => {
                        feed.status.entries = 0;
                        feed.status.updated_at = 0;
                        feed.status.attempted_at = 0;
                        feed.status.error = "Saved category list is unavailable; waiting for download".into();
                        feed.etag.clear();
                    }
                }
            } else {
                feed.slot = 0;
            }
            feeds.push(feed);
        }
        let (queue, queue_rx) = sync_channel(8);
        Ok(Service {
            dir,
            db: Mutex::new(db),
            client: ureq::AgentBuilder::new().timeout(StdDuration::from_secs(600)).redirects(0).build(),
            settings,
            allow_write: allow_write.unwrap_or_else(|| Box::new(|| true)),
            feeds: RwLock::new(feeds),
            history: Mutex::new(()),
            queue,
            queue_rx: Mutex::new(queue_rx),
            refresh: crate::shutdown::Wake::new(),
            generation: AtomicU64::new(0),
            dropped: AtomicU64::new(0),
            state: Mutex::new(RunState::default()),
        })
    }

    pub(crate) fn lock_db(&self) -> MutexGuard<'_, Connection> {
        self.db.lock().unwrap_or_else(|e| e.into_inner())
    }

    fn feeds(&self) -> RwLockReadGuard<'_, Vec<Feed>> {
        self.feeds.read().unwrap_or_else(|e| e.into_inner())
    }

    fn feeds_mut(&self) -> RwLockWriteGuard<'_, Vec<Feed>> {
        self.feeds.write().unwrap_or_else(|e| e.into_inner())
    }

    fn run_state(&self) -> MutexGuard<'_, RunState> {
        self.state.lock().unwrap_or_else(|e| e.into_inner())
    }

    fn enabled_settings(&self) -> Result<(bool, i64)> {
        (self.settings)()
    }

    /// Never holds up DNS accounting or the helper; backpressure is visible.
    pub fn submit(&self, lookups: &[Lookup]) {
        if lookups.is_empty() {
            return;
        }
        let (kept, overflow) = lookups.split_at(lookups.len().min(4096));
        for lookup in overflow {
            self.dropped.fetch_add(lookup.count as u64, Ordering::SeqCst);
        }
        let batch = Batch { lookups: kept.to_vec(), generation: self.generation.load(Ordering::SeqCst) };
        if let Err(TrySendError::Full(batch) | TrySendError::Disconnected(batch)) = self.queue.try_send(batch) {
            for lookup in &batch.lookups {
                self.dropped.fetch_add(lookup.count as u64, Ordering::SeqCst);
            }
        }
    }

    pub fn run(&self, stop: &crate::shutdown::Shutdown) {
        let wake_queue = self.queue.clone();
        stop.on_trigger(move || {
            // A full queue needs no wake-up: the loop checks after each batch.
            let _ = wake_queue.try_send(Batch::default());
        });
        std::thread::scope(|scope| {
            scope.spawn(|| {
                self.update_feeds(stop, false);
                while !stop.is_triggered() {
                    let forced = self.refresh.wait(StdDuration::from_secs(60), stop);
                    if stop.is_triggered() {
                        return;
                    }
                    self.update_feeds(stop, forced);
                }
            });
            let queue = self.queue_rx.lock().unwrap_or_else(|e| e.into_inner());
            let mut next_tick = Instant::now() + StdDuration::from_secs(60);
            while !stop.is_triggered() {
                match queue.recv_timeout(next_tick.saturating_duration_since(Instant::now())) {
                    Ok(batch) if batch.lookups.is_empty() => {}
                    Ok(batch) => {
                        if let Err(err) = self.process(stop, batch) {
                            self.run_state().process_error = process_error_message(&err);
                        }
                    }
                    Err(RecvTimeoutError::Disconnected) => return,
                    Err(RecvTimeoutError::Timeout) if Instant::now() < next_tick => {}
                    Err(RecvTimeoutError::Timeout) => {
                        next_tick = Instant::now() + StdDuration::from_secs(60);
                        let result = match self.enabled_settings() {
                            Err(_) => {
                                self.run_state().process_error = "DNS risk settings could not be read".into();
                                continue;
                            }
                            Ok((false, _)) => self.clear(),
                            Ok((true, days)) => self.prune(Time::now(), days),
                        };
                        if result.is_err() {
                            self.run_state().process_error = "DNS risk history maintenance failed".into();
                        }
                    }
                }
            }
        });
    }

    pub fn request_refresh(&self) -> bool {
        let mut state = self.run_state();
        if state.last_refresh.is_some_and(|t| t.elapsed() < StdDuration::from_secs(300)) {
            return false;
        }
        state.last_refresh = Some(Instant::now());
        self.refresh.notify();
        true
    }

    fn process(&self, stop: &crate::shutdown::Shutdown, batch: Batch) -> Result<()> {
        let _history = self.history.lock().unwrap_or_else(|e| e.into_inner());
        // Failed batches stay visible after a later success clears the error.
        let mut incomplete = false;
        let result = self.process_locked(stop, &batch, &mut incomplete);
        if result.is_err() || incomplete {
            for lookup in &batch.lookups {
                self.dropped.fetch_add(lookup.count as u64, Ordering::SeqCst);
            }
        }
        result
    }

    fn process_locked(&self, _stop: &crate::shutdown::Shutdown, batch: &Batch, incomplete: &mut bool) -> Result<()> {
        if batch.generation != self.generation.load(Ordering::SeqCst) {
            return Ok(());
        }
        let (enabled, days) = self.enabled_settings()?;
        if !enabled {
            return Ok(());
        }
        if !(self.allow_write)() {
            return Err(errorf!("storage pressure"));
        }
        let feeds = self.feeds();
        let loaded = feeds.iter().filter(|f| f.db.is_some()).count();
        if loaded == 0 {
            return Err(ErrListsNotLoaded.into());
        }
        *incomplete = loaded != feeds.len();
        // Match full query names before the coarse site aggregation loses
        // subdomains.
        let mut cache: HashMap<String, Vec<Match>> = HashMap::new();
        for lookup in &batch.lookups {
            if cache.contains_key(&lookup.name) {
                continue;
            }
            let names = candidates(&lookup.name);
            let mut matches = Vec::new();
            if !names.is_empty() {
                let query = format!("SELECT domain FROM domains WHERE domain IN ({}) ORDER BY length(domain) DESC LIMIT 1", vec!["?"; names.len()].join(","));
                let args: Vec<SqlValue> = names.iter().map(|n| SqlValue::from(n.clone())).collect();
                for feed in feeds.iter() {
                    let Some(db) = &feed.db else { continue };
                    let found: Option<String> = db.lock().unwrap_or_else(|e| e.into_inner()).query_row(&query, params_from_iter(args.iter()), |r| r.get(0)).optional()?;
                    if let Some(domain) = found {
                        matches.push(Match { domain, category: feed.source.id, severity: feed.source.severity });
                    }
                }
            }
            cache.insert(lookup.name.clone(), matches);
        }
        drop(feeds);
        // Disabling can race with the slow matching phase; never commit after it.
        if !self.enabled_settings()?.0 {
            return Ok(());
        }
        let now = Time::now();
        let mut db = self.lock_db();
        let tx = db.transaction()?;
        {
            let mut insert = tx.prepare(
                "INSERT INTO alerts(day,domain,category,severity,first_seen,last_seen,lookups,last_address) VALUES(?,?,?,?,?,?,?,?)
                 ON CONFLICT(day,domain,category) DO UPDATE SET first_seen=MIN(first_seen,excluded.first_seen),last_address=CASE WHEN excluded.last_seen>=last_seen THEN excluded.last_address ELSE last_address END,last_seen=MAX(last_seen,excluded.last_seen),lookups=lookups+excluded.lookups",
            )?;
            for lookup in &batch.lookups {
                let at = if lookup.at <= 0 || lookup.at > now.unix() + 300 { now.unix() } else { lookup.at };
                if lookup.count == 0 {
                    continue;
                }
                let day = Time::from_unix(at, 0).truncate(Duration::hours(24)).unix();
                for m in cache.get(&lookup.name).into_iter().flatten() {
                    insert.execute(params![day, m.domain, m.category, m.severity, at, at, lookup.count, lookup.address])?;
                }
            }
        }
        trim_alerts(&tx, now, days)?;
        tx.commit()?;
        let mut state = self.run_state();
        state.last_checked = now.unix();
        state.process_error.clear();
        Ok(())
    }

    fn prune(&self, now: Time, days: i64) -> Result<()> {
        let _history = self.history.lock().unwrap_or_else(|e| e.into_inner());
        let mut db = self.lock_db();
        let tx = db.transaction()?;
        trim_alerts(&tx, now, days)?;
        tx.commit()?;
        Ok(())
    }

    pub fn clear(&self) -> Result<()> {
        let _history = self.history.lock().unwrap_or_else(|e| e.into_inner());
        self.generation.fetch_add(1, Ordering::SeqCst);
        {
            let mut db = self.lock_db();
            let tx = db.transaction()?;
            tx.execute_batch("DELETE FROM alerts; UPDATE state SET removed_alerts=0 WHERE id=1")?;
            tx.commit()?;
        }
        self.dropped.store(0, Ordering::SeqCst);
        let mut state = self.run_state();
        state.last_checked = 0;
        state.process_error.clear();
        Ok(())
    }

    pub fn summary(&self, now: Time) -> Result<Summary> {
        let (enabled, _) = self.enabled_settings()?;
        let mut out = Summary { available: true, enabled, ..Default::default() };
        for feed in self.feeds().iter() {
            let mut status = feed.status.clone();
            status.stale = feed.db.is_none() || now.sub(&Time::from_unix(status.updated_at, 0)) > Duration::hours(48);
            out.sources.push(status);
        }
        if !enabled {
            return Ok(out);
        }
        {
            let state = self.run_state();
            out.last_checked_at = state.last_checked;
            out.error = state.process_error.clone();
        }
        out.dropped_lookups = self.dropped.load(Ordering::SeqCst);
        let db = self.lock_db();
        let (total, new_count): (i64, i64) = db.query_row(
            "SELECT count(*),COALESCE(SUM(CASE WHEN acknowledged_at=0 AND NOT EXISTS(SELECT 1 FROM exceptions e WHERE e.domain=a.domain AND e.category=a.category) THEN 1 ELSE 0 END),0) FROM alerts a",
            [],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )?;
        out.total = total;
        out.new_count = new_count;
        out.removed_alerts = db.query_row("SELECT removed_alerts FROM state WHERE id=1", [], |r| r.get(0))?;
        Ok(out)
    }
}

fn trim_alerts(tx: &rusqlite::Transaction<'_>, now: Time, days: i64) -> rusqlite::Result<()> {
    let days = if !(1..=90).contains(&days) { 30 } else { days };
    let cutoff = now.utc().truncate(Duration::hours(24)).add_date(0, 0, -days + 1).unix();
    tx.execute("DELETE FROM alerts WHERE day<? OR day>?", params![cutoff, now.unix() + 300])?;
    let removed = tx.execute("DELETE FROM alerts WHERE id IN (SELECT id FROM alerts ORDER BY last_seen DESC,id DESC LIMIT -1 OFFSET ?)", params![MAX_ALERTS])?;
    tx.execute("UPDATE state SET removed_alerts=removed_alerts+? WHERE id=1", params![removed as i64])?;
    Ok(())
}

fn valid_category(category: &str) -> bool {
    SOURCES.iter().any(|s| s.id == category)
}

pub fn validate_category(category: &str) -> bool {
    category.is_empty() || valid_category(category)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn candidate_names_stop_at_registrable_domain() {
        assert_eq!(candidates("a.b.Example.COM."), vec!["a.b.example.com", "b.example.com", "example.com"]);
        assert_eq!(candidates("evil.github.io"), vec!["evil.github.io"]);
        assert!(candidates("printer.local").is_empty());
        assert!(candidates("192.168.1.1").is_empty());
        assert!(candidates("co.uk").is_empty());
    }

    #[test]
    fn classifies_lookups_against_a_built_feed() {
        let dir = std::env::temp_dir().join(format!("mr-risk-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let service = Service::open(dir.to_str().unwrap(), Box::new(|| Ok((true, 30))), None).unwrap();
        let path = feed_path(&service.dir, "malware", 1);
        let count = feeds::build_feed(&crate::shutdown::Shutdown::new(), &path, "# list\nbad.example.com\nworse.example.net\n".as_bytes(), &|| true).unwrap();
        assert_eq!(count, 2);
        {
            let mut feeds = service.feeds_mut();
            let feed = feeds.iter_mut().find(|f| f.source.id == "malware").unwrap();
            feed.db = Some(Mutex::new(open_feed(&path).unwrap()));
            feed.slot = 1;
        }
        let stop = crate::shutdown::Shutdown::new();
        let batch = Batch { lookups: vec![Lookup { name: "cdn.bad.example.com".into(), address: "192.168.1.9".into(), count: 2, at: Time::now().unix() }], generation: 0 };
        service.process(&stop, batch).unwrap();
        let page = service.alerts("new", "", 0, 50).unwrap();
        assert_eq!(page.total, 1);
        assert_eq!((page.alerts[0].domain.as_str(), page.alerts[0].category.as_str(), page.alerts[0].lookups), ("bad.example.com", "malware", 2));
        service.ignore(page.alerts[0].id).unwrap();
        assert_eq!(service.summary(Time::now()).unwrap().new_count, 0);
        assert!(crate::goish::errors::is::<crate::config::ErrNoRows>(&service.acknowledge(999).unwrap_err()));
        drop(service);
        std::fs::remove_dir_all(dir).ok();
    }
}
