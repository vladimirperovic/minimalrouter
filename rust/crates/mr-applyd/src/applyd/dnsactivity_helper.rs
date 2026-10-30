//! DNS activity collection. routerd states the on/off setting on every
//! request; the helper converges dnsmasq on it with one fixed drop-in, drains
//! the tmpfs query log every minute into a bounded table and truncates it.
//! Only validated (client, name, count) tuples cross the privilege boundary.

use std::collections::HashMap;
use std::io::{BufRead, Read};
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

pub(crate) fn test_dnsmasq_config() -> Result<()> {
    run_fixed("/usr/sbin/dnsmasq", &["--test"])
}

pub(crate) fn restart_dnsmasq() -> Result<()> {
    run_fixed("/sbin/rc-service", &["dnsmasq", "restart"])
}

struct Activity {
    pending: HashMap<(String, String), Lookup>,
    dropped: u64,
}

static ACTIVITY: Mutex<Option<Activity>> = Mutex::new(None);

/// Drains on a fixed interval so the log stays bounded even without routerd.
pub(crate) fn run_collector() {
    loop {
        collect_query_log(Time::now().unix());
        std::thread::sleep(DRAIN_INTERVAL);
    }
}

fn query_logging_enabled() -> bool {
    let Ok(data) = std::fs::read_to_string(dnsactivity::DROP_IN_PATH) else {
        return false;
    };
    let directive = format!("log-facility={}", dnsactivity::QUERY_LOG_PATH);
    data.split('\n').any(|line| line.trim() == directive)
}

/// Converges the drop-in under the apply lock. Enabling is fail-closed: DNS
/// service never depends on statistics.
fn ensure_query_logging(enabled: bool) -> Result<()> {
    let want = dnsactivity::drop_in();
    let current = std::fs::read(dnsactivity::DROP_IN_PATH);
    let installed = current.is_ok();
    if enabled && current.as_ref().is_ok_and(|c| c.as_slice() == want.as_bytes()) {
        return Ok(());
    }
    if !enabled && !installed {
        return Ok(());
    }
    let _guard = super::lock_apply();
    if !enabled {
        match std::fs::remove_file(dnsactivity::DROP_IN_PATH) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => return Err(wrapf("remove DNS activity drop-in", e)),
            _ => {}
        }
        restart_dnsmasq().map_err(|e| wrapf("restart dnsmasq without query logging", e))?;
        log::info!("[DNS] query logging disabled");
        return Ok(());
    }
    atomic_write(dnsactivity::DROP_IN_PATH, want.as_bytes(), 0o640).map_err(|e| wrapf("write DNS activity drop-in", e))?;
    if let Err(err) = test_dnsmasq_config() {
        let _ = std::fs::remove_file(dnsactivity::DROP_IN_PATH);
        return Err(wrapf("dnsmasq rejected query logging", err));
    }
    if let Err(err) = restart_dnsmasq() {
        let _ = std::fs::remove_file(dnsactivity::DROP_IN_PATH);
        if let Err(restore) = restart_dnsmasq() {
            log::warn!("[DNS] dnsmasq restart without query logging also failed: {}", restore);
        }
        return Err(wrapf("restart dnsmasq with query logging", err));
    }
    log::info!("[DNS] query logging enabled");
    Ok(())
}

fn collect_query_log(now: i64) {
    let mut activity = ACTIVITY.lock().unwrap_or_else(|e| e.into_inner());
    if !query_logging_enabled() {
        *activity = None;
        match std::fs::remove_file(dnsactivity::QUERY_LOG_PATH) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => log::warn!("[DNS] could not remove query log after logging was disabled: {}", e),
            _ => {}
        }
        return;
    }
    let mut options = std::fs::OpenOptions::new();
    options.read(true).write(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.custom_flags(libc::O_NOFOLLOW);
    }
    let file = match options.open(dnsactivity::QUERY_LOG_PATH) {
        Ok(f) => f,
        Err(e) => {
            if e.kind() != std::io::ErrorKind::NotFound {
                log::warn!("[DNS] could not open query log: {}", e);
            }
            return;
        }
    };
    match file.metadata() {
        Ok(meta) if meta.file_type().is_file() => {
            if meta.len() == 0 {
                return;
            }
        }
        _ => {
            log::warn!("[DNS] query log is not a regular file; ignoring it");
            return;
        }
    }
    read_query_lines(
        &mut std::io::BufReader::with_capacity(4096, (&file).take(QUERY_LOG_MAX_READ)),
        now,
        activity.get_or_insert_with(|| Activity { pending: HashMap::new(), dropped: 0 }),
    );
    // Lines appended between the read and the truncation are lost; a
    // microsecond window once a minute is acceptable for statistics.
    if let Err(e) = file.set_len(0) {
        log::warn!("[DNS] could not truncate query log: {}", e);
    }
}

fn read_query_lines(reader: &mut dyn BufRead, now: i64, activity: &mut Activity) {
    let mut line = Vec::new();
    loop {
        line.clear();
        match reader.read_until(b'\n', &mut line) {
            Ok(0) | Err(_) => return,
            Ok(_) => {}
        }
        // A final line without a newline is still being written; overlong
        // lines are never query records.
        if !line.ends_with(b"\n") || line.len() > 4096 {
            if !line.ends_with(b"\n") {
                return;
            }
            continue;
        }
        if let Some((client, name)) = parse_query_line(&String::from_utf8_lossy(&line)) {
            record_lookup(activity, client, name, now);
        }
    }
}

fn record_lookup(activity: &mut Activity, client: String, name: String, now: i64) {
    let key = (client, name);
    if let Some(entry) = activity.pending.get_mut(&key) {
        entry.count = entry.count.saturating_add(1);
        entry.last_seen = now;
        return;
    }
    if activity.pending.len() >= PENDING_MAX_ENTRIES {
        activity.dropped += 1;
        return;
    }
    activity.pending.insert(key.clone(), Lookup { client: key.0, name: key.1, count: 1, last_seen: now });
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
    if let Err(err) = ensure_query_logging(request.enabled) {
        log::warn!("[DNS] {}", err);
        return Response { error: "DNS query logging could not be changed".into(), ..Default::default() };
    }
    collect_query_log(Time::now().unix());
    let mut activity = ACTIVITY.lock().unwrap_or_else(|e| e.into_inner());
    let mut response = Response { success: true, enabled: query_logging_enabled(), ..Default::default() };
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
