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
    generation: u64,
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
    settings: Box<dyn Fn() -> DNSSettings + Send + Sync>,
    allow_write: Box<dyn Fn() -> bool + Send + Sync>,
    wake: crate::shutdown::Wake,
    inner: Mutex<Inner>,
    write: Mutex<()>,
    on_lookups: Mutex<Option<LookupObserver>>,
    on_clear: Mutex<Option<ClearObserver>>,
}

impl DNSCollector {
    pub fn new(store: Arc<Store>, source: Arc<dyn DNSSource>, settings: Box<dyn Fn() -> DNSSettings + Send + Sync>, allow_write: Option<Box<dyn Fn() -> bool + Send + Sync>>) -> DNSCollector {
        DNSCollector {
            store,
            source,
            settings,
            allow_write: allow_write.unwrap_or_else(|| Box::new(|| true)),
            wake: crate::shutdown::Wake::new(),
            inner: Mutex::new(Inner::default()),
            write: Mutex::new(()),
            on_lookups: Mutex::new(None),
            on_clear: Mutex::new(None),
        }
    }

    fn inner(&self) -> MutexGuard<'_, Inner> {
        self.inner.lock().unwrap_or_else(|e| e.into_inner())
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
        loop {
            let settings = (self.settings)();
            if settings.enabled {
                self.collect(stop, Time::now());
                rounds += 1;
                if rounds % DNS_FLUSH_EVERY == 0 || self.pending_pairs() >= DNS_MAX_PENDING_PAIRS {
                    self.flush(Time::now(), &settings);
                }
            } else {
                // Tell the helper every round, so query logging is switched
                // off even if a previous attempt or routerd itself failed.
                if let Err(err) = self.source.drain(Instant::now() + DNS_DRAIN_TIMEOUT, false) {
                    if !stop.is_triggered() {
                        log::info!("[DNS] could not disable query logging: {}", err);
                    }
                }
                self.disable();
                rounds = 0;
            }
            self.wake.wait(DNS_COLLECT_INTERVAL, stop);
            if stop.is_triggered() {
                let settings = (self.settings)();
                if settings.enabled {
                    self.flush(Time::now(), &settings);
                }
                return;
            }
        }
    }

    fn collect(&self, stop: &crate::shutdown::Shutdown, now: Time) {
        let generation = self.inner().generation;
        for _ in 0..DNS_MAX_DRAIN_PAGES {
            let drain = match self.source.drain(Instant::now() + DNS_DRAIN_TIMEOUT, true) {
                Ok(drain) => drain,
                Err(err) => {
                    self.collection_error("DNS collection failed; activity may be missing");
                    if !stop.is_triggered() {
                        log::info!("[DNS] could not collect DNS activity: {}", err);
                    }
                    return;
                }
            };
            if !drain.enabled {
                self.collection_error("DNS query logging is not active");
                return;
            }
            let mut inner = self.inner();
            if generation != inner.generation {
                return;
            }
            add_locked(&mut inner, now, &drain);
            if let Some(observer) = self.on_lookups.lock().unwrap_or_else(|e| e.into_inner()).as_ref() {
                observer(&drain.lookups);
            }
            if !drain.more {
                inner.status = DNSCollectionStatus { state: "active".into(), last_success: Some(now.utc()), ..Default::default() };
                inner.pending.observed_hours.insert(now.utc().truncate(Duration::HOUR).unix(), true);
                return;
            }
        }
        self.collection_error("DNS collection backlog exceeded the round limit");
    }

    pub fn add(&self, now: Time, drain: &DNSDrain) {
        add_locked(&mut self.inner(), now, drain);
    }

    fn pending_pairs(&self) -> usize {
        self.inner().pending.daily.len()
    }

    fn flush(&self, now: Time, settings: &DNSSettings) {
        let _write = self.write.lock().unwrap_or_else(|e| e.into_inner());
        let (batch, prune) = {
            let mut inner = self.inner();
            let batch = std::mem::take(&mut inner.pending);
            let prune = inner.last_prune.is_none_or(|last| now.sub(&last) >= DNS_PRUNE_INTERVAL);
            if prune {
                inner.last_prune = Some(now);
            }
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
            if let Err(err) = self.store.prune_dns(now, settings.retention_days) {
                log::info!("[DNS] could not prune DNS activity: {}", err);
            }
        }
    }

    fn reset_memory(&self, status: Option<DNSCollectionStatus>) {
        let mut inner = self.inner();
        inner.generation += 1;
        inner.pending = DNSFlush::default();
        if let Some(status) = status {
            inner.status = status;
        }
        inner.write_error.clear();
        inner.dropped = 0;
        inner.recent.clear();
        inner.recent_next = 0;
    }

    fn notify_clear(&self) -> Result<()> {
        match self.on_clear.lock().unwrap_or_else(|e| e.into_inner()).as_ref() {
            Some(observer) => observer(),
            None => Ok(()),
        }
    }

    /// Drops everything held in memory and deletes stored history. It runs
    /// every round while the feature is off, so a failed delete is retried.
    fn disable(&self) {
        let _write = self.write.lock().unwrap_or_else(|e| e.into_inner());
        self.reset_memory(Some(DNSCollectionStatus { state: "disabled".into(), ..Default::default() }));
        if let Err(err) = self.notify_clear() {
            log::info!("[DNS] could not clear risk alerts: {}", err);
        }
        match self.store.has_dns_history() {
            Err(err) => log::info!("[DNS] could not read DNS activity state: {}", err),
            Ok(true) => {
                if let Err(err) = self.store.clear_dns() {
                    log::info!("[DNS] could not delete DNS activity after disable: {}", err);
                }
            }
            Ok(false) => {}
        }
    }

    /// Deletes stored and in-memory DNS activity at the operator's request.
    pub fn clear(&self) -> Result<()> {
        let _write = self.write.lock().unwrap_or_else(|e| e.into_inner());
        self.reset_memory(None);
        self.notify_clear()?;
        self.store.clear_dns()
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
        *inner.pending.hourly.entry(DNSHourlyKey { hour: at.truncate(Duration::HOUR).unix(), address: lookup.client.clone() }).or_default() += lookup.count as u64;
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

    #[test]
    fn site_grouping_and_dns_round_trip() {
        assert_eq!(site_of("rr3---sn-abc.googlevideo.com"), "googlevideo.com");
        assert_eq!(site_of("news.bbc.co.uk"), "bbc.co.uk");
        assert_eq!(site_of("example.com"), "example.com");

        let dir = std::env::temp_dir().join(format!("mr-acct-{}", std::process::id()));
        let store = Arc::new(Store::open(dir.to_str().unwrap()).unwrap());
        struct Never;
        impl DNSSource for Never {
            fn drain(&self, _: Instant, enabled: bool) -> Result<DNSDrain> {
                Ok(DNSDrain { enabled, ..Default::default() })
            }
        }
        let collector = DNSCollector::new(store.clone(), Arc::new(Never), Box::new(|| DNSSettings { enabled: true, retention_days: 30 }), None);
        let now = Time::now_utc();
        collector.add(now, &DNSDrain { enabled: true, lookups: vec![DNSLookup { client: "192.168.1.5".into(), name: "www.example.co.uk".into(), count: 3, last_seen: now.unix() }], ..Default::default() });
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
