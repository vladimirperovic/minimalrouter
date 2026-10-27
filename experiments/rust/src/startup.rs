//! Boot timeline capture: readiness milestones and a bounded resource series
//! for the first ten minutes after a Linux boot.

use serde::{Deserialize, Serialize};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration as StdDuration;

use crate::errorf;
use crate::goish::errors::Result;
use crate::goish::json::{self, List};
use crate::goish::time::{Duration, Time};

pub const WINDOW: Duration = Duration::minutes(10);
pub const CHECK_INTERVAL: Duration = Duration::SECOND;
pub const SAMPLE_INTERVAL: Duration = Duration::SECOND;
pub const MAX_BOOTS: usize = 5;
/// Bounds how often the resource series reaches flash: persisting every
/// sample made bytes written grow with the square of the sample count.
const SAMPLE_PERSIST_INTERVAL: Duration = Duration::secs(15);

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Event {
    pub offset_seconds: i64,
    pub kind: String,
    pub message: String,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Sample {
    pub offset_seconds: i64,
    pub cpu_percent: f64,
    pub memory_used_mb: f64,
    pub memory_total_mb: f64,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Readiness {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub management_seconds: Option<i64>,
    #[serde(rename = "pppoe_seconds", skip_serializing_if = "Option::is_none")]
    pub pppoe_seconds: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub dns_seconds: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub internet_seconds: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub wireguard_seconds: Option<i64>,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Boot {
    pub id: String,
    pub started_at: Time,
    pub completed: bool,
    pub readiness: Readiness,
    pub events: List<Event>,
    /// Omitted only when nil, which happens solely in [`BootSummary`] where Go
    /// shadows the series with an omitempty field.
    #[serde(skip_serializing_if = "List::is_nil")]
    pub samples: List<Sample>,
    #[serde(skip_serializing_if = "List::is_empty")]
    pub expected: List<String>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub completion_reason: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub finished_seconds: Option<i64>,
    pub updated_at: Time,
}

struct State {
    boot: Boot,
    last_cpu: u64,
    last_total: u64,
    last_sample_persist: Option<Time>,
}

pub struct Recorder {
    dir: std::path::PathBuf,
    started: Time,
    state: Mutex<State>,
}

fn valid_boot_id(value: &str) -> bool {
    !value.is_empty() && value.len() <= 128 && value.chars().all(|r| r.is_ascii_alphanumeric() || matches!(r, '-' | '_' | '.'))
}

/// Anchors the timeline to the Linux boot, so a routerd restart continues
/// the same capture instead of creating a fake new boot.
fn kernel_boot_identity(now: Time) -> (String, Time) {
    if let (Ok(id), Ok(uptime)) = (std::fs::read_to_string("/proc/sys/kernel/random/boot_id"), std::fs::read_to_string("/proc/uptime")) {
        let id = id.trim().to_string();
        if valid_boot_id(&id) {
            if let Some(seconds) = uptime.split_whitespace().next().and_then(|f| f.parse::<f64>().ok()).filter(|s| *s >= 0.0) {
                return (id, now.add(Duration(-(seconds * 1e9) as i64)).utc());
            }
        }
    }
    (now.0.format("%Y%m%dT%H%M%S%.9fZ").to_string(), now)
}

impl Recorder {
    pub fn new(data_dir: &str) -> Result<Arc<Recorder>> {
        let dir = std::path::Path::new(data_dir).join("startup");
        std::fs::create_dir_all(&dir)?;
        crate::config::set_mode(&dir.to_string_lossy(), 0o700)?;
        let now = Time::now_utc();
        let (boot_id, mut started_at) = kernel_boot_identity(now);
        let mut boot = Boot { id: boot_id.clone(), started_at, events: List::empty(), samples: List::empty(), ..Default::default() };
        let mut reused = false;
        if let Ok(data) = std::fs::read(dir.join(format!("{}.json", boot_id))) {
            if let Ok(existing) = json::unmarshal::<Boot>(&data) {
                if existing.id == boot_id && !existing.started_at.is_zero() {
                    started_at = existing.started_at;
                    boot = existing;
                    reused = true;
                }
            }
        }
        if reused && boot.completion_reason == "interrupted" && Time::now().sub(&started_at) < WINDOW {
            boot.completed = false;
            boot.completion_reason.clear();
            boot.finished_seconds = None;
        }
        let completed = boot.completed;
        let recorder = Arc::new(Recorder { dir: dir.clone(), started: started_at, state: Mutex::new(State { boot, last_cpu: 0, last_total: 0, last_sample_persist: None }) });
        if reused && completed {
            return Ok(recorder);
        }
        recorder.event("routerd", if reused { "Management process restarted" } else { "Management process started" });
        recorder.persist()?;
        // The set of boot files changes only when a boot starts.
        let _ = prune(&dir);
        Ok(recorder)
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|e| e.into_inner())
    }

    fn offset_seconds(&self) -> i64 {
        (Time::now().sub(&self.started).0 / 1_000_000_000).max(0)
    }

    pub fn event(&self, kind: &str, message: &str) {
        let mut st = self.lock();
        if st.boot.completed {
            return;
        }
        let offset = self.offset_seconds();
        st.boot.events.push(Event { offset_seconds: offset, kind: kind.into(), message: message.into() });
        let _ = self.persist_locked(&mut st);
    }

    pub fn ready(&self, kind: &str) {
        let mut st = self.lock();
        if st.boot.completed {
            return;
        }
        let seconds = self.offset_seconds();
        let r = &mut st.boot.readiness;
        let slot = match kind {
            "management" => &mut r.management_seconds,
            "pppoe" => &mut r.pppoe_seconds,
            "dns" => &mut r.dns_seconds,
            "internet" => &mut r.internet_seconds,
            "wireguard" => &mut r.wireguard_seconds,
            _ => return,
        };
        // Repeated checks after a milestone is recorded are read-only.
        if slot.is_none() {
            *slot = Some(seconds);
            let _ = self.persist_locked(&mut st);
        }
    }

    pub fn boot(&self) -> Boot {
        self.lock().boot.clone()
    }

    /// Captures until every expected milestone is ready, the window ends, or
    /// `stop` is set.
    pub fn run(&self, stop: &AtomicBool, pppoe_enabled: bool, wg_interface: &str, wg_enabled: bool) {
        {
            let mut st = self.lock();
            if st.boot.completed {
                return;
            }
            let mut expected = vec!["management".to_string()];
            if pppoe_enabled {
                expected.extend(["pppoe", "dns", "internet"].map(String::from));
            }
            if wg_enabled && !wg_interface.is_empty() {
                expected.push("wireguard".into());
            }
            st.boot.expected = expected.into();
            let _ = self.persist_locked(&mut st);
        }
        let deadline = self.started.add(WINDOW);
        if !Time::now().before(&deadline) {
            self.complete("timeout");
            return;
        }
        if !pppoe_enabled {
            self.event("wan", "No uplink configured at boot; DNS and Internet milestones do not apply");
        }
        self.sample();
        if self.check_readiness(pppoe_enabled, wg_interface, wg_enabled) {
            self.complete("ready");
            return;
        }
        loop {
            std::thread::sleep(StdDuration::from_secs(1));
            if stop.load(Ordering::SeqCst) {
                self.complete("interrupted");
                return;
            }
            if !Time::now().before(&deadline) {
                self.complete("timeout");
                return;
            }
            self.sample();
            if self.check_readiness(pppoe_enabled, wg_interface, wg_enabled) {
                self.complete("ready");
                return;
            }
        }
    }

    /// Only checks that have not succeeded yet; DNS/Internet probes wait for
    /// PPPoE when it is configured and do not apply without an uplink.
    fn check_readiness(&self, pppoe_enabled: bool, wg_interface: &str, wg_enabled: bool) -> bool {
        let mut r = self.lock().boot.readiness.clone();
        let interface_exists = |name: &str| std::path::Path::new("/sys/class/net").join(name).exists();
        if pppoe_enabled && r.pppoe_seconds.is_none() && interface_exists("ppp0") {
            self.ready("pppoe");
            r.pppoe_seconds = Some(self.offset_seconds());
        }
        if wg_enabled && !wg_interface.is_empty() && r.wireguard_seconds.is_none() && interface_exists(wg_interface) {
            self.ready("wireguard");
            r.wireguard_seconds = Some(self.offset_seconds());
        }
        let wg_ready = !wg_enabled || wg_interface.is_empty() || r.wireguard_seconds.is_some();
        if !pppoe_enabled {
            return r.management_seconds.is_some() && wg_ready;
        }
        let wan_ready = r.pppoe_seconds.is_some();
        if wan_ready && r.dns_seconds.is_none() {
            let resolved = std::net::ToSocketAddrs::to_socket_addrs(&("example.com", 443)).is_ok_and(|mut a| a.next().is_some());
            if resolved {
                self.ready("dns");
                r.dns_seconds = Some(self.offset_seconds());
            }
        }
        if wan_ready && r.internet_seconds.is_none() {
            let addr: std::net::SocketAddr = "1.1.1.1:443".parse().expect("literal");
            if std::net::TcpStream::connect_timeout(&addr, StdDuration::from_secs(2)).is_ok() {
                self.ready("internet");
                r.internet_seconds = Some(self.offset_seconds());
            }
        }
        r.management_seconds.is_some() && r.pppoe_seconds.is_some() && wg_ready && r.dns_seconds.is_some() && r.internet_seconds.is_some()
    }

    fn complete(&self, reason: &str) {
        let mut st = self.lock();
        if st.boot.completed {
            return;
        }
        st.boot.completed = true;
        st.boot.completion_reason = reason.into();
        st.boot.finished_seconds = Some(self.offset_seconds());
        let _ = self.persist_locked(&mut st);
    }

    fn sample(&self) {
        let (total, idle) = read_cpu();
        let (used, total_mb) = read_memory();
        let mut st = self.lock();
        if st.boot.completed {
            return;
        }
        let mut cpu = 0.0;
        if st.last_total > 0 && total > st.last_total {
            let delta_total = total - st.last_total;
            let delta_idle = idle.wrapping_sub(st.last_cpu);
            cpu = (100.0 * (1.0 - delta_idle as f64 / delta_total as f64)).clamp(0.0, 100.0);
        }
        st.last_total = total;
        st.last_cpu = idle;
        let offset = self.offset_seconds();
        st.boot.samples.push(Sample { offset_seconds: offset, cpu_percent: cpu, memory_used_mb: used, memory_total_mb: total_mb });
        let now = Time::now();
        if st.last_sample_persist.is_some_and(|t| now.sub(&t) < SAMPLE_PERSIST_INTERVAL) {
            return;
        }
        st.last_sample_persist = Some(now);
        let _ = self.persist_locked(&mut st);
    }

    fn persist(&self) -> Result<()> {
        let mut st = self.lock();
        self.persist_locked(&mut st)
    }

    fn persist_locked(&self, st: &mut State) -> Result<()> {
        st.boot.updated_at = Time::now_utc();
        let data = json::marshal_indent(&st.boot, "  ")?;
        let tmp = self.dir.join(format!("{}.json.tmp", st.boot.id));
        let dst = self.dir.join(format!("{}.json", st.boot.id));
        std::fs::write(&tmp, data)?;
        crate::config::set_mode(&tmp.to_string_lossy(), 0o600)?;
        std::fs::rename(tmp, dst)?;
        Ok(())
    }
}

fn read_cpu() -> (u64, u64) {
    std::fs::read_to_string("/proc/stat").ok().and_then(|s| s.lines().next().map(parse_cpu)).unwrap_or((0, 0))
}

/// guest/guest_nice are already part of user/nice; iowait counts as idle.
pub fn parse_cpu(line: &str) -> (u64, u64) {
    let fields: Vec<&str> = line.split_whitespace().collect();
    if fields.len() < 5 {
        return (0, 0);
    }
    let total = fields[1..].iter().take(8).map(|v| v.parse::<u64>().unwrap_or(0)).sum();
    let mut idle = fields[4].parse::<u64>().unwrap_or(0);
    if fields.len() > 5 {
        idle += fields[5].parse::<u64>().unwrap_or(0);
    }
    (total, idle)
}

fn read_memory() -> (f64, f64) {
    let Ok(data) = std::fs::read_to_string("/proc/meminfo") else { return (0.0, 0.0) };
    let (mut total, mut available) = (0.0, 0.0);
    for line in data.split('\n') {
        let f: Vec<&str> = line.split_whitespace().collect();
        if f.len() < 2 {
            continue;
        }
        let value = f[1].parse::<f64>().unwrap_or(0.0);
        match f[0] {
            "MemTotal:" => total = value / 1024.0,
            "MemAvailable:" => available = value / 1024.0,
            _ => {}
        }
    }
    (total - available, total)
}

fn prune(dir: &std::path::Path) -> std::io::Result<()> {
    let mut files: Vec<(std::path::PathBuf, std::time::SystemTime)> = std::fs::read_dir(dir)?
        .flatten()
        .filter(|e| e.file_type().is_ok_and(|t| !t.is_dir()) && e.file_name().to_string_lossy().ends_with(".json"))
        .filter_map(|e| e.metadata().and_then(|m| m.modified()).ok().map(|t| (e.path(), t)))
        .collect();
    if files.len() <= MAX_BOOTS {
        return Ok(());
    }
    files.sort_by_key(|(_, t)| *t);
    let excess = files.len() - MAX_BOOTS;
    for (path, _) in files.into_iter().take(excess) {
        let _ = std::fs::remove_file(path);
    }
    Ok(())
}

fn normalize_boot(boot: &mut Boot) {
    if boot.events.is_nil() {
        boot.events = List::empty();
    }
    if boot.samples.is_nil() {
        boot.samples = List::empty();
    }
}

pub fn load(data_dir: &str) -> Result<Vec<Boot>> {
    let dir = std::path::Path::new(data_dir).join("startup");
    let entries = match std::fs::read_dir(&dir) {
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(Vec::new()),
        Err(e) => return Err(e.into()),
        Ok(entries) => entries,
    };
    let mut out = Vec::new();
    for entry in entries.flatten() {
        if entry.file_type().is_ok_and(|t| t.is_dir()) || !entry.file_name().to_string_lossy().ends_with(".json") {
            continue;
        }
        if let Ok(data) = std::fs::read(entry.path()) {
            if let Ok(mut boot) = json::unmarshal::<Boot>(&data) {
                normalize_boot(&mut boot);
                out.push(boot);
            }
        }
    }
    out.sort_by(|a, b| b.started_at.cmp(&a.started_at));
    out.truncate(MAX_BOOTS);
    Ok(out)
}

impl Boot {
    /// Never infers successful startup from a legacy completed flag.
    pub fn capture_status(&self, now: Time) -> &'static str {
        if self.completed {
            return match self.completion_reason.as_str() {
                "ready" => "ready",
                "timeout" => "timeout",
                "interrupted" => "interrupted",
                _ => "unknown",
            };
        }
        if now.sub(&self.started_at) > WINDOW + SAMPLE_PERSIST_INTERVAL {
            return "interrupted";
        }
        "capturing"
    }

    pub fn summary(&self) -> String {
        format!("boot {}", self.started_at.rfc3339())
    }
}

/// A boot without its full sample series.
#[derive(Clone, Debug, Serialize)]
pub struct BootSummary {
    #[serde(flatten)]
    pub boot: Boot,
    pub status: String,
    pub sample_count: usize,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub last_sample: Option<Sample>,
}

pub fn summarize(b: &Boot, now: Time) -> BootSummary {
    let mut boot = b.clone();
    let last_sample = b.samples.last().cloned();
    let sample_count = b.samples.len();
    // Go's shadowing field drops the series (omitempty on a nil slice).
    boot.samples = List::nil();
    BootSummary { status: b.capture_status(now).into(), boot, sample_count, last_sample }
}

/// Accepts an identifier, never an operator-supplied filesystem path.
pub fn load_boot(data_dir: &str, id: &str) -> Result<Boot> {
    if !valid_boot_id(id) || id == "." || id == ".." {
        return Err(std::io::Error::from(std::io::ErrorKind::NotFound).into());
    }
    let data = std::fs::read(std::path::Path::new(data_dir).join("startup").join(format!("{}.json", id)))?;
    let mut boot: Boot = json::unmarshal(&data)?;
    if boot.id != id {
        return Err(errorf!("startup identity mismatch"));
    }
    normalize_boot(&mut boot);
    Ok(boot)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cpu_line_parsing_matches_go() {
        assert_eq!(parse_cpu("cpu  100 0 50 800 50 0 0 0 0 0"), (1000, 850));
        assert_eq!(parse_cpu("cpu 1 2"), (0, 0));
    }

    #[test]
    fn capture_status_rules() {
        let mut boot = Boot { started_at: Time::now(), ..Default::default() };
        assert_eq!(boot.capture_status(Time::now()), "capturing");
        boot.completed = true;
        assert_eq!(boot.capture_status(Time::now()), "unknown");
        boot.completion_reason = "ready".into();
        assert_eq!(boot.capture_status(Time::now()), "ready");
    }
}
