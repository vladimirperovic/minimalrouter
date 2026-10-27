//! routerd: the unprivileged management plane. It serves the dashboard and
//! REST API over TLS, owns canonical configuration, and reaches privileged
//! operations only through router-applyd's allowlisted socket.

#![forbid(unsafe_code)]

use std::collections::HashSet;
use std::net::TcpListener;
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::{Duration as StdDuration, Instant};

use regex::Regex;
use sha2::{Digest, Sha256};

use minimalrouter::accounting::{self, DNSDrain, DNSLookup, DNSSource};
use minimalrouter::api::{RateLimiterInterface, Server, SessionManagerInterface};
use minimalrouter::apply::{self, ApplyRequest, Client, Engine, UnixClient};
use minimalrouter::auth_persistent::{PersistentRateLimiter, PersistentSessionManager};
use minimalrouter::config::{self, SQLiteStore, SystemConfig};
use minimalrouter::goish::errors::{is, Result};
use minimalrouter::goish::{net, time::Time};
use minimalrouter::httpd::{self, Request, Response, ResponseBody, Router};
use minimalrouter::shutdown::Shutdown;
use minimalrouter::{dnsactivity, dnsfilter, dnsrisk, firmware, gateway, release, storage, tlsutil};

const FIRMWARE_KEY_PATH: &str = "/etc/minimalrouter/firmware-signing.pub";
const STARTUP_VERIFIED_HASH_PATH: &str = "/run/minimalrouter/routerd-startup-verified.sha256";
const STARTUP_CONSUMED_PATH: &str = "/run/minimalrouter/routerd-state/startup-fastpath-consumed";
const STARTUP_VERIFIED_MAX_AGE: StdDuration = StdDuration::from_secs(60);
const READY_FILE_ENV: &str = "MINIMALROUTER_READY_FILE";

fn fatal(message: String) -> ! {
    log::error!("{}", message);
    eprintln!("{}", message);
    std::process::exit(1);
}

/// Threads that end with the process-wide shutdown and are joined on exit.
struct Workers {
    stop: Shutdown,
    handles: Vec<std::thread::JoinHandle<()>>,
}

impl Workers {
    fn spawn(&mut self, run: impl FnOnce(Shutdown) + Send + 'static) {
        let stop = self.stop.clone();
        self.handles.push(std::thread::spawn(move || run(stop)));
    }

    fn join(self) {
        self.stop.trigger();
        for handle in self.handles {
            let _ = handle.join();
        }
    }
}

/// The root-created hash proves router-applyd just restored and verified
/// this exact configuration; a routerd-owned O_EXCL marker makes the fast
/// path one-shot across supervisor respawns.
fn startup_runtime_verified(cfg: &SystemConfig) -> bool {
    let consumed = std::fs::OpenOptions::new().write(true).create_new(true).open(STARTUP_CONSUMED_PATH);
    if consumed.is_err() {
        return false;
    }
    startup_runtime_verified_at(cfg, STARTUP_VERIFIED_HASH_PATH, std::time::SystemTime::now())
}

fn startup_runtime_verified_at(cfg: &SystemConfig, path: &str, now: std::time::SystemTime) -> bool {
    let Ok(meta) = std::fs::metadata(path) else { return false };
    if !meta.is_file() {
        return false;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        // The handoff must not be modifiable by the management plane.
        if meta.permissions().mode() & 0o022 != 0 {
            return false;
        }
    }
    let Ok(modified) = meta.modified() else { return false };
    let fresh = match now.duration_since(modified) {
        Ok(age) => age <= STARTUP_VERIFIED_MAX_AGE,
        Err(ahead) => ahead.duration() <= StdDuration::from_secs(5),
    };
    if !fresh {
        return false;
    }
    let Some(hint) = std::fs::read_to_string(path).ok().and_then(|d| hex::decode(d.trim()).ok()).filter(|h| h.len() == 32) else { return false };
    // router-applyd hashes the exact json.Marshal bytes of last-good.
    let Ok(canonical) = minimalrouter::goish::json::marshal(cfg) else { return false };
    bool::from(subtle::ConstantTimeEq::ct_eq(hint.as_slice(), Sha256::digest(canonical).as_slice()))
}

/// Called only after reconciliation, TLS preparation and listener binding:
/// OpenRC waits for this marker before reporting routerd started.
fn signal_routerd_ready(revision: config::Revision) -> Result<()> {
    let Ok(path) = std::env::var(READY_FILE_ENV) else { return Ok(()) };
    if path.is_empty() {
        return Ok(());
    }
    use std::io::Write;
    let mut options = std::fs::OpenOptions::new();
    options.write(true).create(true).truncate(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut file = options.open(&path).map_err(|e| minimalrouter::errorf!("open readiness marker: {}", e))?;
    file.write_all(format!("{}\n", revision).as_bytes()).map_err(|e| minimalrouter::errorf!("write readiness marker: {}", e))?;
    file.sync_all().map_err(|e| minimalrouter::errorf!("sync readiness marker: {}", e))?;
    Ok(())
}

fn configure_gateway(server: &Arc<Server>, data_dir: &str, workers: &mut Workers) {
    let store = match gateway::Store::open(data_dir) {
        Ok(store) => Arc::new(store),
        Err(err) => return log::info!("[GATEWAY] Monitoring unavailable: {}", err),
    };
    // Derived history must exist before the first sample.
    if let Err(err) = store.ensure_insights_schema() {
        return log::info!("[GATEWAY] Insight history unavailable: {}", err);
    }
    let monitor = gateway::Monitor::with_defaults(store);
    server.configure_gateway_monitor(Some(monitor.clone()));
    workers.spawn(move |stop| monitor.run(&stop));
    log::info!("[GATEWAY] Read-only PPPoE and WAN quality monitoring enabled");
}

/// Only validated client addresses, names and counts cross the privilege
/// boundary.
struct DNSActivitySource(dnsactivity::Client);

impl DNSSource for DNSActivitySource {
    fn drain(&self, deadline: Instant, enabled: bool) -> Result<DNSDrain> {
        let response = self.0.drain(deadline, enabled)?;
        Ok(DNSDrain {
            enabled: response.enabled,
            dropped: response.dropped,
            more: response.more,
            lookups: response.entries.into_iter().map(|e| DNSLookup { client: e.client, name: e.name, count: e.count, last_seen: e.last_seen }).collect(),
        })
    }
}

/// Optional: a store that cannot be opened makes accounting "unavailable",
/// never a failed management plane.
fn configure_accounting(server: &Arc<Server>, engine: &Arc<Engine>, data_dir: &str, workers: &mut Workers) {
    let store = match accounting::Store::open(data_dir) {
        Ok(store) => Arc::new(store),
        Err(err) => return log::info!("[ACCOUNTING] Per-device traffic accounting unavailable: {}", err),
    };
    server.configure_accounting_store(Some(store.clone()));
    let settings_engine = engine.clone();
    let collector = accounting::Collector::new(
        store.clone(),
        Arc::new(accounting::CommandReader),
        Box::new(move || {
            let cfg = settings_engine.get_current_config();
            // Every apply recreates the counter sets and advances the
            // revision, so the revision is exactly the counter generation.
            accounting::Settings { enabled: cfg.accounting.enabled, retention_months: cfg.accounting.retention_months, generation: u64::from(cfg.revision) }
        }),
    );
    workers.spawn(move |stop| collector.run(&stop));

    let (firewall_server, firewall_store) = (Arc::downgrade(server), store.clone());
    workers.spawn(move |stop| {
        let client = UnixClient::new("");
        loop {
            let request = ApplyRequest { id: format!("fwstats-{}", Time::now().unix_nano()), op: apply::op::firewall_counters(), ..Default::default() };
            if let (Ok(response), Some(server)) = (client.apply(Instant::now() + StdDuration::from_secs(10), &request), firewall_server.upgrade()) {
                if let (true, Some(counters)) = (response.success, response.firewall_counters) {
                    if let Err(err) = firewall_store.record_firewall(Time::now_utc(), &counters.generation, counters.seen, counters.accepted) {
                        log::info!("[FIREWALL] Activity storage failed: {}", err);
                    }
                    server.publish_firewall_observation(Some(counters));
                }
            }
            if !stop.sleep(StdDuration::from_secs(60)) {
                return;
            }
        }
    });
    log::info!("[ACCOUNTING] Per-device byte accounting collector started");

    // A failed settings read keeps the last known setting: "disabled" would
    // delete the history on a transient database error.
    let last_settings = Arc::new(Mutex::new(accounting::DNSSettings::default()));
    let (settings_store, dir_for_writes) = (store.clone(), data_dir.to_string());
    let dns_collector = Arc::new(accounting::DNSCollector::new(
        store.clone(),
        Arc::new(DNSActivitySource(dnsactivity::Client::default())),
        Box::new(move || match settings_store.dns_settings() {
            Ok(settings) => {
                *last_settings.lock().unwrap_or_else(|e| e.into_inner()) = settings.clone();
                settings
            }
            Err(err) => {
                log::info!("[DNS] could not read DNS activity setting: {}", err);
                last_settings.lock().unwrap_or_else(|e| e.into_inner()).clone()
            }
        }),
        Some(Box::new(move || storage::allow_nonessential_write(&dir_for_writes))),
    ));
    server.configure_dns_activity(Some(dns_collector.clone()));
    let (risk_store, risk_dir) = (store.clone(), data_dir.to_string());
    let risk = dnsrisk::Service::open(
        data_dir,
        Box::new(move || risk_store.dns_settings().map(|s| (s.enabled, s.retention_days))),
        Some(Box::new(move || storage::allow_nonessential_write(&risk_dir))),
    );
    match risk {
        Err(err) => log::info!("[DNS] Risk monitor unavailable: {}", err),
        Ok(service) => {
            let service = Arc::new(service);
            server.configure_dns_risk(Some(service.clone()));
            let (submit, clear) = (service.clone(), service.clone());
            dns_collector.set_risk_observer(
                Box::new(move |lookups: &[DNSLookup]| {
                    let batch: Vec<dnsrisk::Lookup> = lookups.iter().map(|l| dnsrisk::Lookup { name: l.name.clone(), address: l.client.clone(), count: l.count, at: l.last_seen }).collect();
                    submit.submit(&batch);
                }),
                Box::new(move || clear.clear()),
            );
            workers.spawn(move |stop| service.run(&stop));
        }
    }
    workers.spawn(move |stop| dns_collector.run(&stop));
}

fn configure_dns_filter(server: &Arc<Server>, data_dir: &str) -> Option<Arc<dnsfilter::Service>> {
    let dir = data_dir.to_string();
    match dnsfilter::Service::open(data_dir, Arc::new(dnsfilter::Client::new()), Some(Arc::new(move || storage::allow_nonessential_write(&dir)))) {
        Ok(service) => {
            server.configure_dns_filter_service(Some(service.clone()));
            Some(service)
        }
        Err(err) => {
            log::info!("[DNS FILTER] unavailable: {}", err);
            None
        }
    }
}

/// Every input that changes the generated SAN set; a stale hit would serve a
/// certificate missing a pending LAN or WireGuard address.
fn certificate_cache_key(active: &SystemConfig, ips: &[net::IP], dns: &[String]) -> String {
    let mut key = format!("{}|{}|{}|{}.{}", active.lan.ip_address, active.wireguard.address, if active.wireguard.enabled { "wg" } else { "" }, active.system.hostname, active.system.domain);
    for ip in ips {
        key.push('|');
        key.push_str(&ip.to_string());
    }
    for name in dns {
        key.push('|');
        key.push_str(name);
    }
    key
}

/// Pending LAN/WireGuard addresses and hostnames join the SAN list so the
/// administrator can confirm a change over the new management path.
fn pending_sans(engine: &Engine, active: &SystemConfig) -> (Vec<net::IP>, Vec<String>) {
    let (mut ips, mut dns) = (Vec::new(), Vec::new());
    if let Some(pending) = engine.get_pending_transaction() {
        let candidate = &pending.config;
        if !candidate.lan.ip_address.is_empty() && candidate.lan.ip_address != active.lan.ip_address {
            ips.extend(net::parse_ip(&candidate.lan.ip_address));
        }
        if candidate.wireguard.enabled {
            if let Some((ip, _)) = net::parse_cidr(&candidate.wireguard.address) {
                let active_wg = net::parse_cidr(&active.wireguard.address).map(|(ip, _)| ip);
                if active_wg.as_ref() != Some(&ip) {
                    ips.push(ip);
                }
            }
        }
        let name = |c: &SystemConfig| format!("{}.{}", c.system.hostname, c.system.domain).trim().trim_end_matches('.').to_string();
        let (candidate_name, active_name) = (name(candidate), name(active));
        if !candidate_name.is_empty() && !candidate_name.eq_ignore_ascii_case(&active_name) {
            dns.push(candidate_name);
        }
    }
    (ips, dns)
}

struct CertCache {
    key: String,
    validity: Option<(Time, Time)>,
}

/// A second boundary behind nftables: requests whose destination is not an
/// active LAN or WireGuard management address get 404.
fn management_destination_allowed(engine: &Engine, r: &Request<'_>) -> bool {
    let Some(destination) = net::split_host_port(&r.local_addr).ok().and_then(|(h, _)| net::parse_ip(&h)) else { return false };
    let (mut allowed, mut hosts): (HashSet<String>, HashSet<String>) = (HashSet::new(), HashSet::new());
    let mut add = |cfg: &SystemConfig, include_lan: bool| {
        let hostname = cfg.system.hostname.trim().to_lowercase();
        let domain = cfg.system.domain.trim().trim_matches('.').to_lowercase();
        if !hostname.is_empty() {
            hosts.insert(hostname.clone());
            if !domain.is_empty() {
                hosts.insert(format!("{}.{}", hostname, domain));
            }
        }
        if include_lan {
            if let Some(ip) = net::parse_ip(&cfg.lan.ip_address) {
                allowed.insert(ip.to_string());
                hosts.insert(ip.to_string());
            }
        }
        if cfg.wireguard.enabled {
            if let Some((ip, _)) = net::parse_cidr(&cfg.wireguard.address) {
                allowed.insert(ip.to_string());
                hosts.insert(ip.to_string());
            }
        }
    };
    let current = engine.get_current_config();
    match engine.get_pending_transaction() {
        None => add(&current, current.system.management_access != "wireguard_only"),
        Some(p) if p.config.system.management_access == "wireguard_only" => add(&p.config, false),
        Some(p) => {
            add(&current, true);
            add(&p.config, true);
        }
    }
    allowed.contains(&destination.to_string()) && request_host_allowed(&r.host, &hosts)
}

fn request_host_allowed(raw: &str, allowed: &HashSet<String>) -> bool {
    let host = match net::split_host_port(raw) {
        Ok((host, _)) => host,
        Err(_) if raw.contains(':') => return false,
        Err(_) => raw.to_string(),
    };
    allowed.contains(&host.trim_end_matches('.').to_lowercase())
}

/// POST/PUT/PATCH/DELETE that commit appliance state are refused under
/// critical storage pressure; sign-in, sign-out, export and previews stay.
fn mutation_needs_durable_write(method: &str, path: &str) -> bool {
    if !matches!(method, "POST" | "PUT" | "PATCH" | "DELETE") {
        return false;
    }
    if matches!(path, "/api/v1/auth/login" | "/api/v1/auth/logout" | "/api/v1/backup/export" | "/api/v1/firmware/verify" | "/api/v1/auth/totp/enroll") || path.ends_with("/preview") {
        return false;
    }
    path.starts_with("/api/")
}

static HASHED_ASSET: std::sync::LazyLock<Regex> = std::sync::LazyLock::new(|| Regex::new(r"^[A-Za-z0-9_.-]+-[A-Za-z0-9_-]{8,}\.(?:js|css|svg|png|jpe?g|webp|woff2?)$").unwrap());

fn content_type(path: &str) -> &'static str {
    match Path::new(path).extension().and_then(|e| e.to_str()).unwrap_or("").to_ascii_lowercase().as_str() {
        "html" | "htm" => "text/html; charset=utf-8",
        "js" | "mjs" => "text/javascript; charset=utf-8",
        "css" => "text/css; charset=utf-8",
        "svg" => "image/svg+xml",
        "png" => "image/png",
        "jpg" | "jpeg" => "image/jpeg",
        "webp" => "image/webp",
        "gif" => "image/gif",
        "ico" => "image/vnd.microsoft.icon",
        "woff" => "font/woff",
        "woff2" => "font/woff2",
        "json" => "application/json",
        "webmanifest" => "application/manifest+json",
        "txt" => "text/plain; charset=utf-8",
        "wasm" => "application/wasm",
        _ => "application/octet-stream",
    }
}

fn accepts_gzip(header: &str) -> bool {
    let mut wildcard = false;
    for item in header.split(',') {
        let mut parts = item.split(';');
        let encoding = parts.next().unwrap_or("").trim().to_lowercase();
        if encoding != "gzip" && encoding != "*" {
            continue;
        }
        let mut quality = 1.0;
        for parameter in parts {
            let Some((name, value)) = parameter.trim().split_once('=') else { continue };
            if !name.trim().eq_ignore_ascii_case("q") {
                continue;
            }
            quality = value.trim().parse::<f64>().ok().filter(|q| (0.0..=1.0).contains(q)).unwrap_or(0.0);
        }
        if encoding == "gzip" {
            return quality > 0.0;
        }
        wildcard = quality > 0.0;
    }
    wildcard
}

/// The dashboard bundle: SPA fallback to index.html, pre-compressed assets,
/// immutable caching for hashed names and a strict CSP.
fn serve_static(root: &str, r: &Request<'_>) -> Response {
    if r.path.starts_with("/api/") {
        return Response::error("404 page not found", 404);
    }
    if r.method != "GET" && r.method != "HEAD" {
        return Response::error("Method not allowed", 405);
    }
    if r.path.ends_with("/index.html") {
        let target = r.path.trim_end_matches("index.html").to_string();
        return Response::new(301).with_header("Location", target);
    }
    let mut relative = minimalrouter::goish::filepath::clean(&format!("/{}", r.path)).trim_start_matches('/').to_string();
    if relative.is_empty() || relative == "." {
        relative = "index.html".into();
    }
    let mut candidate = Path::new(root).join(&relative);
    if !std::fs::metadata(&candidate).is_ok_and(|m| m.is_file()) {
        if Path::new(&relative).extension().is_some() {
            return Response::error("404 page not found", 404);
        }
        candidate = Path::new(root).join("index.html");
        if !candidate.is_file() {
            return Response::error("404 page not found", 404);
        }
    }
    let candidate_text = candidate.to_string_lossy().into_owned();
    let name = candidate.file_name().and_then(|n| n.to_str()).unwrap_or("").to_string();
    let mut response = Response::new(200);
    for (header, value) in [
        ("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; style-src-attr 'none'; script-src-attr 'none'; img-src 'self' data:; font-src 'self'; connect-src 'self'; worker-src 'none'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"),
        ("Referrer-Policy", "no-referrer"),
        ("X-Content-Type-Options", "nosniff"),
        ("X-Frame-Options", "DENY"),
        ("Strict-Transport-Security", "max-age=63072000"),
        ("Cross-Origin-Opener-Policy", "same-origin"),
        ("Cross-Origin-Resource-Policy", "same-origin"),
        ("Permissions-Policy", "camera=(), microphone=(), geolocation=()"),
    ] {
        response.headers.set(header, value);
    }
    if name == "index.html" {
        response.headers.set("Cache-Control", "no-store");
    } else if relative.starts_with("assets/") && HASHED_ASSET.is_match(&name) {
        response.headers.set("Cache-Control", "public, max-age=31536000, immutable");
    }
    let mut path = candidate_text.clone();
    if matches!(Path::new(&candidate_text).extension().and_then(|e| e.to_str()), Some("html" | "js" | "css" | "svg")) {
        let gz = format!("{}.gz", candidate_text);
        if std::fs::metadata(&gz).is_ok_and(|m| m.is_file()) {
            response.headers.add("Vary", "Accept-Encoding");
            if accepts_gzip(r.headers.get("Accept-Encoding")) {
                response.headers.set("Content-Encoding", "gzip");
                path = gz;
            }
        }
    }
    response.headers.set("Content-Type", content_type(&candidate_text));
    if let Ok(modified) = std::fs::metadata(&path).and_then(|m| m.modified()) {
        let modified: chrono::DateTime<chrono::Utc> = modified.into();
        response.headers.set("Last-Modified", modified.format("%a, %d %b %Y %H:%M:%S GMT").to_string());
    }
    match std::fs::File::open(&path) {
        Ok(file) => {
            if let Ok(meta) = file.metadata() {
                response.headers.set("Content-Length", meta.len().to_string());
            }
            response.body = ResponseBody::Stream(Box::new(file));
            response
        }
        Err(_) => Response::error("404 page not found", 404),
    }
}

fn main() {
    minimalrouter::goish::log::init();
    let guard = minimalrouter::recovery::acquire_daemon_guard().unwrap_or_else(|e| fatal(format!("routerd offline migration admission failed: {}", e)));
    log::info!("Starting Minimal Router OS routerd (unprivileged management plane)...");

    let data_dir = std::env::var("MINIMALROUTER_DATA_DIR").ok().filter(|d| !d.is_empty()).unwrap_or_else(|| "./data".into());
    let abs_dir = std::path::absolute(&data_dir).map(|p| p.to_string_lossy().into_owned()).unwrap_or(data_dir);
    log::info!("Initializing configuration store at {}", abs_dir);
    let store = Arc::new(SQLiteStore::new_file_store(&abs_dir).unwrap_or_else(|e| fatal(format!("Failed to initialize store: {}", e))));

    let stop = Shutdown::new();
    // OpenRC sends SIGTERM and waits 10s: shut down cleanly so SQLite closes
    // instead of relying on crash recovery every reboot.
    #[cfg(unix)]
    if let Err(err) = stop.trigger_on_signals(&[signal_hook::consts::SIGTERM, signal_hook::consts::SIGINT]) {
        fatal(format!("Failed to install shutdown signal handler: {}", err));
    }
    let mut workers = Workers { stop: stop.clone(), handles: Vec::new() };
    // Bound/prune once at startup, then hourly as a passive safety net.
    let maintenance_store = store.clone();
    let maintain = move || {
        if let Err(err) = maintenance_store.maintain_storage() {
            log::info!("[STORAGE] maintenance failed: {}", err);
        }
    };
    maintain();
    workers.spawn(move |stop| {
        while stop.sleep(StdDuration::from_secs(3600)) {
            maintain();
        }
    });

    let initial = store.get_latest_config().unwrap_or_else(|e| fatal(format!("Refusing startup because canonical configuration is unavailable: {}", e)));
    let admin_hash = match store.get_admin_hash() {
        Ok(hash) => {
            log::info!("[AUTH] Loaded persisted administrator password hash from SQLite store");
            hash
        }
        Err(err) if is::<config::ErrNoRows>(&err) => String::new(),
        Err(err) => fatal(format!("Refusing startup because administrator state is unavailable: {}", err)),
    };
    if std::env::var("MINIMALROUTER_PREVIEW_MODE").as_deref() == Ok("1") {
        fatal("MINIMALROUTER_PREVIEW_MODE is restricted to the macOS development build".into());
    }
    let engine = Engine::new(initial.clone(), Some(store.clone()));
    if !admin_hash.is_empty() {
        if startup_runtime_verified(&initial) {
            log::info!("[BOOT] privileged startup runtime already verified canonical revision {}; duplicate reconcile skipped", initial.revision);
        } else if let Err(err) = engine.reconcile(Instant::now() + apply::RECONCILE_BUDGET) {
            // Serving after a failed reconcile could describe a runtime that
            // was never proven active; the supervisor retries instead.
            fatal(format!("Refusing startup because canonical runtime reconciliation failed: {}", err));
        }
    }

    let session_mgr: Arc<dyn SessionManagerInterface> = PersistentSessionManager::new(store.clone());
    let rate_limiter: Arc<dyn RateLimiterInterface> = PersistentRateLimiter::new(store.clone(), 5, minimalrouter::goish::time::Duration::secs(60));
    let global_limiter: Arc<dyn RateLimiterInterface> = PersistentRateLimiter::new(store.clone(), 100, minimalrouter::goish::time::Duration::secs(60));
    let server = Server::with_auth(engine.clone(), session_mgr, rate_limiter, &admin_hash, Some(store.clone()));
    server.configure_global_login_limiter(Some(global_limiter));
    configure_gateway(&server, &abs_dir, &mut workers);
    configure_accounting(&server, &engine, &abs_dir, &mut workers);
    let dns_filter = configure_dns_filter(&server, &abs_dir);
    match firmware::load_trusted_public_key(FIRMWARE_KEY_PATH) {
        Ok(key) => {
            server.configure_firmware_trust(&key, "/var/lib/minimalrouter-update/staging");
            log::info!("[SECURITY] Firmware verification enabled with pinned key {}", FIRMWARE_KEY_PATH);
        }
        Err(err) => log::info!("[SECURITY] Firmware updates disabled: trusted key unavailable: {}", err),
    }
    // Discovery is a convenience, never part of the boot path.
    let checker = server.configure_updates(release::Catalog::default(), firmware::host_arch());
    workers.spawn(move |stop| checker.run(&stop));

    let mut router = Router::new();
    server.register_routes(&mut router);
    if let Some(web_dir) = std::env::var("MINIMALROUTER_WEB_DIR").ok().filter(|d| !d.is_empty()) {
        let root = std::path::absolute(minimalrouter::goish::filepath::clean(&web_dir)).map(|p| p.to_string_lossy().into_owned()).unwrap_or(web_dir.clone());
        router.handle_fn("/", move |r| serve_static(&root, r));
        log::info!("Serving dashboard from {}", web_dir);
    }

    let cert_manager = Arc::new(tlsutil::CertManager::new(&abs_dir));
    let (cert_pem, key_pem) = cert_manager.ensure_certificate(&initial).unwrap_or_else(|e| fatal(format!("Failed to setup TLS certificate: {}", e)));
    if let Ok(fingerprint) = tlsutil::get_certificate_fingerprint(&cert_pem) {
        log::info!("TLS Certificate Fingerprint (SHA256): {}", fingerprint);
    }
    // Rebuild only when the SAN inputs change or the cached window lapses;
    // reading and parsing PEM on every handshake is a constant cost.
    let cache = Mutex::new(CertCache { key: certificate_cache_key(&initial, &[], &[]), validity: tlsutil::certificate_validity(&cert_pem) });
    let (resolver_engine, resolver_certs) = (engine.clone(), cert_manager.clone());
    let resolver = httpd::CertResolver::from_pem(&cert_pem, &key_pem)
        .unwrap_or_else(|e| fatal(format!("Failed to load TLS key pair: {}", e)))
        .with_refresh(Box::new(move || {
            let mut cache = cache.lock().unwrap_or_else(|e| e.into_inner());
            let active = resolver_engine.get_current_config();
            let (ips, dns) = pending_sans(&resolver_engine, &active);
            let key = certificate_cache_key(&active, &ips, &dns);
            if cache.key == key && cache.validity.is_some_and(|(nb, na)| tlsutil::certificate_time_valid(nb, na, Time::now())) {
                return None;
            }
            let (cert, private) = resolver_certs.ensure_certificate_with_additional_sans(&active, &ips, &dns).ok()?;
            cache.key = key;
            cache.validity = tlsutil::certificate_validity(&cert);
            Some((cert, private))
        }));
    let tls = Arc::new(resolver).server_config().unwrap_or_else(|e| fatal(format!("Failed to configure TLS: {}", e)));

    let port = initial.system.https_port;
    let listener = TcpListener::bind(("::", port as u16)).or_else(|_| TcpListener::bind(("0.0.0.0", port as u16))).unwrap_or_else(|e| fatal(format!("Refusing startup because management listener could not bind: {}", e)));
    if let Err(err) = signal_routerd_ready(initial.revision) {
        fatal(format!("Refusing startup because OpenRC readiness could not be published: {}", err));
    }
    log::info!("routerd listening on firewall-confined management endpoint https://{}:{}/api/v1/", initial.lan.ip_address, port);
    log::info!("Certificate fingerprint displayed above - verify on first connect");
    server.start_startup_capture(stop.clone());

    let router = Arc::new(router);
    let (handler_engine, pressure_dir) = (engine.clone(), abs_dir.clone());
    let handler: httpd::Handler = Arc::new(move |r: &mut Request<'_>| {
        if !management_destination_allowed(&handler_engine, r) {
            return Response::error("404 page not found", 404);
        }
        if mutation_needs_durable_write(&r.method, &r.path) {
            let status = storage::inspect(&pressure_dir);
            if status.available && !status.durable_writes_allowed {
                return minimalrouter::api::write_storage_pressure(&status);
            }
        }
        router.dispatch(r)
    });
    let options = httpd::ServerOptions { max_header_bytes: 32 << 10, ..Default::default() };
    if let Err(err) = httpd::serve(listener, Some(tls), handler, options, &stop) {
        fatal(format!("Server error: {}", err));
    }
    log::info!("Shutdown signal received; draining management connections");
    workers.join();
    if let Some(filter) = dns_filter {
        filter.close();
    }
    drop(guard);
    log::info!("Management plane stopped cleanly");
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn routing_guards() {
        assert!(mutation_needs_durable_write("PUT", "/api/v1/config"));
        assert!(!mutation_needs_durable_write("POST", "/api/v1/auth/login"));
        assert!(!mutation_needs_durable_write("POST", "/api/v1/snapshots/abc/preview"));
        assert!(!mutation_needs_durable_write("GET", "/api/v1/config"));
        assert!(accepts_gzip("br, gzip;q=0.5") && !accepts_gzip("gzip;q=0") && accepts_gzip("*") && !accepts_gzip("identity"));
        let hosts: HashSet<String> = ["192.168.1.1".to_string(), "router.lan".to_string()].into();
        assert!(request_host_allowed("192.168.1.1:443", &hosts) && request_host_allowed("Router.LAN.", &hosts) && !request_host_allowed("evil.example", &hosts));
        assert!(HASHED_ASSET.is_match("index-3f9a8b7c.js") && !HASHED_ASSET.is_match("index.js"));
    }
}
