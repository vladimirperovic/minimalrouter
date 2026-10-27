//! The REST API of Minimal Router OS (Go `internal/api`): authentication,
//! the trusted-network boundary, CSRF/Origin checks, configuration
//! transactions, recovery and every dashboard endpoint.

mod audit;
mod dns;
mod gateway;
mod lan;
mod observability;
mod recovery;
mod redaction;
mod routes;
mod setup;
mod wireguard;

pub use routes::*;

use serde::de::DeserializeOwned;
use serde_json::{json, Value};
use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex, MutexGuard, RwLock};
use std::time::Instant;

use crate::apply::{self, Engine, Transaction};
use crate::auth::{self, Session};
use crate::config::{self, SQLiteStore, SystemConfig};
use crate::dnsfilter;
use crate::goish::errors::{is, Result};
use crate::goish::net;
use crate::goish::time::{Duration, Time};
use crate::httpd::{media_type, Request, Response};
use crate::telemetry;

pub(crate) use redaction::{redact_config, redact_transaction, restore_redacted_config};

/// The session operations the API needs; both the in-memory and the SQLite
/// manager implement it.
pub trait SessionManagerInterface: Send + Sync {
    fn validate(&self, cookie: &str) -> Result<Session>;
    fn destroy(&self, cookie: &str) -> Result<()>;
    fn create_with_mode(&self, read_only: bool) -> Option<Session>;
    fn create_with_generation(&self, read_only: bool, expected_generation: u64) -> Result<Session>;
    fn destroy_all(&self) -> Result<()>;
    fn secure_cookies(&self) -> bool {
        true
    }
}

pub trait RateLimiterInterface: Send + Sync {
    fn allow(&self, ip: &str) -> bool;
}

impl SessionManagerInterface for auth::SessionManager {
    fn validate(&self, cookie: &str) -> Result<Session> {
        self.validate_session(cookie)
    }
    fn destroy(&self, cookie: &str) -> Result<()> {
        self.destroy_session(cookie);
        Ok(())
    }
    fn create_with_mode(&self, read_only: bool) -> Option<Session> {
        self.create_session(read_only)
    }
    fn create_with_generation(&self, read_only: bool, _expected_generation: u64) -> Result<Session> {
        self.create_session(read_only).ok_or_else(|| crate::errorf!("session creation failed"))
    }
    fn destroy_all(&self) -> Result<()> {
        self.destroy_all_sessions();
        Ok(())
    }
}

impl SessionManagerInterface for crate::auth_persistent::PersistentSessionManager {
    fn validate(&self, cookie: &str) -> Result<Session> {
        self.validate_session(cookie)
    }
    fn destroy(&self, cookie: &str) -> Result<()> {
        self.destroy_session(cookie)
    }
    fn create_with_mode(&self, read_only: bool) -> Option<Session> {
        self.create_session_with_mode(read_only)
    }
    fn create_with_generation(&self, read_only: bool, expected_generation: u64) -> Result<Session> {
        self.create_session_with_generation(read_only, expected_generation)
    }
    fn destroy_all(&self) -> Result<()> {
        self.destroy_all_sessions()
    }
    fn secure_cookies(&self) -> bool {
        self.secure_cookies
    }
}

impl RateLimiterInterface for auth::RateLimiter {
    fn allow(&self, ip: &str) -> bool {
        auth::RateLimiter::allow(self, ip)
    }
}

impl RateLimiterInterface for crate::auth_persistent::PersistentRateLimiter {
    fn allow(&self, ip: &str) -> bool {
        crate::auth_persistent::PersistentRateLimiter::allow(self, ip)
    }
}

/// http.SetCookie for the session cookie (Max-Age < 0 is written as 0).
fn cookie_header(value: &str, max_age: i64, secure: bool) -> String {
    format!(
        "{}={}; Path=/; Max-Age={}; HttpOnly{}; SameSite=Strict",
        auth::SESSION_COOKIE_NAME,
        value,
        max_age.max(0),
        if secure { "; Secure" } else { "" }
    )
}

#[derive(Clone)]
struct PendingTOTPEnrollment {
    secret: String,
    expires_at: Time,
}

#[derive(Clone)]
pub(crate) struct PendingImport {
    session_id: String,
    config: SystemConfig,
    expires_at: Time,
    kind: String,
    dns_policy: Option<dnsfilter::Policy>,
}

#[derive(Default)]
struct State {
    pending_totp: HashMap<String, PendingTOTPEnrollment>,
    pending_imports: HashMap<String, PendingImport>,
    totp_replay: HashMap<[u8; 32], Time>,
}

/// Bounds how stale a telemetry read may be: one dashboard tab polls the
/// system endpoint every two seconds, and each uncached read spawns several
/// short-lived processes.
const RUNTIME_SNAPSHOT_TTL: std::time::Duration = std::time::Duration::from_secs(1);

struct CachedRuntime {
    value: telemetry::RuntimeStatus,
    taken_at: Instant,
    wan: String,
    lan: String,
}

static RUNTIME_CACHE: Mutex<Option<CachedRuntime>> = Mutex::new(None);

/// Telemetry no older than the TTL; interface names are part of the key so
/// a configuration change never serves counters from the previous device.
pub(crate) fn runtime_snapshot(wan: &str, lan: &str, data_dir: &str) -> telemetry::RuntimeStatus {
    let mut cache = RUNTIME_CACHE.lock().unwrap_or_else(|e| e.into_inner());
    if let Some(cached) = cache.as_ref() {
        if cached.wan == wan && cached.lan == lan && cached.taken_at.elapsed() < RUNTIME_SNAPSHOT_TTL {
            return cached.value.clone();
        }
    }
    let value = telemetry::runtime_snapshot(wan, lan, data_dir);
    *cache = Some(CachedRuntime { value: value.clone(), taken_at: Instant::now(), wan: wan.into(), lan: lan.into() });
    value
}

pub(crate) fn appliance_data_dir() -> String {
    std::env::var("MINIMALROUTER_DATA_DIR").ok().filter(|v| !v.is_empty()).unwrap_or_else(|| "/var/lib/minimalrouter".into())
}

/// Optional subsystems attached after construction (Go keeps several of
/// these in package registries keyed by *Server).
#[derive(Default)]
pub(crate) struct Subsystems {
    pub gateway: Option<Arc<crate::gateway::Monitor>>,
    pub accounting: Option<Arc<crate::accounting::Store>>,
    pub dns_collector: Option<Arc<crate::accounting::DNSCollector>>,
    pub dns_risk: Option<Arc<crate::dnsrisk::Service>>,
    pub dns_filter: Option<Arc<dnsfilter::Service>>,
}

pub struct Server {
    pub(crate) engine: Arc<Engine>,
    session_mgr: Arc<dyn SessionManagerInterface>,
    rate_limiter: Arc<dyn RateLimiterInterface>,
    global_limiter: RwLock<Option<Arc<dyn RateLimiterInterface>>>,
    admin_hash: RwLock<String>,
    store: Option<Arc<SQLiteStore>>,
    firmware: RwLock<(Vec<u8>, String)>,
    state: Mutex<State>,
    preview_http: AtomicBool,
    pub(crate) subsystems: RwLock<Subsystems>,
    pub(crate) recovery_mu: Mutex<()>,
    audit_limiter: Mutex<audit::AuditThrottle>,
    pub(crate) ext: routes::Extensions,
}

crate::sentinel!(ErrAuthenticationStateUnavailable, "authentication state unavailable");

impl Server {
    /// An API server with the in-memory authentication subsystem.
    pub fn new(engine: Arc<Engine>) -> Arc<Server> {
        Self::build(engine, auth::SessionManager::new(), auth::RateLimiter::new(5, std::time::Duration::from_secs(60)), String::new(), None)
    }

    /// An API server with persistent authentication.
    pub fn with_auth(engine: Arc<Engine>, session_mgr: Arc<dyn SessionManagerInterface>, rate_limiter: Arc<dyn RateLimiterInterface>, admin_hash: &str, store: Option<Arc<SQLiteStore>>) -> Arc<Server> {
        Self::build(engine, session_mgr, rate_limiter, admin_hash.into(), store)
    }

    fn build(engine: Arc<Engine>, session_mgr: Arc<dyn SessionManagerInterface>, rate_limiter: Arc<dyn RateLimiterInterface>, admin_hash: String, store: Option<Arc<SQLiteStore>>) -> Arc<Server> {
        Arc::new(Server {
            engine,
            session_mgr,
            rate_limiter,
            global_limiter: RwLock::new(Some(auth::RateLimiter::new(100, std::time::Duration::from_secs(60)))),
            admin_hash: RwLock::new(admin_hash),
            store,
            firmware: RwLock::new((Vec::new(), String::new())),
            state: Mutex::new(State::default()),
            preview_http: AtomicBool::new(false),
            subsystems: RwLock::new(Subsystems::default()),
            recovery_mu: Mutex::new(()),
            audit_limiter: Mutex::new(audit::AuditThrottle::default()),
            ext: routes::Extensions::default(),
        })
    }

    fn state(&self) -> MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|e| e.into_inner())
    }

    pub(crate) fn subsystems(&self) -> std::sync::RwLockReadGuard<'_, Subsystems> {
        self.subsystems.read().unwrap_or_else(|e| e.into_inner())
    }

    pub(crate) fn subsystems_mut(&self) -> std::sync::RwLockWriteGuard<'_, Subsystems> {
        self.subsystems.write().unwrap_or_else(|e| e.into_inner())
    }

    /// Installs the immutable update trust anchor and the staging directory.
    /// An empty key leaves updates disabled.
    pub fn configure_firmware_trust(&self, key: &[u8], staging_dir: &str) {
        *self.firmware.write().unwrap_or_else(|e| e.into_inner()) = (key.to_vec(), staging_dir.into());
    }

    pub(crate) fn firmware_trust(&self) -> (Vec<u8>, String) {
        self.firmware.read().unwrap_or_else(|e| e.into_inner()).clone()
    }

    pub(crate) fn update_trust_configured(&self) -> bool {
        self.firmware_trust().0.len() == crate::firmware::PUBLIC_KEY_SIZE
    }

    /// A router-wide limit on top of the per-source limit.
    pub fn configure_global_login_limiter(&self, limiter: Option<Arc<dyn RateLimiterInterface>>) {
        *self.global_limiter.write().unwrap_or_else(|e| e.into_inner()) = limiter;
    }

    /// Permits an HTTP Origin only for an explicitly loopback-bound preview.
    pub fn configure_loopback_http_preview(&self, enabled: bool) {
        self.preview_http.store(enabled, Ordering::SeqCst);
    }

    pub fn configure_dns_filter(&self, service: Option<Arc<dnsfilter::Service>>) {
        self.subsystems_mut().dns_filter = service;
    }

    pub(crate) fn dns_filter(&self) -> Option<Arc<dnsfilter::Service>> {
        self.subsystems().dns_filter.clone()
    }

    pub(crate) fn credential_store(&self) -> Option<Arc<SQLiteStore>> {
        self.store.clone().or_else(|| self.engine.store().cloned())
    }

    pub(crate) fn engine_store(&self) -> Option<Arc<SQLiteStore>> {
        self.engine.store().cloned()
    }

    /// The session the auth middleware validated for this request; handlers
    /// never validate the cookie a second time.
    pub(crate) fn request_session(&self, r: &Request<'_>) -> Result<Session> {
        if let Some(session) = &r.session {
            return Ok(session.clone());
        }
        self.session_mgr.validate(&r.cookie(auth::SESSION_COOKIE_NAME).unwrap_or_default())
    }

    fn expired_cookie(&self) -> String {
        cookie_header("", -1, self.session_mgr.secure_cookies())
    }

    /// DestroySession: revokes the request's session; the caller attaches
    /// the returned expiring cookie.
    fn destroy_request_session(&self, r: &Request<'_>) -> Result<String> {
        if let Some(cookie) = r.cookie(auth::SESSION_COOKIE_NAME).filter(|c| !c.is_empty()) {
            self.session_mgr.destroy(&cookie)?;
        }
        Ok(self.expired_cookie())
    }

    // ── Middleware ──

    pub(crate) fn audit_request(&self, event: &str, r: &Request<'_>, extra: &[(&str, &str)]) {
        let mut details: HashMap<String, String> = [("method", r.method.as_str()), ("path", r.path.as_str())].into_iter().map(|(k, v)| (k.to_string(), v.to_string())).collect();
        details.extend(extra.iter().map(|(k, v)| (k.to_string(), v.to_string())));
        self.append_audit(event, &audit_actor(&r.remote_addr), details);
    }

    /// Trusted networks run before authentication: an outside source gets
    /// 403 without any login attempt. Loopback is always trusted and an
    /// unparsable source is denied.
    fn trusted_rejection(&self, r: &Request<'_>) -> Option<Response> {
        if self.engine.get_current_config().is_trusted_client_address(&r.remote_addr) {
            return None;
        }
        log::info!("[ACCESS] Rejected untrusted source {} for {} {}", r.remote_addr, r.method, r.path);
        self.audit_request("access.trusted_network_rejected", r, &[]);
        Some(Response::json(403, &json!({"error": "Source address is not within a trusted management network"})))
    }

    /// Session cookie, read-only enforcement, Fetch-Metadata, Origin and
    /// CSRF checks; every accepted mutation is audited with its status.
    fn authenticate(&self, r: &mut Request<'_>, handler: Handler) -> Response {
        let session = match self.session_mgr.validate(&r.cookie(auth::SESSION_COOKIE_NAME).unwrap_or_default()) {
            Ok(session) => session,
            Err(_) => {
                log::info!("[AUTH] Unauthorized request to {} from {}", r.path, r.remote_addr);
                self.audit_request("auth.unauthorized", r, &[]);
                return Response::json(401, &json!({"error": "Unauthorized or expired session"}));
            }
        };
        let safe = matches!(r.method.as_str(), "GET" | "HEAD" | "OPTIONS");
        if !safe {
            if session.read_only && r.path != "/api/v1/auth/logout" {
                log::info!("[AUTH] Read-only session rejected mutation {} {}", r.method, r.path);
                self.audit_request("auth.read_only_rejected", r, &[]);
                return Response::json(403, &json!({"error": "Read-only session cannot change router state"}));
            }
            if r.headers.get("Sec-Fetch-Site") == "cross-site" {
                self.audit_request("auth.cross_site_rejected", r, &[]);
                return Response::error("Cross-site request rejected", 403);
            }
            let origin = r.headers.get("Origin").to_string();
            if !origin.is_empty() && !self.origin_allowed(&origin, &r.host) {
                self.audit_request("auth.origin_rejected", r, &[]);
                return Response::error("Origin rejected", 403);
            }
            let token = r.headers.get(auth::CSRF_HEADER_NAME);
            if !bool::from(subtle::ConstantTimeEq::ct_eq(token.as_bytes(), session.csrf_token.as_bytes())) {
                log::info!("[AUTH] CSRF token mismatch on {} {} from {}", r.method, r.path, r.remote_addr);
                self.audit_request("auth.csrf_rejected", r, &[]);
                return Response::json(403, &json!({"error": "CSRF token mismatch"}));
            }
        }
        r.session = Some(session);
        let response = handler(self, r);
        if !safe {
            let status = response.status.to_string();
            self.audit_request("api.mutation", r, &[("status", &status)]);
        }
        response
    }

    /// url.Parse(origin): same host as the request and HTTPS, or HTTP from a
    /// loopback address when the preview is enabled.
    fn origin_allowed(&self, origin: &str, host: &str) -> bool {
        let Some((scheme, rest)) = origin.split_once("://") else { return false };
        if rest.contains(['/', '?', '#']) && !rest.ends_with('/') {
            let authority = rest.split(['/', '?', '#']).next().unwrap_or("");
            return authority == host && self.scheme_allowed(scheme, authority);
        }
        let authority = rest.trim_end_matches('/');
        authority == host && self.scheme_allowed(scheme, authority)
    }

    fn scheme_allowed(&self, scheme: &str, authority: &str) -> bool {
        if scheme == "https" {
            return true;
        }
        let hostname = authority.strip_prefix('[').and_then(|h| h.split_once(']').map(|(h, _)| h)).unwrap_or_else(|| authority.rsplit_once(':').map(|(h, _)| h).unwrap_or(authority));
        scheme == "http" && self.preview_http.load(Ordering::SeqCst) && net::parse_ip(hostname).is_some_and(|ip| ip.is_loopback())
    }

    pub(crate) fn gate(&self, r: &mut Request<'_>, authenticated: bool, handler: Handler) -> Response {
        let response = match self.trusted_rejection(r) {
            Some(rejection) => rejection,
            None if authenticated => self.authenticate(r, handler),
            None => handler(self, r),
        };
        security_headers(response)
    }

    // ── Credentials ──

    /// The administrator hash from the same source login uses; a recovery
    /// console reset in SQLite is honoured without a restart.
    pub(crate) fn admin_credential_hash(&self) -> Result<String> {
        if let Some(store) = &self.store {
            return match store.get_admin_auth_state() {
                Ok((hash, _)) => Ok(hash),
                Err(err) if is::<config::ErrNoRows>(&err) => Ok(String::new()),
                Err(err) => Err(err),
            };
        }
        Ok(self.admin_hash.read().unwrap_or_else(|e| e.into_inner()).clone())
    }

    /// An unreadable store reports "configured" so setup fails closed.
    pub(crate) fn administrator_configured(&self) -> bool {
        match self.admin_credential_hash() {
            Ok(hash) => !hash.is_empty(),
            Err(err) => {
                log::info!("[AUTH] Administrator state unavailable: {}", err);
                true
            }
        }
    }

    /// Ok(true) on a match; a busy KDF or unreadable store is an error, not
    /// a wrong password.
    pub(crate) fn verify_current_password(&self, password: &str) -> Result<bool> {
        let hash = self.admin_credential_hash().map_err(|err| {
            log::info!("[AUTH] Administrator state unavailable for re-authentication: {}", err);
            crate::goish::errors::Error::new(ErrAuthenticationStateUnavailable)
        })?;
        if hash.is_empty() {
            return Ok(false);
        }
        match auth::verify_password(password, &hash) {
            Err(err) if is::<crate::kdf::ErrBusy>(&err) => Err(err),
            result => Ok(result.unwrap_or(false)),
        }
    }

    fn consume_totp(&self, secret: &str, code: &str) -> bool {
        let key = auth::totp_replay_key(secret, code);
        let now = Time::now();
        let mut state = self.state();
        state.totp_replay.retain(|_, expires| !now.after(expires));
        if state.totp_replay.contains_key(&key) {
            return false;
        }
        state.totp_replay.insert(key, now.add(Duration::minutes(2)));
        true
    }
}

pub(crate) type Handler = fn(&Server, &mut Request<'_>) -> Response;

/// Strict headers on every API response; a handler's own value wins.
pub(crate) fn security_headers(mut response: Response) -> Response {
    for (name, value) in [
        ("Strict-Transport-Security", "max-age=63072000"),
        ("X-Content-Type-Options", "nosniff"),
        ("X-Frame-Options", "DENY"),
        ("Cross-Origin-Opener-Policy", "same-origin"),
        ("Cross-Origin-Resource-Policy", "same-origin"),
        ("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'"),
        ("Referrer-Policy", "no-referrer"),
        ("Permissions-Policy", "camera=(), microphone=(), geolocation=()"),
        ("Cache-Control", "no-store"),
    ] {
        if !response.headers.has(name) {
            response.headers.set(name, value);
        }
    }
    response
}

pub(crate) fn audit_actor(remote_addr: &str) -> String {
    match net::split_host_port(remote_addr).ok() {
        Some((host, _)) if !host.is_empty() => host,
        _ if remote_addr.is_empty() => "local".into(),
        _ => remote_addr.into(),
    }
}

pub(crate) fn remote_ip(remote_addr: &str) -> String {
    net::split_host_port(remote_addr).map(|(h, _)| h).unwrap_or_else(|_| remote_addr.to_string())
}

/// `writeGatewayJSON`: JSON with Cache-Control: no-store.
pub(crate) fn write_json<T: serde::Serialize + ?Sized>(status: u16, value: &T) -> Response {
    Response::json(status, value).with_header("Cache-Control", "no-store")
}

fn last_segment(path: &serde_ignored::Path<'_>) -> String {
    match path {
        serde_ignored::Path::Map { key, .. } => key.clone(),
        other => other.to_string(),
    }
}

/// decodeJSON: application/json only, at most 1 MiB, exactly one value, and
/// unknown fields rejected like `Decoder.DisallowUnknownFields`.
pub(crate) fn decode_json<T: DeserializeOwned>(r: &mut Request<'_>) -> std::result::Result<T, String> {
    let content_type = r.headers.get("Content-Type").to_string();
    if !content_type.is_empty() && !matches!(media_type(&content_type), Ok((m, _)) if m == "application/json") {
        return Err("content type must be application/json".into());
    }
    r.limit_body(1 << 20);
    let body = r.read_body().map_err(|e| e.to_string())?;
    let mut stream = serde_json::Deserializer::from_slice(&body).into_iter::<Value>();
    let value = match stream.next() {
        Some(Ok(value)) => value,
        Some(Err(err)) => return Err(err.to_string()),
        None => return Err("EOF".into()),
    };
    if stream.next().is_some() {
        return Err("request must contain exactly one JSON object".into());
    }
    let mut unknown: Option<String> = None;
    let decoded = serde_ignored::deserialize(crate::goish::decode::GoValue(&value), |path| {
        unknown.get_or_insert_with(|| last_segment(&path));
    })
    .map_err(|e| e.to_string())?;
    if let Some(field) = unknown {
        return Err(format!("json: unknown field \"{}\"", field));
    }
    Ok(decoded)
}

/// The single anti-lockout policy for every configuration mutation.
pub(crate) fn management_continuity_err(candidate: &SystemConfig, remote_addr: &str) -> std::result::Result<(), String> {
    wireguard_management_trust_err(candidate)?;
    if !candidate.is_trusted_client_address(remote_addr) {
        return Err("this change removes the caller's source address from trusted_networks and would lock out the administrator".into());
    }
    Ok(())
}

/// WireGuard-only management whose trust list does not cover the tunnel
/// subnet would admit the port only where the API trusts nobody.
pub(crate) fn wireguard_management_trust_err(candidate: &SystemConfig) -> std::result::Result<(), String> {
    if candidate.system.management_access != "wireguard_only" {
        return Ok(());
    }
    let Some((_, tunnel)) = net::parse_cidr(&candidate.wireguard.address) else {
        return Ok(());
    };
    if trusted_networks_cover(&candidate.trusted_networks, &tunnel) {
        return Ok(());
    }
    Err(format!("WireGuard-only management requires the WireGuard network {} in trusted_networks", tunnel))
}

fn trusted_networks_cover(trusted: &[String], target: &net::IPNet) -> bool {
    let (target_ones, target_bits) = target.mask.size();
    trusted.iter().filter_map(|n| net::parse_cidr(n)).any(|(_, candidate)| {
        let (ones, bits) = candidate.mask.size();
        bits == target_bits && ones <= target_ones && candidate.contains(&target.ip)
    })
}

/// A LAN address change is confirmed only when the request reached the
/// router at the candidate address.
fn confirm_via_candidate_lan(local_addr: &str, candidate_ip: &str) -> bool {
    if candidate_ip.is_empty() {
        return true;
    }
    net::split_host_port(local_addr).map(|(h, _)| h).unwrap_or_else(|_| local_addr.to_string()) == candidate_ip
}

pub(crate) fn management_url(cfg: &SystemConfig) -> String {
    let (scheme, port) = if cfg.system.https_enabled { ("https", cfg.system.https_port.to_string()) } else { ("http", "8080".to_string()) };
    format!("{}://{}/#network", scheme, net::join_host_port(&cfg.lan.ip_address, &port))
}

fn write_login_rejected() -> Response {
    Response::json(401, &json!({"error": "TOTP code required", "totp_required": "true"}))
}

fn write_session_required() -> Response {
    Response::json(401, &json!({"error": "Unauthorized or expired session"}))
}

fn write_credential_check_failure(err: Option<&crate::goish::errors::Error>) -> Response {
    match err {
        Some(err) if is::<crate::kdf::ErrBusy>(err) => Response::error("Authentication is busy; retry shortly", 503).with_header("Retry-After", "5"),
        Some(err) if is::<ErrAuthenticationStateUnavailable>(err) => Response::error("Authentication service unavailable", 503).with_header("Retry-After", "5"),
        _ => Response::error("Current administrator password is incorrect", 401),
    }
}

/// Upload bodies of multi-megabyte configurations (18 MiB at 0.5 Mbit/s).
pub(crate) const CONFIG_UPLOAD_READ_TIMEOUT: std::time::Duration = std::time::Duration::from_secs(300);

/// Replaces the server-wide read timeout for one authenticated upload.
pub(crate) fn extend_upload_read_deadline(r: &mut Request<'_>, timeout: std::time::Duration) {
    r.set_read_deadline(Instant::now() + timeout);
}

#[derive(serde::Deserialize, Default)]
#[serde(default)]
struct LoginRequest {
    password: String,
    totp_code: String,
    read_only: bool,
}

// ── Handlers from server.go ──

impl Server {
    pub(crate) fn handle_login(&self, r: &mut Request<'_>) -> Response {
        let ip = remote_ip(&r.remote_addr);
        log::info!("[AUTH] Login attempt from {}", ip);
        let global_ok = self.global_limiter.read().unwrap_or_else(|e| e.into_inner()).as_ref().is_none_or(|l| l.allow("all-login-sources"));
        if !self.rate_limiter.allow(&ip) || !global_ok {
            log::info!("[AUTH] Rate limited login from {}", ip);
            self.append_audit("auth.login_rate_limited", &ip, details(&[("result", "rejected")]));
            return Response::json(429, &json!({"error": "Too many login attempts. Try again later."}));
        }
        let Ok(req) = decode_json::<LoginRequest>(r) else {
            return Response::error("Invalid JSON", 400);
        };
        // The hash and generation come from one read, so a concurrent
        // credential change cannot slip in between.
        let (hash, generation) = match &self.store {
            Some(store) => match store.get_admin_auth_state() {
                Ok(state) => state,
                Err(err) if is::<config::ErrNoRows>(&err) => (String::new(), 0),
                Err(err) => {
                    log::info!("[AUTH] Authentication state unavailable for login from {}: {}", ip, err);
                    self.append_audit("auth.login_failed", &ip, details(&[("result", "authentication_store_unavailable")]));
                    return Response::json(503, &json!({"error": "Authentication service unavailable"}));
                }
            },
            None => (self.admin_hash.read().unwrap_or_else(|e| e.into_inner()).clone(), 0),
        };
        if hash.is_empty() {
            return Response::json(503, &json!({"error": "System not configured. Run the setup wizard first.", "is_configured": "false"}));
        }
        match auth::verify_password(&req.password, &hash) {
            Err(err) if is::<crate::kdf::ErrBusy>(&err) => {
                log::info!("[AUTH] Login from {} rejected: password verification capacity exhausted", ip);
                return Response::json(503, &json!({"error": "Authentication is busy. Try again shortly."})).with_header("Retry-After", "5");
            }
            Ok(true) => {}
            _ => {
                log::info!("[AUTH] Failed login from {}", ip);
                self.append_audit("auth.login_failed", &ip, details(&[("result", "invalid_credentials")]));
                return write_login_rejected();
            }
        }
        if let Some(store) = &self.store {
            match store.get_admin_totp_secret() {
                Err(err) => {
                    log::info!("[AUTH] TOTP state unavailable for login from {}: {}", ip, err);
                    self.append_audit("auth.login_failed", &ip, details(&[("result", "authentication_store_unavailable")]));
                    return Response::json(503, &json!({"error": "Authentication service unavailable"}));
                }
                Ok(secret) if !secret.is_empty() => {
                    if req.totp_code.is_empty() {
                        return write_login_rejected();
                    }
                    if !auth::validate_totp(&secret, &req.totp_code) || !self.consume_totp(&secret, &req.totp_code) {
                        log::info!("[AUTH] Invalid TOTP code from {}", ip);
                        self.append_audit("auth.login_failed", &ip, details(&[("result", "invalid_totp")]));
                        return write_login_rejected();
                    }
                }
                Ok(_) => {}
            }
        }
        let session = match self.session_mgr.create_with_generation(req.read_only, generation) {
            Ok(session) => session,
            Err(err) if is::<auth::ErrGenerationChanged>(&err) => {
                log::info!("[AUTH] Login from {} raced a credential change and was refused", ip);
                self.append_audit("auth.login_failed", &ip, details(&[("result", "credentials_changed")]));
                return write_login_rejected();
            }
            Err(_) => return Response::error("Could not create a durable session", 500),
        };
        log::info!("[AUTH] Successful login from {}", ip);
        self.append_audit("auth.login_succeeded", &ip, details(&[("mode", if session.read_only { "read_only" } else { "administrator" })]));
        Response::json(200, &json!({"success": true, "csrf_token": session.csrf_token, "read_only": session.read_only}))
            .with_header("Set-Cookie", cookie_header(&session.id, auth::ABSOLUTE_TIMEOUT.0 / 1_000_000_000, self.session_mgr.secure_cookies()))
    }

    pub(crate) fn handle_firmware_verify(&self, r: &mut Request<'_>) -> Response {
        #[derive(serde::Deserialize, Default)]
        #[serde(default)]
        struct Req {
            manifest_b64: String,
        }
        let Ok(req) = decode_json::<Req>(r) else { return Response::error("Invalid JSON", 400) };
        let Some(manifest_json) = crate::goish::base64::std_decode(&req.manifest_b64) else {
            return Response::error("Invalid manifest base64", 400);
        };
        let Ok(manifest) = crate::goish::json::unmarshal::<crate::firmware::FirmwareManifest>(&manifest_json) else {
            return Response::error("Invalid manifest JSON", 400);
        };
        let (key, staging) = self.firmware_trust();
        if key.len() != crate::firmware::PUBLIC_KEY_SIZE || staging.is_empty() {
            return Response::error("Firmware updates are disabled: no trusted signing key is installed", 503);
        }
        if let Err(err) = crate::firmware::validate_release_candidate(&staging, Some(&manifest), &key) {
            log::info!("[SECURITY] Firmware verification rejected: {}", err);
            return Response::json(422, &json!({"success": false, "valid": false}));
        }
        Response::json(200, &json!({"success": true, "valid": true, "version": manifest.version, "files_count": manifest.files().len()}))
    }

    pub(crate) fn handle_logout(&self, r: &mut Request<'_>) -> Response {
        log::info!("[AUTH] Logout from {}", r.remote_addr);
        match self.destroy_request_session(r) {
            Ok(cookie) => Response::json(200, &json!({"success": true})).with_header("Set-Cookie", cookie),
            Err(_) => Response::error("Session revocation could not be confirmed; retry", 503).with_header("Retry-After", "1"),
        }
    }

    pub(crate) fn handle_get_session(&self, r: &mut Request<'_>) -> Response {
        match self.request_session(r) {
            Ok(s) => Response::json(200, &json!({"authenticated": true, "csrf_token": s.csrf_token, "read_only": s.read_only, "created_at": s.created_at, "last_seen": s.last_seen})),
            Err(_) => write_session_required(),
        }
    }

    /// Revokes every session after a credential change, then the caller's.
    fn revoke_after_credential_change(&self, r: &Request<'_>, body: Value, failure: &str) -> Response {
        if self.session_mgr.destroy_all().is_err() {
            return Response::error(failure, 500);
        }
        match self.destroy_request_session(r) {
            Ok(cookie) => Response::json(200, &body).with_header("Set-Cookie", cookie),
            Err(_) => Response::error("Session revocation could not be confirmed; retry", 503).with_header("Retry-After", "1"),
        }
    }

    pub(crate) fn handle_change_password(&self, r: &mut Request<'_>) -> Response {
        #[derive(serde::Deserialize, Default)]
        #[serde(default)]
        struct Req {
            old_password: String,
            new_password: String,
        }
        let Ok(req) = decode_json::<Req>(r) else { return Response::error("Invalid JSON", 400) };
        match self.verify_current_password(&req.old_password) {
            Ok(true) => {}
            Ok(false) => return Response::json(401, &json!({"error": "Current password is incorrect"})),
            Err(err) => return write_credential_check_failure(Some(&err)),
        }
        let new_hash = match auth::hash_password(&req.new_password) {
            Ok(hash) => hash,
            Err(err) if is::<crate::kdf::ErrBusy>(&err) => return Response::error("Authentication is busy; retry shortly", 503).with_header("Retry-After", "5"),
            Err(err) => return Response::json(422, &json!({"error": err.to_string()})),
        };
        if let Some(store) = self.credential_store() {
            if store.set_admin_hash(&new_hash).is_err() {
                return Response::error("Failed to persist password", 500);
            }
        }
        *self.admin_hash.write().unwrap_or_else(|e| e.into_inner()) = new_hash;
        log::info!("[AUTH] Admin password changed from {}", r.remote_addr);
        self.revoke_after_credential_change(r, json!({"success": true}), "Password changed but session revocation failed")
    }

    pub(crate) fn handle_totp_enable(&self, r: &mut Request<'_>) -> Response {
        #[derive(serde::Deserialize, Default)]
        #[serde(default)]
        struct Req {
            code: String,
        }
        let Ok(req) = decode_json::<Req>(r) else { return Response::error("Invalid JSON", 400) };
        let Ok(session) = self.request_session(r) else { return write_session_required() };
        let pending = {
            let mut state = self.state();
            match state.pending_totp.get(&session.id).cloned() {
                Some(p) if Time::now().after(&p.expires_at) => {
                    state.pending_totp.remove(&session.id);
                    None
                }
                other => other,
            }
        };
        let Some(pending) = pending.filter(|p| auth::validate_totp(&p.secret, &req.code) && self.consume_totp(&p.secret, &req.code)) else {
            return Response::json(401, &json!({"error": "Invalid TOTP code"}));
        };
        if self.store.as_ref().is_none_or(|s| s.set_admin_totp_secret(&pending.secret).is_err()) {
            return Response::error("Failed to persist TOTP configuration", 500);
        }
        self.state().pending_totp.remove(&session.id);
        self.revoke_after_credential_change(r, json!({"success": true, "message": "TOTP enabled successfully"}), "TOTP enabled but session revocation failed")
    }

    pub(crate) fn handle_totp_qr(&self, r: &mut Request<'_>) -> Response {
        #[derive(serde::Deserialize, Default)]
        #[serde(default)]
        struct Req {
            current_password: String,
        }
        let Ok(req) = decode_json::<Req>(r) else { return Response::error("Invalid JSON", 400) };
        match self.verify_current_password(&req.current_password) {
            Ok(true) => {}
            Ok(false) => return write_credential_check_failure(None),
            Err(err) => return write_credential_check_failure(Some(&err)),
        }
        let Some(store) = &self.store else { return Response::error("TOTP store unavailable", 503) };
        match store.get_admin_totp_secret() {
            Err(_) => return Response::error("Failed to check TOTP status", 500),
            Ok(secret) if !secret.is_empty() => return Response::error("TOTP is already enabled", 409),
            Ok(_) => {}
        }
        let Ok(secret) = auth::generate_totp_secret() else { return Response::error("Failed to generate TOTP secret", 500) };
        let Ok(session) = self.request_session(r) else { return write_session_required() };
        self.state().pending_totp.insert(session.id, PendingTOTPEnrollment { secret: secret.clone(), expires_at: Time::now().add(Duration::minutes(10)) });
        Response::json(200, &json!({"enabled": false, "secret": secret, "qr_uri": auth::build_totp_uri("admin", &secret)}))
    }

    pub(crate) fn handle_totp_disable(&self, r: &mut Request<'_>) -> Response {
        #[derive(serde::Deserialize, Default)]
        #[serde(default)]
        struct Req {
            code: String,
            current_password: String,
        }
        let Ok(req) = decode_json::<Req>(r) else { return Response::error("Invalid JSON", 400) };
        match self.verify_current_password(&req.current_password) {
            Ok(true) => {}
            Ok(false) => return write_credential_check_failure(None),
            Err(err) => return write_credential_check_failure(Some(&err)),
        }
        let Some(store) = self.store.clone() else { return Response::json(401, &json!({"error": "Invalid TOTP code"})) };
        let valid = auth::verify_totp(&store, &req.code);
        let secret = store.get_admin_totp_secret();
        let accepted = matches!((&valid, &secret), (Ok(true), Ok(secret)) if self.consume_totp(secret, &req.code));
        if !accepted {
            return Response::json(401, &json!({"error": "Invalid TOTP code"}));
        }
        if auth::disable_totp(&store).is_err() {
            return Response::error("Failed to disable TOTP", 500);
        }
        self.revoke_after_credential_change(r, json!({"success": true, "message": "TOTP disabled successfully"}), "TOTP disabled but session revocation failed")
    }

    // ── Snapshots ──

    pub(crate) fn handle_get_snapshots(&self, r: &mut Request<'_>) -> Response {
        log::info!("[API] GET {} from {}", r.path, r.remote_addr);
        let Some(store) = self.engine_store() else { return Response::error("Snapshot history is unavailable", 503) };
        match store.list_snapshots() {
            Ok(snapshots) => Response::json(200, &json!({"snapshots": snapshots, "retention": {"manual": 20, "automatic": 20}})),
            Err(err) => {
                // An empty list would claim there is nothing to restore.
                log::info!("[API] Failed to list snapshots: {}", err);
                Response::error("Snapshot history is unavailable", 500)
            }
        }
    }

    pub(crate) fn handle_create_snapshot(&self, r: &mut Request<'_>) -> Response {
        log::info!("[API] POST {} from {}", r.path, r.remote_addr);
        let Some(store) = self.engine_store() else { return Response::error("Snapshot store unavailable", 500) };
        #[derive(serde::Deserialize, Default)]
        #[serde(default)]
        struct Req {
            label: String,
        }
        let mut req = Req::default();
        if r.has_body() {
            match decode_json::<Req>(r) {
                Ok(parsed) => req = parsed,
                Err(_) => return Response::error("Invalid snapshot request", 400),
            }
        }
        if let Err(err) = config::validate_snapshot_label(&req.label) {
            return Response::error(&err.to_string(), 422);
        }
        match store.create_manual_snapshot(&self.engine.get_current_config(), &req.label) {
            Ok(snap) => {
                log::info!("[API] Created snapshot {} (rev {})", snap.id, snap.revision);
                Response::json(200, &json!({"success": true, "snapshot": snap}))
            }
            Err(err) => {
                log::info!("[API] Failed to create snapshot: {}", err);
                Response::error(&format!("Snapshot creation failed: {}", err), 500)
            }
        }
    }

    pub(crate) fn handle_delete_snapshot(&self, r: &mut Request<'_>) -> Response {
        let id = r.path_value("id");
        log::info!("[API] DELETE /api/v1/snapshots/{} from {}", id, r.remote_addr);
        let Some(store) = self.engine_store() else { return Response::error("Snapshot store unavailable", 500) };
        match store.delete_snapshot(&id) {
            Ok(()) => Response::json(200, &json!({"success": true})),
            Err(err) => snapshot_read_error(&err),
        }
    }

    pub(crate) fn handle_restore_snapshot(&self, r: &mut Request<'_>) -> Response {
        let id = r.path_value("id");
        log::info!("[API] POST /api/v1/snapshots/{}/restore from {}", id, r.remote_addr);
        let Some(store) = self.engine_store() else { return Response::error("Snapshot store unavailable", 500) };
        let snap = match store.get_snapshot(&id) {
            Ok(snap) => snap,
            Err(err) => return snapshot_read_error(&err),
        };
        let Ok(mut restored) = crate::goish::json::unmarshal::<SystemConfig>(snap.config_json.as_bytes()) else {
            return Response::error("Corrupted snapshot config", 500);
        };
        restored.migrate_legacy_fields();
        let current = self.engine.get_current_config();
        #[derive(serde::Deserialize, Default)]
        #[serde(default)]
        struct Req {
            expected_revision: Option<config::Revision>,
        }
        let mut req = Req::default();
        if r.has_body() {
            match decode_json::<Req>(r) {
                Ok(parsed) => req = parsed,
                Err(_) => return Response::error("Invalid restore request", 400),
            }
        }
        let Some(expected) = req.expected_revision else {
            return Response::error("Preview this snapshot first and supply expected_revision", 428);
        };
        if expected != current.revision {
            return Response::error("Configuration changed after preview; preview again", 409);
        }
        restored.revision = current.revision;
        let assessment = self.assess_recovery(&restored, &r.remote_addr);
        if !assessment.can_apply {
            return write_json(422, &json!({"error": "Restore is blocked", "assessment": assessment}));
        }
        self.perform_recovery(r, restored, "snapshot", None)
    }

    // ── pfSense migration ──

    pub(crate) fn handle_pfsense_import_preview(&self, r: &mut Request<'_>) -> Response {
        let content_type = r.headers.get("Content-Type").to_string();
        if !matches!(media_type(&content_type), Ok((m, _)) if m == "application/xml" || m == "text/xml") {
            return Response::error("Content-Type must be application/xml", 415);
        }
        extend_upload_read_deadline(r, CONFIG_UPLOAD_READ_TIMEOUT);
        r.limit_body(8 << 20);
        let Ok(body) = r.read_body() else { return Response::error("pfSense configuration is too large", 413) };
        let query = r.query();
        let mapping = config::PfSenseInterfaceMapping { wan: query.get("wan"), lan: query.get("lan") };
        match config::import_pfsense_xml_with_mapping(&body, &mapping) {
            Ok(report) => {
                let candidate = report.config.clone();
                self.recovery_preview(r, candidate, "pfsense", None, Some(report))
            }
            Err((_, err)) => Response::json(422, &json!({"error": err.to_string()})),
        }
    }

    pub(crate) fn handle_pfsense_import_apply(&self, r: &mut Request<'_>) -> Response {
        self.apply_recovery_preview(r, "pfsense")
    }

    pub(crate) fn handle_recovery_reconcile(&self, _r: &mut Request<'_>) -> Response {
        match self.engine.reconcile(Instant::now() + apply::RECONCILE_BUDGET) {
            Ok(()) => Response::json(200, &json!({"success": true, "status": self.engine.get_status()})),
            Err(err) => Response::json(503, &json!({"error": err.to_string(), "status": self.engine.get_status()})),
        }
    }

    // ── System ──

    pub(crate) fn handle_get_system(&self, r: &mut Request<'_>) -> Response {
        log::info!("[API] GET {} from {}", r.path, r.remote_addr);
        let cfg = self.engine.get_current_config();
        let status = self.engine.get_status();
        let runtime = runtime_snapshot(&cfg.wan.interface, cfg.runtime_lan_interface(), &appliance_data_dir());
        let connection = if cfg.wan.enabled && runtime.wan_connected { "Connected" } else { "Disconnected" };
        Response::json(
            200,
            &json!({
                "status": connection,
                "version": crate::buildinfo::display_version(),
                "git_commit": crate::buildinfo::COMMIT,
                "build_date": crate::buildinfo::BUILD_DATE,
                "hostname": cfg.system.hostname,
                "domain": cfg.system.domain,
                "wan_iface": cfg.wan.interface,
                "wan_enabled": cfg.wan.enabled,
                "lan_ip": cfg.lan.ip_address,
                "mtu": cfg.wan.mtu,
                "revision": cfg.revision,
                "runtime": runtime,
                "update_trust_configured": self.update_trust_configured(),
                "apply_in_progress": status.applying,
                "recovery_required": status.recovery_required,
                "recovery_reason": status.recovery_reason,
                "transaction_id": status.active_transaction_id,
                "transaction_state": status.active_state,
                "timestamp": Time::now().unix(),
            }),
        )
    }

    pub(crate) fn handle_get_diagnostics(&self, r: &mut Request<'_>) -> Response {
        log::info!("[API] GET {} from {}", r.path, r.remote_addr);
        match self.build_recovery_diagnostics(&self.engine.get_current_config()) {
            Ok(data) => Response::bytes(200, "application/json", data).with_header("Content-Disposition", "attachment; filename=\"minimalrouter-diagnostics.json\""),
            Err(_) => Response::error("Failed to build diagnostic bundle", 500),
        }
    }

    // ── Configuration ──

    pub(crate) fn handle_get_config(&self, r: &mut Request<'_>) -> Response {
        log::info!("[API] GET {} from {}", r.path, r.remote_addr);
        Response::json(200, &redact_config(&self.engine.get_current_config()))
    }

    pub(crate) fn handle_update_config(&self, r: &mut Request<'_>) -> Response {
        let candidate = match decode_json::<SystemConfig>(r) {
            Ok(cfg) => cfg,
            Err(err) => return Response::error(&format!("Invalid JSON body: {}", err), 400),
        };
        let current = self.engine.get_current_config();
        let candidate = restore_redacted_config(&candidate, &current);
        if management_continuity_err(&candidate, &r.remote_addr).is_err() {
            // The route is fixed; logging the request path would let a
            // caller inject arbitrary text into the log.
            log::info!("[API] PUT /api/v1/config - Rejected management access change that would lock out {}", r.remote_addr);
            self.append_audit("config.lockout_prevented", &audit_actor(&r.remote_addr), details(&[("path", &r.path)]));
            let message = wireguard_management_trust_err(&candidate).err().unwrap_or_else(|| "This change would remove your own source address from trusted_networks and lock you out".into());
            return Response::json(403, &json!({"error": message}));
        }
        let tx_id = format!("tx-{}", Time::now().unix_nano());
        let (tx, result) = self.engine.process_transaction(&tx_id, candidate);
        self.audit_config_request(r, Some(&tx));
        if let Err(err) = result {
            log::info!("[API] PUT {} - Transaction {} REJECTED: {}", r.path, tx_id, err);
            return Response::json(422, &json!({"error": err.to_string(), "tx": redact_transaction(Some(&tx))}));
        }
        log::info!("[API] PUT {} - Transaction {} {} (Rev: {})", r.path, tx_id, tx.current_state.as_str(), tx.config.revision);
        let status = if tx.current_state == apply::State::AwaitingConfirmation { 202 } else { 200 };
        Response::json(status, &redact_transaction(Some(&tx)))
    }

    pub(crate) fn handle_confirm_transaction(&self, r: &mut Request<'_>) -> Response {
        let id = r.path_value("id");
        let Some(pending) = self.engine.get_pending_transaction().filter(|p| p.id == id) else {
            return Response::error("Transaction confirmation failed", 409);
        };
        if pending.config.system.management_access == "wireguard_only" {
            let peer = net::parse_ip(&remote_ip(&r.remote_addr));
            let network = net::parse_cidr(&pending.config.wireguard.address).map(|(_, n)| n);
            if !matches!((peer, network), (Some(ip), Some(n)) if n.contains(&ip)) {
                return Response::error("WireGuard connectivity must be used to confirm this change", 403);
            }
        } else if !pending.config.lan.ip_address.is_empty() && pending.config.lan.ip_address != self.engine.get_current_config().lan.ip_address {
            // The administrator must prove the new management path.
            if !confirm_via_candidate_lan(&r.local_addr, &pending.config.lan.ip_address) {
                return Response::error("Reach the router at the new LAN address to confirm this change", 403);
            }
        }
        let (tx, result) = self.engine.confirm_transaction(&id);
        self.audit_config_request(r, tx.as_ref());
        match result {
            // The engine's reason is operator guidance, not a secret.
            Err(err) => Response::json(409, &json!({"error": format!("Transaction confirmation failed: {}", err)})),
            Ok(()) => Response::json(200, &redact_transaction(tx.as_ref())),
        }
    }

    pub(crate) fn handle_get_pending_transaction(&self, _r: &mut Request<'_>) -> Response {
        match self.engine.get_pending_transaction() {
            None => Response::json(200, &json!({"pending": false})),
            Some(tx) => Response::json(
                200,
                &json!({
                    "pending": true,
                    "id": tx.id,
                    "state": tx.current_state,
                    "confirmation_deadline": tx.confirmation_deadline,
                    "management_access": tx.config.system.management_access,
                    "management_url": management_url(&tx.config),
                }),
            ),
        }
    }

    // ── Encrypted backups ──

    pub(crate) fn handle_backup_export(&self, r: &mut Request<'_>) -> Response {
        #[derive(serde::Deserialize, Default)]
        #[serde(default)]
        struct Req {
            current_password: String,
            backup_passphrase: String,
        }
        let Ok(req) = decode_json::<Req>(r) else { return Response::error("Invalid JSON", 400) };
        match self.verify_current_password(&req.current_password) {
            Ok(true) => {}
            Ok(false) => return write_credential_check_failure(None),
            Err(err) => return write_credential_check_failure(Some(&err)),
        }
        // A separate password from a stale dashboard is refused explicitly.
        if !req.backup_passphrase.is_empty() && req.backup_passphrase != req.current_password {
            return Response::error("New backups use your dashboard password; refresh the dashboard before exporting", 422);
        }
        let mut policy = None;
        if let Some(filter) = self.dns_filter() {
            match filter.status(Instant::now() + std::time::Duration::from_secs(30)) {
                Ok(status) if !status.updating => policy = Some(status.applied.policy),
                _ => return Response::error("DNS protection is unavailable or changing; retry the backup when it settles", 503),
            }
        }
        match crate::recoverybackup::encrypt(&self.engine.get_current_config(), policy.as_ref(), &req.current_password) {
            Ok(encrypted) => Response::bytes(200, "application/vnd.minimalrouter.backup+json", encrypted)
                .with_header("Content-Disposition", "attachment; filename=\"minimalrouter-backup.mrbak\"")
                .with_header("Cache-Control", "no-store"),
            Err(err) if is::<crate::kdf::ErrBusy>(&err) => Response::error("Backup encryption is busy; retry shortly", 503).with_header("Retry-After", "5"),
            Err(err) => Response::error(&err.to_string(), 422),
        }
    }

    pub(crate) fn handle_backup_import_preview(&self, r: &mut Request<'_>) -> Response {
        extend_upload_read_deadline(r, CONFIG_UPLOAD_READ_TIMEOUT);
        r.limit_body(18 << 20);
        let Ok(form) = crate::httpd::read_form(r, 18 << 20) else {
            return Response::error("Invalid or oversized backup upload", 400);
        };
        let current_password = form.value("current_password");
        let mut passphrase = form.value("backup_passphrase");
        if passphrase.is_empty() {
            passphrase = current_password.clone();
        }
        match self.verify_current_password(&current_password) {
            Ok(true) => {}
            Ok(false) => return write_credential_check_failure(None),
            Err(err) => return write_credential_check_failure(Some(&err)),
        }
        let Some(data) = form.file("backup") else { return Response::error("Encrypted backup file is required", 400) };
        if data.len() > 16 << 20 {
            return Response::error("Encrypted backup is too large", 413);
        }
        match crate::recoverybackup::decrypt(data, &passphrase) {
            Ok(payload) => self.recovery_preview(r, payload.config, "backup", payload.dns_filter, None),
            Err(_) => Response::error("Backup could not be authenticated or validated", 422),
        }
    }

    pub(crate) fn handle_backup_import_apply(&self, r: &mut Request<'_>) -> Response {
        self.apply_recovery_preview(r, "backup")
    }

    pub(crate) fn handle_wake_on_lan(&self, r: &mut Request<'_>) -> Response {
        #[derive(serde::Deserialize, Default)]
        #[serde(default)]
        struct Req {
            mac: String,
        }
        let Ok(req) = decode_json::<Req>(r) else { return Response::error("Invalid JSON", 400) };
        // A magic packet repeats a 48-bit Ethernet address; EUI-64 and IPoIB
        // forms would be truncated into a packet no card recognises.
        let Some(mac) = net::parse_mac(&req.mac).filter(|m| m.len() == 6) else {
            return Response::error("Invalid MAC address: a 48-bit Ethernet address is required", 400);
        };
        let mut packet = vec![0xFFu8; 6];
        for _ in 0..16 {
            packet.extend_from_slice(&mac);
        }
        // The LAN subnet broadcast keeps the packet on the local segment.
        let broadcast = net::parse_cidr(&self.engine.get_current_config().lan.cidr).and_then(|(_, n)| n.ipv4_broadcast()).map(|ip| ip.to_string()).unwrap_or_else(|| "255.255.255.255".into());
        let send = || -> std::io::Result<()> {
            let socket = std::net::UdpSocket::bind("0.0.0.0:0")?;
            socket.set_broadcast(true)?;
            socket.connect(format!("{}:9", broadcast))?;
            socket.send(&packet)?;
            Ok(())
        };
        match send() {
            Ok(()) => Response::new(204),
            Err(err) => Response::error(&format!("WoL send failed: {}", err), 500),
        }
    }

    pub(crate) fn audit_config_request(&self, r: &Request<'_>, tx: Option<&Transaction>) {
        if let Some(tx) = tx {
            self.append_audit(
                "config.request",
                &audit_actor(&r.remote_addr),
                details(&[("transaction_id", &tx.id), ("candidate_revision", &tx.config.revision.to_string()), ("method", &r.method), ("path", &r.path)]),
            );
        }
    }
}

pub(crate) fn details(pairs: &[(&str, &str)]) -> HashMap<String, String> {
    pairs.iter().map(|(k, v)| (k.to_string(), v.to_string())).collect()
}

pub(crate) fn snapshot_read_error(err: &crate::goish::errors::Error) -> Response {
    if is::<config::ErrSnapshotNotFound>(err) {
        Response::error("Snapshot not found", 404)
    } else if is::<config::ErrSnapshotCorrupt>(err) {
        Response::error("Snapshot integrity check failed; this restore point cannot be used", 422)
    } else {
        Response::error("Snapshot storage unavailable; retry after checking appliance health", 503)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn decode_json_rejects_unknown_and_trailing_values() {
        #[derive(serde::Deserialize, Default, Debug)]
        #[serde(default)]
        struct Req {
            password: String,
        }
        let run = |body: &str, content_type: &str| {
            let mut cursor = std::io::Cursor::new(body.as_bytes().to_vec());
            let mut r = Request::synthetic("POST", "/x", &mut cursor, body.len() as u64);
            if !content_type.is_empty() {
                r.headers.set("Content-Type", content_type);
            }
            decode_json::<Req>(&mut r).map(|v| v.password)
        };
        assert_eq!(run(r#"{"Password":"a"}"#, "application/json; charset=utf-8").unwrap(), "a");
        assert_eq!(run(r#"{"password":"a","extra":1}"#, "").unwrap_err(), "json: unknown field \"extra\"");
        assert_eq!(run(r#"{"password":"a"} {}"#, "").unwrap_err(), "request must contain exactly one JSON object");
        assert_eq!(run(r#"{"password":"a"}"#, "text/plain").unwrap_err(), "content type must be application/json");
    }

    #[test]
    fn origin_and_continuity_rules() {
        let engine = Engine::new(config::default_config(), None);
        let server = Server::new(engine);
        assert!(server.origin_allowed("https://192.168.1.1:8443", "192.168.1.1:8443"));
        assert!(!server.origin_allowed("http://192.168.1.1:8443", "192.168.1.1:8443"));
        assert!(!server.origin_allowed("https://evil.example", "192.168.1.1:8443"));
        server.configure_loopback_http_preview(true);
        assert!(server.origin_allowed("http://127.0.0.1:8080", "127.0.0.1:8080"));
        let mut cfg = config::default_config();
        cfg.system.management_access = "wireguard_only".into();
        cfg.wireguard.address = "10.6.0.1/24".into();
        cfg.trusted_networks = vec!["192.168.1.0/24".to_string()].into();
        assert_eq!(wireguard_management_trust_err(&cfg).unwrap_err(), "WireGuard-only management requires the WireGuard network 10.6.0.0/24 in trusted_networks");
        cfg.trusted_networks.push("10.0.0.0/8".into());
        assert!(wireguard_management_trust_err(&cfg).is_ok());
        assert!(confirm_via_candidate_lan("192.168.2.1:443", "192.168.2.1") && !confirm_via_candidate_lan("192.168.1.1:443", "192.168.2.1"));
    }
}
