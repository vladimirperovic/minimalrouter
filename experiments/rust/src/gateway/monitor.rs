use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::time::Duration as StdDuration;

use super::store::{FUTURE_SAMPLE_TOLERANCE, RETENTION_PERIOD};
use super::*;

/// One bounded ICMP sample; implementations must be read-only.
pub trait Prober: Send + Sync {
    fn probe(&self, target: &str) -> TargetResult;
}

pub trait LinkReader: Send + Sync {
    fn read(&self) -> LinkStatus;
}

pub struct CommandProber {
    pub ping_path: String,
    pub count: i64,
    pub timeout: StdDuration,
}

impl CommandProber {
    pub fn new() -> CommandProber {
        CommandProber { ping_path: find_fixed_binary("ping"), count: 4, timeout: StdDuration::from_secs(6) }
    }
}

impl Default for CommandProber {
    fn default() -> Self {
        CommandProber::new()
    }
}

impl Prober for CommandProber {
    fn probe(&self, target: &str) -> TargetResult {
        let target = target.trim();
        let mut result = TargetResult { target: target.into(), packet_loss_percent: 100.0, ..Default::default() };
        if net::parse_ip(target).and_then(|ip| ip.to4()).is_none() || target.contains(':') {
            result.error = "target is not a dotted-quad IPv4 address".into();
            return result;
        }
        if self.ping_path.is_empty() {
            result.error = "ping utility unavailable".into();
            return result;
        }
        let count = if (1..=10).contains(&self.count) { self.count } else { 4 };
        let timeout = if self.timeout.is_zero() || self.timeout > StdDuration::from_secs(15) { StdDuration::from_secs(6) } else { self.timeout };
        // Arguments and destination are fully bounded literals.
        let outcome = run_with_timeout(&self.ping_path, &["-n", "-c", &count.to_string(), "-W", "1", target], timeout);
        let (output, success, timed_out) = outcome;
        if let Ok(parsed) = parse_ping_output(target, &output) {
            result = parsed;
        }
        if timed_out {
            result.error = "probe timed out".into();
        } else if !success && result.packets_received == 0 {
            result.error = "target did not reply".into();
        }
        result
    }
}

/// Combined output, success flag and whether the deadline killed the child.
fn run_with_timeout(program: &str, args: &[&str], timeout: StdDuration) -> (String, bool, bool) {
    use std::io::Read;
    let Ok(mut child) = std::process::Command::new(program).args(args).stdout(std::process::Stdio::piped()).stderr(std::process::Stdio::piped()).spawn() else {
        return (String::new(), false, false);
    };
    let mut stdout = child.stdout.take().expect("piped");
    let mut stderr = child.stderr.take().expect("piped");
    let out = std::thread::spawn(move || {
        let mut b = Vec::new();
        let _ = stdout.read_to_end(&mut b);
        b
    });
    let err = std::thread::spawn(move || {
        let mut b = Vec::new();
        let _ = stderr.read_to_end(&mut b);
        b
    });
    let started = std::time::Instant::now();
    let (success, timed_out) = loop {
        match child.try_wait() {
            Ok(Some(status)) => break (status.success(), false),
            Ok(None) if started.elapsed() < timeout => std::thread::sleep(StdDuration::from_millis(10)),
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                break (false, true);
            }
        }
    };
    let mut bytes = out.join().unwrap_or_default();
    bytes.extend(err.join().unwrap_or_default());
    (String::from_utf8_lossy(&bytes).into_owned(), success, timed_out)
}

pub struct SysfsLinkReader {
    pub interface: String,
    pub ip_path: String,
}

impl SysfsLinkReader {
    pub fn new(interface: &str) -> SysfsLinkReader {
        SysfsLinkReader { interface: if interface.is_empty() { "ppp0".into() } else { interface.into() }, ip_path: find_fixed_binary("ip") }
    }
}

impl LinkReader for SysfsLinkReader {
    fn read(&self) -> LinkStatus {
        let mut status = LinkStatus { interface: self.interface.clone(), ..Default::default() };
        let flags = std::fs::read_to_string(format!("/sys/class/net/{}/flags", self.interface)).ok().and_then(|f| u32::from_str_radix(f.trim().trim_start_matches("0x"), 16).ok());
        if !flags.is_some_and(|f| f & 0x1 != 0) {
            return status;
        }
        status.connected = true;
        if !self.ip_path.is_empty() {
            let (output, _, _) = run_with_timeout(&self.ip_path, &["-4", "-o", "addr", "show", "dev", &self.interface], StdDuration::from_secs(2));
            let (local, peer) = parse_peer_output(&output);
            status.local_ip = local;
            status.peer_ip = peer;
        }
        status
    }
}

struct Live {
    latest: Summary,
    last_connected: bool,
    initialized: bool,
    connected_since: Option<Time>,
}

pub struct Monitor {
    store: Arc<Store>,
    prober: Arc<dyn Prober>,
    link: Arc<dyn LinkReader>,
    collect: Mutex<()>,
    manual: Mutex<Option<Time>>,
    live: Mutex<Live>,
    wake: (Mutex<bool>, Condvar),
}

/// Long probe intervals must not starve recovery of fresh link evidence.
fn collection_interval(configured: Duration, summary: Option<&Summary>) -> Duration {
    let disconnected = summary.is_none_or(|s| s.enabled && !s.link.connected);
    if configured > Duration::secs(30) && disconnected { Duration::secs(30) } else { configured }
}

impl Monitor {
    pub fn new(store: Arc<Store>, prober: Arc<dyn Prober>, link: Arc<dyn LinkReader>) -> Arc<Monitor> {
        Arc::new(Monitor {
            store,
            prober,
            link,
            collect: Mutex::new(()),
            manual: Mutex::new(None),
            live: Mutex::new(Live { latest: Summary { state: STATE_UNKNOWN.into(), ..Default::default() }, last_connected: false, initialized: false, connected_since: None }),
            wake: (Mutex::new(false), Condvar::new()),
        })
    }

    pub fn with_defaults(store: Arc<Store>) -> Arc<Monitor> {
        Monitor::new(store, Arc::new(CommandProber::new()), Arc::new(SysfsLinkReader::new("ppp0")))
    }

    fn live(&self) -> std::sync::MutexGuard<'_, Live> {
        self.live.lock().unwrap_or_else(|e| e.into_inner())
    }

    fn signal_wake(&self) {
        *self.wake.0.lock().unwrap_or_else(|e| e.into_inner()) = true;
        self.wake.1.notify_all();
    }

    pub fn run(&self, stop: &AtomicBool) {
        while !stop.load(Ordering::SeqCst) {
            let interval = self.store.settings().map(|s| s.interval()).unwrap_or(Duration::secs(30));
            let collected = self.collect();
            if collected.is_err() {
                self.live().latest.available = false;
            }
            let interval = collection_interval(interval, collected.as_ref().ok());
            let guard = self.wake.0.lock().unwrap_or_else(|e| e.into_inner());
            let (mut woken, _) = self.wake.1.wait_timeout_while(guard, interval.to_std(), |w| !*w && !stop.load(Ordering::SeqCst)).unwrap_or_else(|e| e.into_inner());
            *woken = false;
        }
    }

    pub fn collect(&self) -> Result<Summary> {
        let _collect = self.collect.lock().unwrap_or_else(|e| e.into_inner());
        let settings = self.store.settings()?;
        let now = Time::now_utc();
        if !settings.enabled {
            let summary = Summary { available: true, enabled: false, state: STATE_UNKNOWN.into(), timestamp: now, ..Default::default() };
            let mut live = self.live();
            live.latest = summary.clone();
            live.initialized = false;
            live.last_connected = false;
            live.connected_since = None;
            return Ok(summary);
        }
        let link = self.link.read();
        let connected_since = {
            let mut live = self.live();
            if !live.initialized {
                let previous = self.store.latest_sample();
                let recent_limit = (settings.interval() * 3).max(Duration::minutes(2));
                match &previous {
                    Ok(Some(prev)) if prev.link.connected && link.connected && now.sub(&prev.timestamp) <= recent_limit => {
                        live.connected_since = Some(prev.timestamp.add(Duration::secs(-prev.pppoe_uptime)));
                    }
                    _ if link.connected => {
                        live.connected_since = Some(now);
                        if let Ok(Some(prev)) = &previous {
                            if !prev.link.connected && now.sub(&prev.timestamp) <= recent_limit && self.store.nonessential_writes_allowed() {
                                self.store.add_reconnect(now)?;
                            }
                        }
                    }
                    _ => {}
                }
                live.last_connected = link.connected;
                live.initialized = true;
            } else if link.connected && !live.last_connected {
                live.connected_since = Some(now);
                if self.store.nonessential_writes_allowed() {
                    self.store.add_reconnect(now)?;
                }
            } else if !link.connected {
                live.connected_since = None;
            }
            live.last_connected = link.connected;
            live.connected_since
        };
        let mut targets: Vec<TargetResult> = settings.targets.iter().map(|t| TargetResult { target: t.clone(), packet_loss_percent: 100.0, ..Default::default() }).collect();
        let mut peer_probe = None;
        if link.connected {
            std::thread::scope(|scope| {
                let handles: Vec<_> = settings.targets.iter().map(|t| scope.spawn(move || self.prober.probe(t))).collect();
                for (slot, handle) in targets.iter_mut().zip(handles) {
                    if let Ok(result) = handle.join() {
                        *slot = result;
                    }
                }
            });
            if !link.peer_ip.is_empty() {
                peer_probe = Some(self.prober.probe(&link.peer_ip));
            }
        }
        let reconnects_1h = self.store.count_reconnects(now.add(Duration::hours(-1)))?;
        let reconnects_24h = self.store.count_reconnects(now.add(Duration::hours(-24)))?;
        let (latency, jitter, loss) = aggregate_targets(&targets);
        let uptime = match connected_since {
            Some(since) if link.connected => (now.sub(&since).0 / 1_000_000_000).max(0),
            _ => 0,
        };
        let state = classify(&link, &targets, reconnects_1h);
        let sample = Sample {
            timestamp: now,
            state: state.clone(),
            link: link.clone(),
            targets: targets.clone().into(),
            peer_probe: peer_probe.clone(),
            latency_ms: latency,
            jitter_ms: jitter,
            packet_loss_percent: loss,
            pppoe_uptime: uptime,
        };
        if self.store.nonessential_writes_allowed() {
            self.store.save_sample(&sample)?;
            self.store.checkpoint_if_pressured();
        }
        let summary = Summary {
            available: true,
            enabled: true,
            state,
            timestamp: now,
            link,
            targets: targets.into(),
            peer_probe,
            latency_ms: latency,
            jitter_ms: jitter,
            packet_loss_percent: loss,
            pppoe_uptime: uptime,
            reconnects_1h,
            reconnects_24h,
        };
        self.live().latest = summary.clone();
        Ok(summary)
    }

    /// Serialises callers and bounds manual traffic, including diagnostics.
    pub fn probe_now(&self) -> Result<Summary> {
        let Ok(mut last) = self.manual.try_lock() else {
            return Err(errorf!("a gateway check is already running"));
        };
        let settings = self.settings()?;
        if !settings.enabled {
            return Err(errorf!("enable gateway monitoring before checking"));
        }
        let now = Time::now();
        if last.is_some_and(|t| now.sub(&t) < Duration::secs(10)) {
            return Err(errorf!("wait 10 seconds between gateway checks"));
        }
        *last = Some(now);
        match self.collect() {
            Err(err) => {
                self.live().latest.available = false;
                Err(err)
            }
            Ok(result) => {
                if result.enabled && !result.link.connected {
                    self.signal_wake();
                }
                Ok(result)
            }
        }
    }

    pub fn settings(&self) -> Result<Settings> {
        self.store.settings()
    }

    pub fn update_settings(&self, settings: &Settings) -> Result<()> {
        let _collect = self.collect.lock().unwrap_or_else(|e| e.into_inner());
        self.store.require_durable_write()?;
        self.store.save_settings(settings)?;
        self.store.checkpoint_if_pressured();
        if !settings.enabled {
            let mut live = self.live();
            live.latest = Summary { available: true, enabled: false, state: STATE_UNKNOWN.into(), timestamp: Time::now_utc(), ..Default::default() };
            live.initialized = false;
            live.last_connected = false;
            live.connected_since = None;
        }
        self.signal_wake();
        Ok(())
    }

    pub fn summary(&self) -> Summary {
        self.live().latest.clone()
    }

    pub fn history(&self, window: Duration, max_points: usize) -> Result<Vec<HistoryPoint>> {
        self.store.history(Time::now_utc().add(Duration(-window.0)), max_points)
    }

    pub fn insights(&self, window: Duration, ip_limit: i64) -> Result<Insights> {
        self.store.insights(window, ip_limit)
    }

    pub fn store(&self) -> &Arc<Store> {
        &self.store
    }

    /// Most recent first, at most 20 episodes from the bounded raw store.
    pub fn outages(&self) -> Result<Vec<Outage>> {
        let now = Time::now_utc();
        let rows = self.store.outage_rows(now.add(Duration(-RETENTION_PERIOD.0)).unix(), now.add(FUTURE_SAMPLE_TOLERANCE).unix())?;
        let mut events = Vec::new();
        let mut current: Option<Outage> = None;
        let mut previous: Option<Time> = None;
        let finish = |current: &mut Option<Outage>, events: &mut Vec<Outage>, outcome: &str, end: Option<Time>| {
            if let Some(mut outage) = current.take() {
                outage.outcome = outcome.into();
                outage.ended_at = end;
                let until = end.unwrap_or(outage.last_observed_at);
                outage.duration_seconds = until.sub(&outage.started_at).0 / 1_000_000_000;
                events.push(outage);
            }
        };
        for (seconds, link_up, state) in rows {
            let at = Time::from_unix(seconds, 0);
            let gap = previous.is_some_and(|p| at.sub(&p) > Duration::minutes(2));
            if gap {
                finish(&mut current, &mut events, "unknown", None);
            }
            if link_up && state != STATE_OFFLINE {
                finish(&mut current, &mut events, "recovered", Some(at));
            } else {
                let outage = current.get_or_insert_with(|| Outage { started_at: at, partial: previous.is_none() || gap, cause: OUTAGE_CAUSE_INTERNET.into(), ..Default::default() });
                if !link_up {
                    outage.cause = OUTAGE_CAUSE_PPPOE_LINK.into();
                }
                outage.last_observed_at = at;
            }
            previous = Some(at);
        }
        let summary = self.summary();
        let still_down = !summary.link.connected || summary.state == STATE_OFFLINE;
        let stale = previous.is_none_or(|p| now.sub(&p) > Duration::minutes(2));
        let state = if stale || !summary.enabled || !summary.available || !still_down { "unknown" } else { "ongoing" };
        finish(&mut current, &mut events, state, None);
        events.reverse();
        events.truncate(20);
        Ok(events)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    struct FixedProbe;
    impl Prober for FixedProbe {
        fn probe(&self, target: &str) -> TargetResult {
            TargetResult { target: target.into(), reachable: true, packets_sent: 4, packets_received: 4, latency_ms: 12.0, ..Default::default() }
        }
    }
    struct FixedLink(bool);
    impl LinkReader for FixedLink {
        fn read(&self) -> LinkStatus {
            LinkStatus { connected: self.0, interface: "ppp0".into(), local_ip: "203.0.113.10".into(), ..Default::default() }
        }
    }

    #[test]
    fn collect_persists_samples_and_reports_history() {
        let dir = std::env::temp_dir().join(format!("mr-gw-{}", std::process::id()));
        let store = Arc::new(Store::open(dir.to_str().unwrap()).unwrap());
        store.ensure_insights_schema().unwrap();
        let monitor = Monitor::new(store.clone(), Arc::new(FixedProbe), Arc::new(FixedLink(true)));
        let summary = monitor.collect().unwrap();
        assert_eq!(summary.state, STATE_HEALTHY);
        assert_eq!(summary.latency_ms, 12.0);
        assert_eq!(store.latest_sample().unwrap().unwrap().state, STATE_HEALTHY);
        assert_eq!(monitor.history(Duration::HOUR, 60).unwrap().len(), 1);
        let insights = monitor.insights(Duration::hours(24 * 30), 20).unwrap();
        assert_eq!((insights.samples, insights.up_samples), (1, 1));
        assert!(monitor.outages().unwrap().is_empty());
        drop(monitor);
        drop(store);
        std::fs::remove_dir_all(dir).ok();
    }
}
