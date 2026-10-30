//! Root side of the DNS filter: validates streamed domains, writes one fixed
//! dnsmasq drop-in, activates it with a durable rollback marker and proves the
//! running resolver generation through a local TXT record.

use std::io::{BufRead, BufReader, Read, Seek, Write};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use super::dnswire;
use super::sys::*;
use crate::dnsfilter::{self, Applied, Request, Response};
use crate::errorf;
use crate::goish::errors::Result;
use crate::goish::json;
use crate::goish::time::Time;
use crate::services;

const FILTER_HEADER: &str = "# minimalrouter-filter-v1 ";
const FILTER_HEALTH_NAME: &str = "filter-health.minimalrouter.invalid.";

static FILTER_UPDATE: Mutex<()> = Mutex::new(());

pub(crate) fn filter_path() -> &'static str {
    dnsfilter::DROP_IN_PATH
}

pub(crate) fn read_filter_state() -> Result<Applied> {
    let file = match std::fs::File::open(filter_path()) {
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(Applied::default()),
        Err(e) => return Err(e.into()),
        Ok(f) => f,
    };
    let mut reader = BufReader::with_capacity(dnsfilter::MAX_METADATA_BYTES + 1, file.take(dnsfilter::MAX_METADATA_BYTES as u64 + 1));
    let mut line = String::new();
    let n = reader.read_line(&mut line)?;
    if n == 0 || !line.ends_with('\n') || !line.starts_with(FILTER_HEADER) {
        return Err(errorf!("DNS filter state is invalid"));
    }
    let state: Applied = dnsfilter::decode_metadata(line[FILTER_HEADER.len()..].as_bytes())?;
    state.policy.validate()?;
    Ok(state)
}

#[cfg(unix)]
pub(crate) fn handle_connection(stream: std::os::unix::net::UnixStream) {
    let deadline = Instant::now() + Duration::from_secs(90);
    let _ = stream.set_read_timeout(Some(Duration::from_secs(90)));
    let mut response = Response::default();
    if validate_peer(&stream).is_err() {
        response.error = "unauthorized local peer".into();
    } else {
        let mut reader = BufReader::with_capacity(dnsfilter::MAX_METADATA_BYTES + 1, &stream);
        let mut line = Vec::new();
        let read = (&mut reader).take(dnsfilter::MAX_METADATA_BYTES as u64 + 1).read_until(b'\n', &mut line);
        let request: Option<Request> = match read {
            Ok(_) if line.ends_with(b"\n") => dnsfilter::decode_metadata::<Request>(&line).ok().filter(|r| r.version == 1),
            _ => None,
        };
        match request {
            None => response.error = "invalid DNS filter request".into(),
            Some(request) if request.operation == "status" => {
                let mut probe = [0u8; 1];
                if !matches!(reader.read(&mut probe), Ok(0)) {
                    response.error = "unexpected status data".into();
                } else {
                    match read_filter_status() {
                        Ok(state) => response.state = state,
                        Err(_) => response.error = "DNS filter state unavailable".into(),
                    }
                }
            }
            Some(request) if request.operation == "apply" => match FILTER_UPDATE.try_lock() {
                Err(_) => response.error = "DNS filter update is already running".into(),
                Ok(_guard) => match apply_dns_filter(&request, &mut reader) {
                    Ok(state) => response.state = state,
                    Err((state, err)) => {
                        response.state = state;
                        response.error = err.to_string();
                    }
                },
            },
            Some(_) => response.error = "unsupported DNS filter operation".into(),
        }
    }
    let _ = deadline;
    let _ = write_json_line(&stream, &response);
}

struct StatusCache {
    revision: u64,
    applied_at: i64,
    checked_at: Option<Instant>,
    checked_unix: i64,
    healthy: bool,
    running: bool,
}

static STATUS_CACHE: Mutex<StatusCache> =
    Mutex::new(StatusCache { revision: 0, applied_at: 0, checked_at: None, checked_unix: 0, healthy: false, running: false });

fn pending_marker_exists() -> bool {
    std::fs::metadata(format!("{}.pending", filter_path())).is_ok()
}

/// Status never holds the network apply lock or runs the activation retry
/// loop; one bounded probe is shared for five seconds and rechecked against
/// the current generation before it is published.
fn read_filter_status() -> Result<Applied> {
    let mut state = read_filter_state()?;
    state.healthy = false;
    state.activation_pending = pending_marker_exists();
    if state.activation_pending || state.applied_at == 0 {
        return Ok(state);
    }
    {
        let mut cache = STATUS_CACHE.lock().unwrap_or_else(|e| e.into_inner());
        if cache.revision == state.policy.revision
            && cache.applied_at == state.applied_at
            && cache.checked_at.is_some_and(|t| t.elapsed() < Duration::from_secs(5))
        {
            state.healthy = cache.healthy;
            state.health_checked_at = cache.checked_unix;
            return Ok(state);
        }
        if cache.running {
            state.health_checking = true;
            return Ok(state);
        }
        cache.running = true;
    }
    let healthy = probe_filter_revision_once(state.policy.revision, "127.0.0.1:53").is_ok();
    let current = read_filter_state();
    let pending = pending_marker_exists();
    let mut cache = STATUS_CACHE.lock().unwrap_or_else(|e| e.into_inner());
    cache.running = false;
    let mut current = current?;
    if pending || current.policy.revision != state.policy.revision || current.applied_at != state.applied_at {
        current.healthy = false;
        current.health_checking = !pending;
        current.activation_pending = pending;
        return Ok(current);
    }
    cache.revision = state.policy.revision;
    cache.applied_at = state.applied_at;
    cache.checked_at = Some(Instant::now());
    cache.checked_unix = Time::now().unix();
    cache.healthy = healthy;
    state.healthy = healthy;
    state.health_checked_at = cache.checked_unix;
    Ok(state)
}

type FilterResult = std::result::Result<Applied, (Applied, crate::goish::errors::Error)>;

fn apply_dns_filter(request: &Request, domains: &mut dyn BufRead) -> FilterResult {
    let empty = |e| (Applied::default(), e);
    request.policy.validate().map_err(empty)?;
    if request.sources.len() > dnsfilter::sources().len() {
        return Err(empty(errorf!("invalid source manifest")));
    }
    for (id, hash) in &request.sources {
        if !request.policy.category(id) || hash.len() != 64 || !hash.bytes().all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b)) {
            return Err(empty(errorf!("invalid source hash")));
        }
    }
    for (id, on) in &request.policy.categories {
        if *on && request.sources.get(id).is_none_or(|h| h.is_empty()) {
            return Err(empty(errorf!("enabled category has no validated list")));
        }
    }
    let mut state = Applied { policy: request.policy.clone(), applied_at: Time::now().unix(), sources: request.sources.clone(), ..Default::default() };
    if request.expected_revision == u64::MAX {
        return Err((state, errorf!("DNS filter revision exhausted")));
    }
    state.policy.revision = request.expected_revision + 1;
    let dir = std::path::Path::new(filter_path()).parent().unwrap_or(std::path::Path::new("/"));
    let suffix = crate::auth::generate_random_hex(6).map_err(|e| (state.clone(), e))?;
    let final_tmp = dir.join(format!(".dns-filter-{}", suffix));
    let content_tmp = dir.join(format!(".dns-domains-{}", suffix));
    struct Cleanup(Vec<std::path::PathBuf>);
    impl Drop for Cleanup {
        fn drop(&mut self) {
            for p in &self.0 {
                let _ = std::fs::remove_file(p);
            }
        }
    }
    let _cleanup = Cleanup(vec![final_tmp.clone(), content_tmp.clone()]);
    let io = |state: &Applied| {
        let state = state.clone();
        move |e: std::io::Error| (state.clone(), crate::goish::errors::Error::new(e))
    };
    let content = std::fs::OpenOptions::new().read(true).write(true).create_new(true).open(&content_tmp).map_err(io(&state))?;
    let mut writer = std::io::BufWriter::with_capacity(32768, &content);
    let mut limited = domains.take(dnsfilter::MAX_STREAM_BYTES as u64 + 1);
    let mut line = Vec::new();
    loop {
        line.clear();
        let n = (&mut limited).take(513).read_until(b'\n', &mut line).map_err(io(&state))?;
        if n == 0 {
            break;
        }
        // bufio.Scanner with a 512-byte buffer rejects longer tokens.
        if line.len() > 512 && !line.ends_with(b"\n") {
            return Err((state, errorf!("bufio.Scanner: token too long")));
        }
        let text = String::from_utf8_lossy(&line);
        let text = text.strip_suffix('\n').unwrap_or(&text);
        let text = text.strip_suffix('\r').unwrap_or(text);
        match dnsfilter::domain(text) {
            Some(d) if d == text => {
                state.domains += 1;
                if state.domains > dnsfilter::MAX_DOMAINS as i64 {
                    return Err((state, errorf!("DNS blocklist exceeds appliance limit")));
                }
                if !state.policy.allowed(&d) {
                    writeln!(writer, "address=/{}/", d).map_err(io(&state))?;
                }
            }
            _ => return Err((state, errorf!("invalid blocking domain"))),
        }
    }
    if limited.limit() == 0 {
        return Err((state, errorf!("DNS domain stream too large")));
    }
    if state.domains == 0 && !state.sources.is_empty() {
        return Err((state, errorf!("empty blocking list refused")));
    }
    if state.domains > 0 && state.sources.is_empty() {
        return Err((state, errorf!("blocking domains require an enabled source")));
    }
    if let Err(err) = filter_resource_budget(state.domains) {
        return Err((state, err));
    }
    for exception in &state.policy.exceptions {
        writeln!(writer, "server=/{}/#", exception.domain).map_err(io(&state))?;
    }
    // More-specific bundled entries must not defeat a parent exception.
    for domain in services::builtin_blocklist() {
        if state.policy.allowed(domain) {
            writeln!(writer, "server=/{}/#", domain).map_err(io(&state))?;
        }
    }
    writeln!(writer, "txt-record={},{}", FILTER_HEALTH_NAME.trim_end_matches('.'), state.policy.revision).map_err(io(&state))?;
    writer.flush().map_err(io(&state))?;
    drop(writer);
    let metadata = json::marshal_string(&state).map_err(|e| (state.clone(), e.into()))?;
    {
        let mut out = std::fs::OpenOptions::new().write(true).create_new(true).open(&final_tmp).map_err(io(&state))?;
        crate::config::set_mode(&final_tmp.to_string_lossy(), 0o640).map_err(io(&state))?;
        write!(out, "{}{}\n", FILTER_HEADER, metadata).map_err(io(&state))?;
        let mut content = content;
        content.seek(std::io::SeekFrom::Start(0)).map_err(io(&state))?;
        std::io::copy(&mut content, &mut out).map_err(io(&state))?;
        out.sync_all().map_err(io(&state))?;
    }

    // The activation holds the network apply lock until it is finished.
    let guarded = super::lock_apply();
    if super::actions::guard_service_action_mutation(&guarded).is_err() {
        return Err((state, errorf!("confirm or roll back the pending network change before changing DNS protection")));
    }
    let previous = read_filter_state().map_err(|e| (state.clone(), e))?;
    if previous.policy.revision != request.expected_revision {
        return Err((previous, errorf!("DNS filter changed in another session; reload before saving")));
    }
    let path = filter_path();
    let backup = format!("{}.previous", path);
    let had_previous = previous.applied_at > 0;
    let fail = |e: crate::goish::errors::Error| (previous.clone(), e);
    if pending_marker_exists() {
        return Err(fail(errorf!("unfinished DNS filter activation requires helper recovery")));
    }
    match std::fs::remove_file(&backup) {
        Err(e) if e.kind() != std::io::ErrorKind::NotFound => return Err(fail(e.into())),
        _ => {}
    }
    if had_previous {
        std::fs::hard_link(path, &backup).map_err(|e| fail(e.into()))?;
    }
    sync_dir(dir).map_err(|e| fail(e.into()))?;
    atomic_write(&format!("{}.pending", path), had_previous.to_string().as_bytes(), 0o600).map_err(fail)?;
    std::fs::rename(&final_tmp, path).map_err(|e| fail(e.into()))?;
    let activation = sync_dir(dir)
        .map_err(crate::goish::errors::Error::new)
        .and_then(|_| super::dnsactivity_helper::test_dnsmasq_config())
        .and_then(|_| super::dnsactivity_helper::restart_dnsmasq())
        .and_then(|_| filter_resolver_healthy(state.policy.revision));
    if let Err(err) = activation {
        log::warn!("[DNS FILTER] activation step failed, rolling back: {}", err);
        let rollback = restore_filter_previous(had_previous)
            .and_then(|_| super::dnsactivity_helper::restart_dnsmasq())
            .and_then(|_| if had_previous { filter_resolver_healthy(previous.policy.revision) } else { Ok(()) });
        if rollback.is_err() {
            return Err(fail(errorf!("DNS filter activation and rollback verification failed; console recovery required")));
        }
        finish_filter_activation().map_err(fail)?;
        return Err(fail(errorf!("DNS filter activation failed; previous DNS configuration restored")));
    }
    if finish_filter_activation().is_err() {
        return Err((state, errorf!("DNS policy is active but its commit could not be made durable; helper recovery required")));
    }
    state.healthy = true;
    Ok(state)
}

fn restore_filter_previous(existed: bool) -> Result<()> {
    let path = filter_path();
    if existed {
        let staged = format!("{}.restore", path);
        match std::fs::remove_file(&staged) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => return Err(e.into()),
            _ => {}
        }
        std::fs::hard_link(format!("{}.previous", path), &staged)?;
        std::fs::rename(&staged, path)?;
    } else {
        match std::fs::remove_file(path) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => return Err(e.into()),
            _ => {}
        }
    }
    sync_dir(std::path::Path::new(path).parent().unwrap_or(std::path::Path::new("/"))).map_err(Into::into)
}

fn finish_filter_activation() -> Result<()> {
    let path = filter_path();
    clear_file_durably(&format!("{}.pending", path))?;
    // An orphaned hard link after a crash is harmless once the marker is gone.
    let _ = std::fs::remove_file(format!("{}.previous", path));
    Ok(())
}

/// Finishes or undoes an activation interrupted by a crash, before serving.
pub(crate) fn recover_filter_activation() -> Result<()> {
    let data = match std::fs::read_to_string(format!("{}.pending", filter_path())) {
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(e) => return Err(e.into()),
        Ok(d) => d,
    };
    let existed = match data.as_str() {
        "1" | "t" | "T" | "TRUE" | "true" | "True" => true,
        "0" | "f" | "F" | "FALSE" | "false" | "False" => false,
        other => return Err(errorf!("strconv.ParseBool: parsing {}: invalid syntax", crate::goish::strings::quote(other))),
    };
    restore_filter_previous(existed)?;
    super::dnsactivity_helper::restart_dnsmasq()?;
    if existed {
        let state = read_filter_state()?;
        filter_resolver_healthy(state.policy.revision)?;
    }
    finish_filter_activation()
}

/// Refuses growth instead of risking an appliance OOM while the old resolver
/// still runs during the parse of the new list.
fn filter_resource_budget(domains: i64) -> Result<()> {
    if domains == 0 {
        return Ok(());
    }
    let meminfo = std::fs::read_to_string("/proc/meminfo").map_err(|_| errorf!("available memory could not be checked"))?;
    for line in meminfo.lines() {
        let f: Vec<&str> = line.split_whitespace().collect();
        if f.len() == 3 && f[0] == "MemAvailable:" {
            let ok = f[1].parse::<u64>().is_ok_and(|kb| kb >= (64 << 10) + domains as u64 / 4);
            if !ok {
                return Err(errorf!("insufficient available memory for selected DNS lists; disable a category or increase appliance RAM"));
            }
            return Ok(());
        }
    }
    Err(errorf!("available memory could not be checked"))
}

const FILTER_PROBE_INTERVAL: Duration = Duration::from_millis(250);
const FILTER_PROBE_TIMEOUT: Duration = Duration::from_secs(15);
const FILTER_PROBE_ATTEMPT_TIMEOUT: Duration = Duration::from_secs(1);

/// The running resolver's generation, retried until a bounded deadline: the
/// restart returns before a large catalog is bound and serving.
fn filter_resolver_healthy(revision: u64) -> Result<()> {
    let deadline = Instant::now() + FILTER_PROBE_TIMEOUT;
    loop {
        let err = match probe_filter_revision_once(revision, "127.0.0.1:53") {
            Ok(()) => return Ok(()),
            Err(e) => e,
        };
        if Instant::now() >= deadline {
            return Err(err);
        }
        std::thread::sleep(FILTER_PROBE_INTERVAL);
    }
}

fn probe_filter_revision_once(revision: u64, address: &str) -> Result<()> {
    const ID: u16 = 23719;
    let query = dnswire::build_query(ID, FILTER_HEALTH_NAME, dnswire::TYPE_TXT, false)?;
    let reply = dnswire::parse(&dnswire::exchange(address, &query, FILTER_PROBE_ATTEMPT_TIMEOUT)?)?;
    if reply.id != ID || !reply.response || reply.rcode != 0 {
        return Err(errorf!("DNS filter generation probe failed"));
    }
    let want = revision.to_string();
    if reply.answers.iter().any(|a| a.rtype == dnswire::TYPE_TXT && a.name == FILTER_HEALTH_NAME && dnswire::txt_strings(&a.data) == want) {
        return Ok(());
    }
    Err(errorf!("running DNS generation does not match saved policy"))
}
