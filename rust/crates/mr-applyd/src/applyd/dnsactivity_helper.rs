//! DNS activity collection. routerd states the on/off setting on every
//! request; the helper converges dnsmasq on it with one fixed drop-in, drains
//! the tmpfs query log every minute into a bounded table and truncates it.
//! Only validated (client, name, count) tuples cross the privilege boundary.

use std::collections::HashMap;
use std::io::{BufRead, Read};
use std::path::Path;
use std::sync::Mutex;
use std::time::Duration;

use super::sys::*;
use crate::dnsactivity::{self, Lookup, Request, Response};
use crate::errorf;
use crate::goish::errors::{Result, wrapf};
use crate::goish::time::Time;

const QUERY_LOG_MAX_READ: u64 = 16 << 20;
const PENDING_MAX_ENTRIES: usize = 8192;
const BATCH_MAX_ENTRIES: usize = 1000;
const BATCH_MAX_BYTES: usize = 160 << 10;
const DRAIN_INTERVAL: Duration = Duration::from_secs(60);
const RESTART_PENDING: &str = "/run/minimalrouter/dns-activity-restart-pending";

pub(crate) fn test_dnsmasq_config() -> Result<()> {
    run_fixed("/usr/sbin/dnsmasq", &["--test"])
}

pub(crate) fn restart_dnsmasq() -> Result<()> {
    run_fixed("/sbin/rc-service", &["dnsmasq", "restart"])
}

#[derive(Default)]
struct Activity {
    pending: HashMap<(String, String), Lookup>,
    dropped: u64,
}

static ACTIVITY: Mutex<Option<Activity>> = Mutex::new(None);

/// Drains on a fixed interval so the log stays bounded even without routerd.
pub(crate) fn run_collector() {
    loop {
        {
            let mut activity = ACTIVITY.lock().unwrap_or_else(|e| e.into_inner());
            if let Err(err) = collect_query_log(&mut activity, Time::now().unix()) {
                log::warn!("[DNS] could not drain query log: {}", err);
            }
        }
        std::thread::sleep(DRAIN_INTERVAL);
    }
}

fn query_logging_enabled() -> Result<bool> {
    let data = match std::fs::read_to_string(dnsactivity::DROP_IN_PATH) {
        Ok(data) => data,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(false),
        Err(e) => return Err(e.into()),
    };
    let directive = format!("log-facility={}", dnsactivity::QUERY_LOG_PATH);
    Ok(data.lines().any(|line| line.trim() == directive))
}

/// Converges the drop-in under the apply lock. Enabling is fail-closed: DNS
/// service never depends on statistics.
fn ensure_query_logging(enabled: bool) -> Result<()> {
    let _guard = super::lock_apply();
    converge_query_logging(enabled, dnsactivity::DROP_IN_PATH, Path::new(RESTART_PENDING), test_dnsmasq_config, restart_dnsmasq)
}

fn converge_query_logging(enabled: bool, drop_in: &str, pending: &Path, validate: impl Fn() -> Result<()>, restart: impl Fn() -> Result<()>) -> Result<()> {
    let want = dnsactivity::drop_in();
    let current = match std::fs::read(drop_in) {
        Ok(data) => Some(data),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => None,
        Err(e) => return Err(wrapf("read DNS activity drop-in", e)),
    };
    let matches = if enabled { current.as_deref() == Some(want.as_bytes()) } else { current.is_none() };
    let interrupted = pending.try_exists()?;
    if matches && !interrupted {
        return Ok(());
    }
    // The drop-in can already match after an interrupted or failed restart.
    // This empty marker in root-owned tmpfs survives a helper process restart.
    if !interrupted {
        std::fs::File::create_new(pending)?;
    }
    if !enabled {
        if current.is_some() {
            std::fs::remove_file(drop_in).map_err(|e| wrapf("remove DNS activity drop-in", e))?;
        }
        restart().map_err(|e| wrapf("restart dnsmasq without query logging", e))?;
        std::fs::remove_file(pending)?;
        log::info!("[DNS] query logging disabled");
        return Ok(());
    }
    if !matches {
        atomic_write(drop_in, want.as_bytes(), 0o640).map_err(|e| wrapf("write DNS activity drop-in", e))?;
    }
    if let Err(err) = validate() {
        std::fs::remove_file(drop_in)?;
        return Err(wrapf("dnsmasq rejected query logging", err));
    }
    if let Err(err) = restart() {
        std::fs::remove_file(drop_in)?;
        match restart() {
            Ok(()) => std::fs::remove_file(pending)?,
            Err(restore) => log::warn!("[DNS] dnsmasq restart without query logging also failed: {}", restore),
        }
        return Err(wrapf("restart dnsmasq with query logging", err));
    }
    std::fs::remove_file(pending)?;
    log::info!("[DNS] query logging enabled");
    Ok(())
}

fn collect_query_log(activity: &mut Option<Activity>, now: i64) -> Result<bool> {
    if Path::new(RESTART_PENDING).try_exists()? {
        return Err(errorf!("DNS query logging restart is pending"));
    }
    if !query_logging_enabled()? {
        *activity = None;
        match std::fs::remove_file(dnsactivity::QUERY_LOG_PATH) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => return Err(wrapf("remove disabled query log", e)),
            _ => {}
        }
        return Ok(false);
    }
    let mut options = std::fs::OpenOptions::new();
    options.read(true).write(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK);
    }
    let file = match options.open(dnsactivity::QUERY_LOG_PATH) {
        Ok(f) => f,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(true),
        Err(e) => return Err(wrapf("open query log", e)),
    };
    let meta = file.metadata()?;
    if !meta.is_file() {
        return Err(errorf!("query log is not a regular file"));
    }
    if meta.len() == 0 {
        return Ok(true);
    }
    drain_log_file(&file, now, activity.get_or_insert_with(Activity::default))?;
    Ok(true)
}

fn drain_log_file(file: &std::fs::File, now: i64, activity: &mut Activity) -> Result<()> {
    let mut batch = Activity::default();
    read_query_lines(&mut std::io::BufReader::with_capacity(4096, file.take(QUERY_LOG_MAX_READ)), now, &mut batch)?;
    // Lines appended between the read and the truncation are lost; a
    // microsecond window once a minute is acceptable for statistics.
    file.set_len(0).map_err(|e| wrapf("truncate query log", e))?;
    activity.dropped += batch.dropped;
    for entry in batch.pending.into_values() {
        record_lookup(activity, entry.client, entry.name, entry.last_seen, entry.count);
    }
    Ok(())
}

fn read_query_lines(reader: &mut dyn BufRead, now: i64, activity: &mut Activity) -> Result<()> {
    let mut line = Vec::new();
    loop {
        line.clear();
        if reader.read_until(b'\n', &mut line)? == 0 {
            return Ok(());
        }
        // A final line without a newline is still being written; overlong
        // lines are never query records.
        if !line.ends_with(b"\n") || line.len() > 4096 {
            if !line.ends_with(b"\n") {
                return Ok(());
            }
            continue;
        }
        if let Some((client, name)) = parse_query_line(&String::from_utf8_lossy(&line)) {
            record_lookup(activity, client, name, now, 1);
        }
    }
}

fn record_lookup(activity: &mut Activity, client: String, name: String, now: i64, count: u32) {
    let key = (client, name);
    if let Some(entry) = activity.pending.get_mut(&key) {
        entry.count = entry.count.saturating_add(count);
        entry.last_seen = now;
        return;
    }
    if activity.pending.len() >= PENDING_MAX_ENTRIES {
        activity.dropped += u64::from(count);
        return;
    }
    activity.pending.insert(key.clone(), Lookup { client: key.0, name: key.1, count, last_seen: now });
}

/// Accepts exactly the dnsmasq log-queries record
/// `Oct  6 12:00:00 dnsmasq[1234]: query[A] example.com from 192.168.1.50`.
pub(crate) fn parse_query_line(line: &str) -> Option<(String, String)> {
    const MARKER: &str = ": query[";
    let line = line.trim_end_matches(['\r', '\n']);
    let index = line.find(MARKER)?;
    let prefix = &line[..index];
    if !prefix.ends_with(']') || !prefix.contains("dnsmasq[") {
        return None;
    }
    let rest = &line[index + MARKER.len()..];
    let end = rest.find(']')?;
    if end == 0 || end > 16 || &rest[..end] == "PTR" {
        return None;
    }
    let fields: Vec<&str> = rest[end + 1..].split_whitespace().collect();
    if fields.len() != 3 || fields[1] != "from" {
        return None;
    }
    let name = normalize_query_name(fields[0])?;
    let address: std::net::IpAddr = fields[2].parse().ok()?;
    let address = match address {
        std::net::IpAddr::V6(v6) => v6.to_ipv4_mapped().map(std::net::IpAddr::V4).unwrap_or(std::net::IpAddr::V6(v6)),
        v4 => v4,
    };
    if address.is_loopback() || address.is_unspecified() {
        return None;
    }
    Some((address.to_string(), name))
}

/// Multi-label hostnames of letters, digits, hyphens and underscores only.
fn normalize_query_name(raw: &str) -> Option<String> {
    let name = raw.strip_suffix('.').unwrap_or(raw).to_ascii_lowercase();
    if name.is_empty() || name.len() > 253 {
        return None;
    }
    let labels: Vec<&str> = name.split('.').collect();
    if labels.len() < 2 || labels.last() == Some(&"arpa") {
        return None;
    }
    if labels.iter().any(|l| l.is_empty() || l.len() > 63 || !l.bytes().all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'-' || c == b'_')) {
        return None;
    }
    if !raw.is_ascii() {
        return None;
    }
    Some(name)
}

fn drain(request: &Request) -> Response {
    let mut activity = ACTIVITY.lock().unwrap_or_else(|e| e.into_inner());
    if let Err(err) = ensure_query_logging(request.enabled) {
        log::warn!("[DNS] {}", err);
        return Response { error: "DNS query logging could not be changed".into(), ..Default::default() };
    }
    let enabled = match collect_query_log(&mut activity, Time::now().unix()) {
        Ok(enabled) => enabled,
        Err(err) => {
            log::warn!("[DNS] {}", err);
            return Response { error: "DNS query log could not be drained".into(), ..Default::default() };
        }
    };
    let mut response = Response { success: true, enabled, ..Default::default() };
    if let Some(activity) = activity.as_mut() {
        let mut size = 0;
        let keys: Vec<_> = activity.pending.keys().cloned().collect();
        for key in keys {
            let entry = &activity.pending[&key];
            let cost = entry.client.len() + entry.name.len() + 64;
            if response.entries.len() >= BATCH_MAX_ENTRIES || size + cost > BATCH_MAX_BYTES {
                response.more = true;
                break;
            }
            response.entries.push(entry.clone());
            size += cost;
            activity.pending.remove(&key);
        }
        response.dropped = activity.dropped;
        activity.dropped = 0;
    }
    response
}

#[cfg(unix)]
pub(crate) fn handle_connection(stream: std::os::unix::net::UnixStream) {
    let deadline = std::time::Instant::now() + REQUEST_READ_TIMEOUT;
    let response = if validate_peer(&stream).is_err() {
        Response { error: "unauthorized local peer".into(), ..Default::default() }
    } else {
        match read_request(&stream, dnsactivity::MAX_REQUEST_BYTES + 1, deadline)
            .map_err(|_| errorf!("invalid or oversized DNS activity request"))
            .and_then(|data| dnsactivity::decode_request(&data))
        {
            Ok(request) => drain(&request),
            Err(err) => Response { error: err.to_string(), ..Default::default() },
        }
    };
    let _ = write_json_line(&stream, &response);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn retrying_a_failed_restart_uses_the_marker_even_after_the_drop_in_changed() {
        use std::cell::Cell;
        let dir = std::env::temp_dir().join(format!("mr-dns-restart-{}-{}", std::process::id(), Time::now().unix_nano()));
        std::fs::create_dir_all(&dir).unwrap();
        let drop_in = dir.join("activity.conf");
        let marker = dir.join("restart-pending");
        std::fs::write(&drop_in, dnsactivity::drop_in()).unwrap();
        let restarts = Cell::new(0);
        let restart = || {
            restarts.set(restarts.get() + 1);
            if restarts.get() == 1 { Err(errorf!("simulated restart failure")) } else { Ok(()) }
        };
        let converge = |enabled| converge_query_logging(enabled, drop_in.to_str().unwrap(), &marker, || Ok(()), restart);
        assert!(converge(false).is_err());
        assert!(!drop_in.exists() && marker.exists());
        converge(false).unwrap();
        converge(false).unwrap();
        assert_eq!(restarts.get(), 2);
        assert!(!marker.exists());
        // An enable interrupted after writing the file must also restart once.
        std::fs::write(&drop_in, dnsactivity::drop_in()).unwrap();
        std::fs::File::create_new(&marker).unwrap();
        converge(true).unwrap();
        converge(true).unwrap();
        assert_eq!(restarts.get(), 3);
        assert!(!marker.exists());
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn an_untruncated_log_is_counted_once_after_a_successful_retry() {
        let path = std::env::temp_dir().join(format!("mr-dns-log-{}-{}", std::process::id(), Time::now().unix_nano()));
        std::fs::write(&path, "x dnsmasq[1]: query[A] one.example from 192.0.2.1\nx dnsmasq[1]: query[A] one.example from 192.0.2.1\n").unwrap();
        let mut activity = Activity::default();
        record_lookup(&mut activity, "192.0.2.1".into(), "one.example".into(), 1, 3);
        assert!(drain_log_file(&std::fs::File::open(&path).unwrap(), 2, &mut activity).is_err());
        assert_eq!(activity.pending.values().next().unwrap().count, 3);
        let file = std::fs::OpenOptions::new().read(true).write(true).open(&path).unwrap();
        drain_log_file(&file, 3, &mut activity).unwrap();
        assert_eq!(activity.pending.values().next().unwrap().count, 5);
        assert_eq!(file.metadata().unwrap().len(), 0);
        drop(file);
        std::fs::remove_file(path).unwrap();
    }

    #[test]
    fn parses_only_query_records() {
        assert_eq!(
            parse_query_line("Oct  6 12:00:00 dnsmasq[1234]: query[A] Example.COM. from 192.168.1.50\n"),
            Some(("192.168.1.50".into(), "example.com".into()))
        );
        assert!(parse_query_line("Oct  6 12:00:00 dnsmasq[1234]: query[PTR] 1.1.168.192.in-addr.arpa from 192.168.1.50").is_none());
        assert!(parse_query_line("Oct  6 12:00:00 dnsmasq[1234]: query[A] localhost from 192.168.1.50").is_none());
        assert!(parse_query_line("Oct  6 12:00:00 dnsmasq[1234]: query[A] example.com from 127.0.0.1").is_none());
        assert!(parse_query_line("Oct  6 12:00:00 dnsmasq[1234]: forwarded example.com to 1.1.1.1").is_none());
        assert_eq!(parse_query_line("x dnsmasq[1]: query[AAAA] a.b from ::ffff:10.0.0.9").unwrap().0, "10.0.0.9");
    }
}
