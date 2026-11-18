use std::sync::{Mutex, MutexGuard};

use super::*;
use crate::goish::time::Duration;

/// One client's lookups of one name as reported by router-applyd.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct DNSLookup {
    pub client: String,
    pub name: String,
    pub count: u32,
    pub last_seen: i64,
}

/// One page drained from router-applyd.
#[derive(Clone, Debug, Default)]
pub struct DNSDrain {
    pub enabled: bool,
    pub lookups: Vec<DNSLookup>,
    pub dropped: u64,
    pub more: bool,
}

/// Converges the privileged helper on the setting and drains its lookups.
pub trait DNSSource: Send + Sync {
    fn drain(&self, deadline: Instant, enabled: bool) -> Result<DNSDrain>;
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DNSSettings {
    pub enabled: bool,
    pub retention_days: i64,
}

impl DNSSettings {
    pub fn validate(&self) -> Result<()> {
        if !(1..=MAX_DNS_RETENTION_DAYS).contains(&self.retention_days) {
            return Err(errorf!("retention_days must be between 1 and {}", MAX_DNS_RETENTION_DAYS));
        }
        Ok(())
    }
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DNSRecentLookup {
    pub at: Time,
    pub address: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub hostname: String,
    pub name: String,
    pub site: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub category: String,
    pub count: u32,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DNSCollectionStatus {
    pub state: String,
    pub last_success: Option<Time>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
    pub dropped_lookups: u64,
}

const DNS_COLLECT_INTERVAL: StdDuration = StdDuration::from_secs(60);
/// Aggregates reach SQLite every five rounds in one transaction; a hard power
/// loss can lose at most that interval.
const DNS_FLUSH_EVERY: u64 = 5;
const DNS_MAX_PENDING_PAIRS: usize = 20000;
const DNS_MAX_DRAIN_PAGES: usize = 64;
const DNS_RECENT_CAPACITY: usize = 2000;
const DNS_PRUNE_INTERVAL: Duration = Duration::HOUR;
/// Enabling can restart dnsmasq, which takes a few seconds.
const DNS_DRAIN_TIMEOUT: StdDuration = StdDuration::from_secs(30);

type LookupObserver = Box<dyn Fn(&[DNSLookup]) + Send + Sync>;
type ClearObserver = Box<dyn Fn() -> Result<()> + Send + Sync>;

#[derive(Default)]
struct Inner {
    status: DNSCollectionStatus,
    write_error: String,
    dropped: u64,
    pending: DNSFlush,
    recent: Vec<DNSRecentLookup>,
    recent_next: usize,
    last_prune: Option<Time>,
}

/// Folds drained lookups into daily per-device site counts. The recent list
/// of full hostnames lives only in memory.
pub struct DNSCollector {
    store: Arc<Store>,
    source: Arc<dyn DNSSource>,
    settings_override: Option<Box<dyn Fn() -> DNSSettings + Send + Sync>>,
    allow_write: Box<dyn Fn() -> bool + Send + Sync>,
    wake: crate::shutdown::Wake,
    inner: Mutex<Inner>,
    operation: Mutex<()>,
    on_lookups: Mutex<Option<LookupObserver>>,
    on_clear: Mutex<Option<ClearObserver>>,
}

impl DNSCollector {
    pub fn new(
        store: Arc<Store>,
        source: Arc<dyn DNSSource>,
        settings: Box<dyn Fn() -> DNSSettings + Send + Sync>,
        allow_write: Option<Box<dyn Fn() -> bool + Send + Sync>>,
    ) -> DNSCollector {
        let mut collector = Self::from_store(store, source, allow_write);
        collector.settings_override = Some(settings);
        collector
    }

    pub fn from_store(store: Arc<Store>, source: Arc<dyn DNSSource>, allow_write: Option<Box<dyn Fn() -> bool + Send + Sync>>) -> DNSCollector {
        DNSCollector {
            store,
            source,
            settings_override: None,
            allow_write: allow_write.unwrap_or_else(|| Box::new(|| true)),
            wake: crate::shutdown::Wake::new(),
            inner: Mutex::new(Inner::default()),
            operation: Mutex::new(()),
            on_lookups: Mutex::new(None),
            on_clear: Mutex::new(None),
        }
    }

    fn inner(&self) -> MutexGuard<'_, Inner> {
        self.inner.lock().unwrap_or_else(|e| e.into_inner())
    }

    fn settings(&self) -> Result<DNSSettings> {
        match &self.settings_override {
            Some(settings) => Ok(settings()),
            None => self.store.dns_settings(),
        }
    }

    /// Configured once, before `run`. The observer only receives validated
    /// lookups and must queue work without delaying collection.
    pub fn set_risk_observer(&self, lookups: LookupObserver, clear: ClearObserver) {
        *self.on_lookups.lock().unwrap_or_else(|e| e.into_inner()) = Some(lookups);
        *self.on_clear.lock().unwrap_or_else(|e| e.into_inner()) = Some(clear);
    }

    pub fn status(&self, now: Time) -> DNSCollectionStatus {
        let inner = self.inner();
        let mut out = inner.status.clone();
        if out.state.is_empty() {
            out.state = "starting".into();
        }
        if out.state == "active" && out.last_success.is_none_or(|t| now.sub(&t) > Duration::minutes(3)) {
            out.state = "stale".into();
        }
        if !inner.write_error.is_empty() {
            out.error = inner.write_error.clone();
            if out.state == "active" {
                out.state = "degraded".into();
            }
        }
        out.dropped_lookups = inner.dropped;
        out
    }

    fn collection_error(&self, message: &str) {
        let mut inner = self.inner();
        inner.status.state = "unavailable".into();
        inner.status.error = message.into();
    }

    /// Runs a collection round now, so a changed setting applies in seconds.
    pub fn wake(&self) {
        self.wake.notify();
    }

    pub fn run(&self, stop: &crate::shutdown::Shutdown) {
        let mut rounds = 0u64;
        while !stop.is_triggered() {
            if let Err(err) = self.round(&mut rounds) {
                self.collection_error("DNS activity is unavailable; collection will retry");
                log::info!("[DNS] could not complete collection: {}", err);
            }
            self.wake.wait(DNS_COLLECT_INTERVAL, stop);
        }
        let _operation = self.operation.lock().unwrap_or_else(|e| e.into_inner());
        if let (Ok(settings), Ok(false)) = (self.settings(), self.store.dns_cleanup_pending())
            && settings.enabled
        {
            self.flush(Time::now(), &settings);
        }
    }

    fn round(&self, rounds: &mut u64) -> Result<()> {
        let _operation = self.operation.lock().unwrap_or_else(|e| e.into_inner());
        let settings = self.settings()?;
        let pending = self.store.dns_cleanup_pending()?;
        if !settings.enabled || pending {
            *rounds = 0;
            self.purge(settings.enabled, pending)?;
            return Ok(());
        }
        let now = Time::now();
        let result = self.collect(now);
        *rounds += 1;
        if rounds.is_multiple_of(DNS_FLUSH_EVERY) || self.pending_pairs() >= DNS_MAX_PENDING_PAIRS {
            self.flush(now, &settings);
        }
        result
    }

    // Both collection and erasure consume the helper's entire bounded queue.
    // One deadline covers all pages so a settings change cannot wait indefinitely.
    fn drain_pages(&self, enabled: bool, mut on_page: impl FnMut(&DNSDrain)) -> Result<()> {
        let deadline = Instant::now() + DNS_DRAIN_TIMEOUT;
        for _ in 0..DNS_MAX_DRAIN_PAGES {
            if Instant::now() >= deadline {
                break;
            }
            let drain = self.source.drain(deadline, enabled)?;
            if drain.enabled != enabled {
                return Err(errorf!("DNS query logging did not reach the requested state"));
            }
            on_page(&drain);
            if !drain.more {
                return Ok(());
            }
        }
        Err(errorf!("DNS collection backlog exceeded the round limit"))
    }

    fn collect(&self, now: Time) -> Result<()> {
        self.drain_pages(true, |drain| {
            add_locked(&mut self.inner(), now, drain);
            if let Some(observer) = self.on_lookups.lock().unwrap_or_else(|e| e.into_inner()).as_ref() {
                observer(&drain.lookups);
            }
        })?;
        let mut inner = self.inner();
        inner.status = DNSCollectionStatus { state: "active".into(), last_success: Some(now.utc()), ..Default::default() };
        inner.pending.observed_hours.insert(now.utc().truncate(Duration::HOUR).unix(), true);
        Ok(())
    }

    pub fn add(&self, now: Time, drain: &DNSDrain) {
        let _operation = self.operation.lock().unwrap_or_else(|e| e.into_inner());
        add_locked(&mut self.inner(), now, drain);
    }

    fn pending_pairs(&self) -> usize {
        self.inner().pending.daily.len()
    }

    fn flush(&self, now: Time, settings: &DNSSettings) {
        let (batch, prune) = {
            let mut inner = self.inner();
            let batch = std::mem::take(&mut inner.pending);
            let prune = inner.last_prune.is_none_or(|last| now.sub(&last) >= DNS_PRUNE_INTERVAL);
            (batch, prune)
        };
        // Statistics are nonessential: under critical disk pressure the batch
        // is discarded rather than competing with configuration writes.
        if !(self.allow_write)() {
            self.inner().write_error = "History writes paused by storage pressure; activity may be missing".into();
            if !batch.is_empty() {
                log::info!("[DNS] skipped writing DNS activity under storage pressure");
            }
            return;
        }
        if !batch.is_empty() {
            let result = self.store.record_dns(now, &batch);
            self.inner().write_error = if result.is_ok() { String::new() } else { "DNS history could not be written; activity may be missing".into() };
            if let Err(err) = result {
                log::info!("[DNS] could not record DNS activity: {}", err);
            }
        }
        if prune {
            match self.store.prune_dns(now, settings.retention_days) {
                Ok(()) => self.inner().last_prune = Some(now),
                Err(err) => log::info!("[DNS] could not prune DNS activity: {}", err),
            }
        }
    }

    fn reset_memory(&self, enabled: bool) {
        let mut inner = self.inner();
        *inner = Inner::default();
        inner.status.state = if enabled { "starting" } else { "disabled" }.into();
    }

    fn notify_clear(&self) -> Result<()> {
        match self.on_clear.lock().unwrap_or_else(|e| e.into_inner()).as_ref() {
            Some(observer) => observer(),
            None => Ok(()),
        }
    }

    fn purge(&self, enabled: bool, pending: bool) -> Result<()> {
        let helper = self.drain_pages(enabled, |_| {});
        self.reset_memory(enabled);
        let risk = self.notify_clear();
        if self.store.has_dns_history()? {
            self.store.clear_dns()?;
        }
        helper?;
        risk?;
        if pending {
            self.store.set_dns_cleanup_pending(false)?;
        }
        Ok(())
    }

    pub fn update_settings(&self, next: &DNSSettings) -> Result<bool> {
        next.validate()?;
        let _operation = self.operation.lock().unwrap_or_else(|e| e.into_inner());
        let previous = self.store.dns_settings()?;
        if next.enabled && self.store.dns_cleanup_pending()? {
            self.purge(previous.enabled, true)?;
        }
        self.store.set_dns_settings(next)?;
        if next.retention_days != previous.retention_days {
            self.inner().last_prune = None;
        }
        self.wake();
        if !next.enabled {
            // The disabled setting has committed. Cleanup failures remain
            // durable and are retried before the next enable.
            if let Err(err) = self.purge(false, true) {
                self.collection_error("DNS activity is disabled; history cleanup is pending");
                log::info!("[DNS] disabled history cleanup will retry: {}", err);
            }
        }
        Ok(previous != *next)
    }

    /// Deletes stored and in-memory DNS activity at the operator's request.
    pub fn clear(&self) -> Result<()> {
        let _operation = self.operation.lock().unwrap_or_else(|e| e.into_inner());
        let settings = self.settings()?;
        self.store.set_dns_cleanup_pending(true)?;
        self.wake();
        self.purge(settings.enabled, true)
    }

    /// The newest in-memory lookups, optionally for one device.
    pub fn recent(&self, device: &str, limit: usize) -> Vec<DNSRecentLookup> {
        let mut out: Vec<DNSRecentLookup> = self.inner().recent.iter().filter(|e| device.is_empty() || e.address == device).cloned().collect();
        out.sort_by(|a, b| b.at.cmp(&a.at));
        if limit > 0 {
            out.truncate(limit);
        }
        out
    }
}

fn add_locked(inner: &mut Inner, now: Time, drain: &DNSDrain) {
    if drain.dropped > 0 {
        *inner.pending.unitemized.entry(utc_day(now)).or_default() += drain.dropped;
        inner.dropped += drain.dropped;
    }
    for lookup in &drain.lookups {
        if lookup.count == 0 || lookup.client.is_empty() || lookup.name.is_empty() {
            continue;
        }
        let seen = if lookup.last_seen <= 0 || lookup.last_seen > now.unix() + 300 { now.unix() } else { lookup.last_seen };
        let at = Time::from_unix(seen, 0);
        let day = utc_day(at);
        let site = site_of(&lookup.name);
        *inner.pending.hourly.entry(DNSHourlyKey { hour: at.truncate(Duration::HOUR).unix(), address: lookup.client.clone() }).or_default() +=
            lookup.count as u64;
        let key = DNSDailyKey { day, address: lookup.client.clone(), site: site.clone() };
        let existing = inner.pending.daily.get(&key).copied();
        if existing.is_none() && inner.pending.daily.len() >= DNS_MAX_PENDING_PAIRS {
            *inner.pending.unitemized.entry(day).or_default() += lookup.count as u64;
        } else {
            let mut count = existing.unwrap_or_default();
            if existing.is_none() || seen < count.first_seen {
                count.first_seen = seen;
            }
            if seen > count.last_seen {
                count.last_seen = seen;
            }
            count.lookups += lookup.count as u64;
            inner.pending.daily.insert(key, count);
        }
        remember(inner, DNSRecentLookup { at, address: lookup.client.clone(), name: lookup.name.clone(), site, count: lookup.count, ..Default::default() });
    }
}

fn remember(inner: &mut Inner, entry: DNSRecentLookup) {
    if inner.recent.len() < DNS_RECENT_CAPACITY {
        inner.recent.push(entry);
        return;
    }
    let next = inner.recent_next;
    inner.recent[next] = entry;
    inner.recent_next = (next + 1) % DNS_RECENT_CAPACITY;
}

#[cfg(test)]
mod tests {
    use super::*;

    struct EmptySource;
    impl DNSSource for EmptySource {
        fn drain(&self, _: Instant, enabled: bool) -> Result<DNSDrain> {
            Ok(DNSDrain { enabled, ..Default::default() })
        }
    }

    #[test]
    fn clearing_while_a_collection_page_is_in_flight_leaves_no_old_lookups() {
        use std::sync::mpsc::{Receiver, SyncSender, sync_channel};
        struct SlowPage(Mutex<Option<(SyncSender<()>, Receiver<()>)>>);
        impl DNSSource for SlowPage {
            fn drain(&self, _: Instant, enabled: bool) -> Result<DNSDrain> {
                let gate = self.0.lock().unwrap().take();
                let mut page = DNSDrain { enabled, ..Default::default() };
                if let Some((started, release)) = gate {
                    started.send(()).unwrap();
                    release.recv_timeout(StdDuration::from_secs(5)).unwrap();
                    page.lookups.push(DNSLookup { client: "192.0.2.1".into(), name: "earlier.example".into(), count: 1, last_seen: 0 });
                }
                Ok(page)
            }
        }
        let (started, in_flight) = sync_channel(0);
        let (release, ready) = sync_channel(0);
        let (clear_started, clearing) = sync_channel(0);
        let store = Arc::new(Store::in_memory());
        store.set_dns_settings(&DNSSettings { enabled: true, retention_days: 30 }).unwrap();
        let collector = DNSCollector::from_store(store.clone(), Arc::new(SlowPage(Mutex::new(Some((started, ready))))), None);
        std::thread::scope(|scope| {
            let read = scope.spawn(|| collector.round(&mut 4));
            in_flight.recv_timeout(StdDuration::from_secs(5)).unwrap();
            let clear = scope.spawn(|| {
                clear_started.send(()).unwrap();
                collector.clear()
            });
            clearing.recv_timeout(StdDuration::from_secs(5)).unwrap();
            release.send(()).unwrap();
            read.join().unwrap().unwrap();
            clear.join().unwrap().unwrap();
        });
        assert!(collector.recent("", 10).is_empty());
        assert!(!store.has_dns_history().unwrap());
    }

    #[test]
    fn a_failed_retention_prune_is_retried_on_the_next_flush() {
        let store = Arc::new(Store::in_memory());
        let collector = DNSCollector::from_store(store.clone(), Arc::new(EmptySource), None);
        store.lock().execute_batch("INSERT INTO dns_unitemized VALUES(0, 7); CREATE TEMP TRIGGER fail_prune BEFORE DELETE ON dns_unitemized BEGIN SELECT RAISE(ABORT, 'interrupted'); END").unwrap();
        let now = Time::date_utc(2026, 3, 1);
        let settings = DNSSettings { enabled: true, retention_days: 1 };
        collector.flush(now, &settings);
        assert!(store.has_dns_history().unwrap());
        store.lock().execute_batch("DROP TRIGGER fail_prune").unwrap();
        collector.flush(now.add(Duration::minutes(5)), &settings);
        assert!(!store.has_dns_history().unwrap());
    }

    #[test]
    fn unreadable_settings_do_not_clear_history_or_toggle_the_helper() {
        struct UnexpectedSource;
        impl DNSSource for UnexpectedSource {
            fn drain(&self, _: Instant, _: bool) -> Result<DNSDrain> {
                panic!("settings are unknown")
            }
        }
        let store = Arc::new(Store::in_memory());
        let settings = DNSSettings { enabled: true, retention_days: 30 };
        store.set_dns_settings(&settings).unwrap();
        let collector = DNSCollector::from_store(store.clone(), Arc::new(UnexpectedSource), None);
        let now = Time::date_utc(2026, 3, 1).add(Duration::hours(12));
        collector.add(
            now,
            &DNSDrain { lookups: vec![DNSLookup { client: "192.0.2.1".into(), name: "kept.example".into(), count: 2, last_seen: 0 }], ..Default::default() },
        );
        collector.flush(now, &settings);
        store.lock().execute_batch("ALTER TABLE dns_settings RENAME TO unreadable_settings").unwrap();
        assert!(collector.round(&mut 0).is_err());
        store.lock().execute_batch("ALTER TABLE unreadable_settings RENAME TO dns_settings").unwrap();
        assert_eq!(collector.recent("", 5).len(), 1);
        assert_eq!(store.dns_activity(now.add(Duration::SECOND), &DNSQuery { period: "today".into(), ..Default::default() }).unwrap().total_lookups, 2);
    }

    #[test]
    fn an_older_disabled_database_finishes_cleanup_before_reenabling() {
        let store = Arc::new(Store::in_memory());
        {
            let conn = store.lock();
            conn.execute_batch("DROP TABLE dns_cleanup; INSERT INTO dns_settings VALUES(1, 0, 30); INSERT INTO dns_observed VALUES(3600)").unwrap();
            super::super::dns::migrate_dns(&conn).unwrap();
        }
        assert!(store.dns_cleanup_pending().unwrap());
        let settings = DNSSettings { enabled: true, retention_days: 30 };
        assert!(store.set_dns_settings(&settings).is_err());
        let collector = DNSCollector::from_store(store.clone(), Arc::new(EmptySource), None);
        collector.update_settings(&settings).unwrap();
        assert!(!store.has_dns_history().unwrap());
        assert!(store.dns_settings().unwrap().enabled);
    }

    #[test]
    fn interrupted_disable_stays_off_across_restart_until_alert_cleanup_succeeds() {
        use std::sync::atomic::{AtomicBool, Ordering};
        let dir = std::env::temp_dir().join(format!("mr-dns-purge-{}-{}", std::process::id(), Time::now().unix_nano()));
        let settings = DNSSettings { enabled: true, retention_days: 30 };
        let off = DNSSettings { enabled: false, ..settings.clone() };
        {
            let store = Arc::new(Store::open(dir.to_str().unwrap()).unwrap());
            let collector = DNSCollector::from_store(store.clone(), Arc::new(EmptySource), None);
            collector.update_settings(&settings).unwrap();
            collector.round(&mut 4).unwrap();
            assert!(store.has_dns_history().unwrap());
            collector.set_risk_observer(Box::new(|_| {}), Box::new(|| Err(errorf!("temporary write failure"))));
            assert!(collector.update_settings(&off).unwrap());
            assert!(!store.has_dns_history().unwrap());
            assert!(!store.dns_settings().unwrap().enabled);
        }
        let store = Arc::new(Store::open(dir.to_str().unwrap()).unwrap());
        let collector = DNSCollector::from_store(store.clone(), Arc::new(EmptySource), None);
        let unavailable = Arc::new(AtomicBool::new(true));
        let flag = unavailable.clone();
        collector.set_risk_observer(Box::new(|_| {}), Box::new(move || if flag.load(Ordering::SeqCst) { Err(errorf!("unavailable")) } else { Ok(()) }));
        assert!(collector.update_settings(&settings).is_err());
        assert!(!store.dns_settings().unwrap().enabled);
        assert!(store.dns_cleanup_pending().unwrap());
        unavailable.store(false, Ordering::SeqCst);
        assert!(collector.update_settings(&settings).unwrap());
        assert!(store.dns_settings().unwrap().enabled);
        assert!(!store.dns_cleanup_pending().unwrap());
        drop(collector);
        drop(store);
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn clearing_history_discards_every_page_already_waiting_at_the_helper() {
        use std::collections::VecDeque;
        struct Queued(Mutex<VecDeque<DNSDrain>>);
        impl DNSSource for Queued {
            fn drain(&self, _: Instant, enabled: bool) -> Result<DNSDrain> {
                Ok(self.0.lock().unwrap().pop_front().unwrap_or(DNSDrain { enabled, ..Default::default() }))
            }
        }
        let store = Arc::new(Store::in_memory());
        let settings = DNSSettings { enabled: true, retention_days: 30 };
        store.set_dns_settings(&settings).unwrap();
        let page = |name: &str, more| DNSDrain {
            enabled: true,
            more,
            lookups: vec![DNSLookup { client: "192.0.2.1".into(), name: name.into(), count: 1, last_seen: 0 }],
            ..Default::default()
        };
        let source = Arc::new(Queued(Mutex::new(VecDeque::from([page("first.example", true), page("second.example", false)]))));
        let collector = DNSCollector::from_store(store.clone(), source.clone(), None);
        collector.clear().unwrap();
        source.0.lock().unwrap().push_back(page("new.example", false));
        collector.round(&mut 0).unwrap();
        let recent = collector.recent("", 10);
        assert_eq!(recent.iter().map(|r| r.name.as_str()).collect::<Vec<_>>(), ["new.example"]);
    }

    #[test]
    fn site_grouping_and_dns_round_trip() {
        assert_eq!(site_of("rr3---sn-abc.googlevideo.com"), "googlevideo.com");
        assert_eq!(site_of("news.bbc.co.uk"), "bbc.co.uk");
        assert_eq!(site_of("example.com"), "example.com");

        let dir = std::env::temp_dir().join(format!("mr-acct-{}", std::process::id()));
        let store = Arc::new(Store::open(dir.to_str().unwrap()).unwrap());
        let collector = DNSCollector::new(store.clone(), Arc::new(EmptySource), Box::new(|| DNSSettings { enabled: true, retention_days: 30 }), None);
        let now = Time::now_utc();
        collector.add(
            now,
            &DNSDrain {
                enabled: true,
                lookups: vec![DNSLookup { client: "192.168.1.5".into(), name: "www.example.co.uk".into(), count: 3, last_seen: now.unix() }],
                ..Default::default()
            },
        );
        collector.flush(now, &DNSSettings { enabled: true, retention_days: 30 });
        let activity = store.dns_activity(now, &DNSQuery { period: "today".into(), ..Default::default() }).unwrap();
        assert_eq!(activity.total_lookups, 3);
        assert_eq!(activity.sites[0].site, "example.co.uk");
        assert_eq!(activity.points.last().map(|p| (p.lookups, p.observed)), Some((3, true)));
        assert_eq!(collector.recent("", 10).len(), 1);
        collector.clear().unwrap();
        assert!(!store.has_dns_history().unwrap());

        store.record(now, &[Counter { address: "192.168.1.5".into(), bytes: 100 }], &[], 13, 1).unwrap();
        store.record(now.add(Duration::secs(60)), &[Counter { address: "192.168.1.5".into(), bytes: 300 }], &[], 13, 1).unwrap();
        let months = store.months(3).unwrap();
        assert_eq!(months[0].total_bytes, 300);
        drop(collector);
        drop(store);
        std::fs::remove_dir_all(dir).ok();
    }
}
