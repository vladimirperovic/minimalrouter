//! What the management API exposes WITHOUT credentials, proven in-process.
//!
//! Already covered elsewhere (not repeated here):
//! * `api_e2e.rs`: one protected route (`GET /system`) answers 401, `setup/status`
//!   reports `is_configured:false` before setup, `setup/apply` answers 403 after.
//! * `api_contract_matrix.rs` (3,000 cases over TLS from loopback): forged-cookie
//!   401s on every protected route, read-only/CSRF/Origin/cross-site 403s.
//!
//! Added here (all through `Router::dispatch` with `Request::synthetic`, so the
//! peer address is under the test's control, which a loopback socket is not):
//! * the route list is DERIVED from `routes.rs`, with guards that a route cannot
//!   be registered some other way and that exactly four routes are public;
//! * every protected route answers 401 with no/forged session cookie, before any
//!   CSRF/Origin check, and the handler demonstrably never ran;
//! * a source outside `trusted_networks` gets 403 on EVERY route (public ones
//!   included), even holding a valid session cookie and CSRF token, and
//!   forwarding headers do not change that;
//! * setup is refused once an administrator exists (state proven unchanged), and
//!   an untrusted source cannot run first-run setup either;
//! * the exact fields `setup/status` and `setup/interfaces` disclose before and
//!   after setup.
//!
//! Limits: in-process only. No TLS, no real socket, no static dashboard files
//! (`/` is registered by `routerd`, not by `register_routes`).

use std::collections::BTreeSet;
use std::path::PathBuf;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, LazyLock};
use std::time::Instant;

use minimalrouter::api::{RateLimiterInterface, Server, SessionManagerInterface};
use minimalrouter::apply::{ApplyRequest, ApplyResponse, Client, Engine};
use minimalrouter::auth_persistent::{PersistentRateLimiter, PersistentSessionManager};
use minimalrouter::config::{AuditQuery, SQLiteStore, default_config};
use minimalrouter::goish::errors::Result;
use minimalrouter::httpd::{Request, Router};
use regex::Regex;
use serde_json::Value;

// ───────────────────────── route inventory ─────────────────────────

const ROUTES_SRC: &str = include_str!("../crates/mr-api/src/api/routes.rs");

#[derive(Clone, Debug)]
struct Route {
    method: String,
    pattern: String,
    public: bool,
}

impl Route {
    /// A concrete path for the pattern; the gate answers before any handler looks at it.
    fn path(&self) -> String {
        Regex::new(r"\{[^}]+\}").unwrap().replace_all(&self.pattern, "preauth-probe").into_owned()
    }

    fn label(&self) -> String {
        format!("{} {}", self.method, self.pattern)
    }
}

/// Parses the two route tables out of `register_routes`.
fn routes() -> Vec<Route> {
    let (head, protected_part) = ROUTES_SRC.split_once("let protected:").expect("routes.rs no longer has a `let protected:` table");
    let (_, public_part) = head.split_once("let public:").expect("routes.rs no longer has a `let public:` table");
    let tuple = Regex::new(r#"\("(GET|POST|PUT|PATCH|DELETE) (/api/v1/[^"]+)",\s*Server::(\w+)\)"#).unwrap();
    let parse = |text: &str, public: bool| -> Vec<Route> {
        tuple.captures_iter(text).map(|c| Route { method: c[1].to_string(), pattern: c[2].to_string(), public }).collect()
    };
    let mut all = parse(public_part, true);
    all.extend(parse(protected_part, false));
    all
}

// ───────────────────────── rig ─────────────────────────

/// Counts every request that reaches the privileged boundary.
struct Helper(AtomicUsize);

impl Client for Helper {
    fn apply(&self, _deadline: Instant, req: &ApplyRequest) -> Result<ApplyResponse> {
        self.0.fetch_add(1, Ordering::SeqCst);
        Ok(ApplyResponse { id: req.id.clone(), success: true, verified: true, ..Default::default() })
    }
}

struct TempDir(PathBuf);

impl Drop for TempDir {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

// A throw-away fixture credential for this test file only.
const PASSWORD: &str = "preauth fixture passphrase";
static HASH: LazyLock<String> = LazyLock::new(|| minimalrouter::auth::hash_password(PASSWORD).unwrap());

struct Reply {
    status: u16,
    body: String,
    set_cookie: String,
    hsts: bool,
}

impl Reply {
    fn json(&self) -> Value {
        serde_json::from_str(self.body.trim()).unwrap_or_else(|e| panic!("not JSON ({e}): {}", self.body))
    }
}

struct State {
    revision: u64,
    admin: Option<(String, u64)>,
    snapshots: usize,
}

/// Field order matters: the temp dir must outlive the SQLite handles.
struct Rig {
    router: Router,
    store: Arc<SQLiteStore>,
    helper: Arc<Helper>,
    _dir: TempDir,
}

fn rig(tag: &str, configured: bool) -> Rig {
    let dir = std::env::temp_dir().join(format!("mr-preauth-{tag}-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);
    let guard = TempDir(dir.clone());
    let store = Arc::new(SQLiteStore::new_file_store(dir.to_str().unwrap()).unwrap());
    let engine_cfg = if configured {
        let mut cfg = default_config();
        cfg.revision = store.get_latest_config().unwrap().revision + 1;
        store.commit_initial_setup(cfg, &HASH).unwrap();
        store.get_latest_config().unwrap()
    } else {
        default_config()
    };
    let helper = Arc::new(Helper(AtomicUsize::new(0)));
    let engine = Engine::with_client(engine_cfg, Some(store.clone()), helper.clone());
    let sessions: Arc<dyn SessionManagerInterface> = PersistentSessionManager::with_secure_cookies(store.clone(), false);
    let limiter: Arc<dyn RateLimiterInterface> = PersistentRateLimiter::new(store.clone(), 50, minimalrouter::goish::time::Duration::secs(60));
    let server = Server::with_auth(engine, sessions, limiter, if configured { &HASH } else { "" }, Some(store.clone()));
    let mut router = Router::new();
    server.register_routes(&mut router);
    // The router's handlers hold the only needed `Arc<Server>` clones.
    Rig { router, store, helper, _dir: guard }
}

const LOOPBACK: &str = "127.0.0.1:50000";
const TRUSTED_LAN: &str = "192.168.1.50:40000";

fn call(rig: &Rig, method: &str, target: &str, remote: &str, headers: &[(&str, &str)], body: &str) -> Reply {
    let mut cursor = std::io::Cursor::new(body.as_bytes().to_vec());
    let mut request = Request::synthetic(method, target, &mut cursor, body.len() as u64);
    request.remote_addr = remote.into();
    for (name, value) in headers {
        request.headers.set(name, *value);
    }
    let response = rig.router.dispatch(&mut request);
    Reply {
        status: response.status,
        body: String::from_utf8_lossy(response.body_bytes()).into_owned(),
        set_cookie: response.headers.get("Set-Cookie").to_string(),
        hsts: response.headers.has("Strict-Transport-Security"),
    }
}

fn state(rig: &Rig) -> State {
    State {
        revision: rig.store.get_latest_config().unwrap().revision,
        admin: rig.store.get_admin_auth_state().ok(),
        snapshots: rig.store.list_snapshots().unwrap().len(),
    }
}

fn audit_count(rig: &Rig, event_type: &str) -> usize {
    rig.store.query_audit_events(&AuditQuery { limit: 100, event_type: event_type.into(), ..Default::default() }).unwrap().events.len()
}

fn assert_nothing_ran(rig: &Rig, before: &State, what: &str) {
    let after = state(rig);
    assert_eq!(rig.helper.0.load(Ordering::SeqCst), 0, "{what}: a request reached the privileged helper");
    assert_eq!(after.revision, before.revision, "{what}: the configuration revision moved");
    assert_eq!(after.admin, before.admin, "{what}: the administrator credential state changed");
    assert_eq!(after.snapshots, before.snapshots, "{what}: a snapshot was created");
    assert_eq!(audit_count(rig, "api.mutation"), 0, "{what}: a mutation handler ran (api.mutation audited)");
    assert_eq!(audit_count(rig, "system.setup_completed"), 0, "{what}: setup completed");
}

const UNAUTHORIZED: &str = r#"{"error":"Unauthorized or expired session"}"#;
const UNTRUSTED: &str = r#"{"error":"Source address is not within a trusted management network"}"#;

// ───────────────────────── tests ─────────────────────────

#[test]
fn route_inventory_is_derived_from_the_registration_source() {
    let all = routes();
    let public: BTreeSet<String> = all.iter().filter(|r| r.public).map(Route::label).collect();
    let documented: BTreeSet<String> =
        ["POST /api/v1/auth/login", "GET /api/v1/setup/interfaces", "GET /api/v1/setup/status", "POST /api/v1/setup/apply"].map(String::from).into();
    assert_eq!(public, documented, "the set of public (unauthenticated) routes changed; review it before updating this list");
    // A floor, not a pin: it only has to notice a parser that lost most of the table.
    assert!(all.iter().filter(|r| !r.public).count() >= 80, "protected route table looks truncated: {} routes", all.len());

    let labels: BTreeSet<String> = all.iter().map(Route::label).collect();
    assert_eq!(labels.len(), all.len(), "duplicate route in routes.rs");

    // The table parser must not have skipped a tuple, and nothing may register a
    // handler except through the two gated loops.
    assert_eq!(ROUTES_SRC.matches("Server::handle_").count(), all.len(), "a `Server::handle_*` entry in routes.rs was not parsed as a route");
    assert_eq!(ROUTES_SRC.matches("router.handle_fn(").count(), 1, "routes.rs registers a handler outside Server::route (ungated?)");
    assert_eq!(ROUTES_SRC.matches("self.route(router, pattern, false, handler)").count(), 1);
    assert_eq!(ROUTES_SRC.matches("self.route(router, pattern, true, handler)").count(), 1);
    assert!(ROUTES_SRC.contains("server.gate(r, authenticated, handler)"), "Server::route no longer wraps every handler in the gate");

    // Every parsed route really is registered (proves the parse reaches the router),
    // and the probe can tell registered from unregistered.
    let rig = rig("inventory", true);
    for route in &all {
        let reply = call(&rig, &route.method, &route.path(), "198.51.100.9:1", &[], "");
        assert_eq!(reply.status, 403, "{} is not registered (status {})", route.label(), reply.status);
    }
    assert_eq!(call(&rig, "GET", "/api/v1/not-a-route", "198.51.100.9:1", &[], "").status, 404);
    assert_eq!(call(&rig, "DELETE", "/api/v1/config", "198.51.100.9:1", &[], "").status, 405, "a wrong method is a router 405, never a handler");
    eprintln!("route inventory: {} routes ({} public, {} protected)", all.len(), public.len(), all.len() - public.len());
}

#[test]
fn protected_routes_answer_401_without_a_session_and_never_run_a_handler() {
    let all: Vec<Route> = routes().into_iter().filter(|r| !r.public).collect();
    let mut requests = 0;
    for configured in [true, false] {
        let rig = rig(if configured { "unauth-configured" } else { "unauth-fresh" }, configured);
        let before = state(&rig);
        for remote in [LOOPBACK, TRUSTED_LAN] {
            for route in &all {
                let zeros = format!("{}={}", minimalrouter::auth::SESSION_COOKIE_NAME, "0".repeat(64));
                let empty = format!("{}=", minimalrouter::auth::SESSION_COOKIE_NAME);
                let mut variants: Vec<(&str, Vec<(&str, &str)>)> = vec![
                    ("no cookie", vec![]),
                    ("empty session cookie", vec![("Cookie", empty.as_str())]),
                    ("well-formed but unknown session", vec![("Cookie", zeros.as_str())]),
                    ("other cookie name", vec![("Cookie", "unrelated=1")]),
                ];
                if route.method != "GET" {
                    // Authentication is decided before CSRF/Origin/Fetch-Metadata: a
                    // hostile cross-site request without a session is still just a 401.
                    variants.push((
                        "cross-site, bad CSRF, no session",
                        vec![("X-CSRF-Token", "x"), ("Origin", "https://evil.example"), ("Sec-Fetch-Site", "cross-site")],
                    ));
                }
                for (name, headers) in variants {
                    let reply = call(&rig, &route.method, &route.path(), remote, &headers, "{}");
                    requests += 1;
                    assert_eq!(reply.status, 401, "[configured={configured}] {} from {remote} ({name}) -> {} {}", route.label(), reply.status, reply.body);
                    assert_eq!(reply.body.trim(), UNAUTHORIZED, "[configured={configured}] {} ({name}): not the gate's own 401 body", route.label());
                    assert!(reply.hsts, "{}: security headers missing on a 401", route.label());
                    assert!(reply.set_cookie.is_empty(), "{}: a rejected request set a cookie", route.label());
                }
                if route.method == "GET" {
                    let head = call(&rig, "HEAD", &route.path(), remote, &[], "");
                    requests += 1;
                    assert_eq!(head.status, 401, "[configured={configured}] HEAD {} from {remote}", route.pattern);
                }
            }
        }
        assert_nothing_ran(&rig, &before, &format!("unauthenticated sweep (configured={configured})"));
        assert!(audit_count(&rig, "auth.unauthorized") >= 1, "rejections are not audited");
    }
    eprintln!("preauth 401 sweep: {} protected routes, {requests} requests, helper calls 0, no handler side effects", all.len());
}

#[test]
fn untrusted_sources_get_403_on_every_route_even_with_a_valid_session() {
    let all = routes();
    let rig = rig("untrusted", true);

    // A real session from a trusted peer, so the replay below is meaningful.
    let json = [("Content-Type", "application/json")];
    let login = call(&rig, "POST", "/api/v1/auth/login", LOOPBACK, &json, &format!(r#"{{"password":"{PASSWORD}"}}"#));
    assert_eq!(login.status, 200, "{}", login.body);
    let cookie = login.set_cookie.split(';').next().unwrap().to_string();
    let csrf = login.json()["csrf_token"].as_str().expect("csrf token").to_string();
    let authed = |remote: &str, method: &str, path: &str| {
        call(&rig, method, path, remote, &[("Cookie", &cookie), ("X-CSRF-Token", &csrf), ("Content-Type", "application/json")], "{}")
    };
    assert_eq!(authed(TRUSTED_LAN, "GET", "/api/v1/auth/session").status, 200, "control: the session cookie must be valid from a trusted source");

    let before = state(&rig);
    let untrusted = [
        "198.51.100.9:44444",    // documentation range, as a WAN peer would appear
        "8.8.8.8:1",             // public address
        "192.168.2.1:1",         // next /24 over
        "192.168.0.255:1",       // just below the trusted /24
        "10.8.0.2:1",            // WireGuard tunnel network (not listed in trusted_networks)
        "[2001:db8::1]:5000",    // public IPv6
        "[fe80::1]:5000",        // link-local IPv6
        "[::ffff:8.8.8.8]:5000", // IPv4-mapped public address
        "8.8.8.8",               // bare address without a port
        "not-an-address:1",      // unparsable source
        "",                      // missing source
    ];
    let spoofing: [(&str, &str); 7] = [
        ("X-Forwarded-For", "192.168.1.50"),
        ("X-Real-IP", "127.0.0.1"),
        ("Forwarded", "for=192.168.1.50"),
        ("Client-IP", "192.168.1.50"),
        ("True-Client-IP", "192.168.1.50"),
        ("X-Originating-IP", "127.0.0.1"),
        ("Host", "192.168.1.1:8443"),
    ];
    let mut requests = 0;
    for remote in untrusted {
        for route in &all {
            let plain = call(&rig, &route.method, &route.path(), remote, &[], "{}");
            requests += 1;
            assert_eq!(plain.status, 403, "{} from `{remote}` -> {} {}", route.label(), plain.status, plain.body);
            assert_eq!(plain.body.trim(), UNTRUSTED, "{} from `{remote}`: not the gate's 403 body", route.label());
            assert!(plain.hsts && plain.set_cookie.is_empty(), "{}: headers on a gate rejection", route.label());
            // A stolen but valid session and CSRF token do not help from an untrusted source.
            let replay = authed(remote, &route.method, &route.path());
            requests += 1;
            assert_eq!(replay.status, 403, "{} from `{remote}` with a VALID session -> {}", route.label(), replay.status);
            assert_eq!(replay.body.trim(), UNTRUSTED);
        }
        for (name, value) in spoofing {
            for route in all.iter().filter(|r| r.public || r.pattern.ends_with("/auth/session") || r.pattern == "/api/v1/config") {
                let reply = call(&rig, &route.method, &route.path(), remote, &[(name, value), ("Cookie", &cookie)], "{}");
                requests += 1;
                assert_eq!(reply.status, 403, "{} from `{remote}` with {name}: {value} -> {}", route.label(), reply.status);
            }
        }
    }
    assert_nothing_ran(&rig, &before, "untrusted-source sweep");
    assert!(audit_count(&rig, "access.trusted_network_rejected") >= 1, "gate rejections are not audited");
    assert_eq!(authed(TRUSTED_LAN, "GET", "/api/v1/auth/session").status, 200, "the session was disturbed by rejected requests");

    // Positive control for `assert_nothing_ran`: a legitimate mutation from a trusted
    // source with the same session must move exactly the counters the sweep asserted
    // had not moved, otherwise those assertions would be vacuous.
    let created = call(
        &rig,
        "POST",
        "/api/v1/snapshots",
        TRUSTED_LAN,
        &[("Cookie", &cookie), ("X-CSRF-Token", &csrf), ("Content-Type", "application/json")],
        r#"{"label":"witness"}"#,
    );
    assert_eq!(created.status, 200, "{}", created.body);
    assert_eq!(audit_count(&rig, "api.mutation"), 1, "witness: a handler that ran must be audited as api.mutation");
    assert_eq!(state(&rig).snapshots, before.snapshots + 1, "witness: the snapshot count must reflect a handler that ran");

    // Trusted controls: the same requests are NOT answered 403 (they reach the auth step).
    for remote in [LOOPBACK, "[::1]:1", TRUSTED_LAN, "[::ffff:192.168.1.50]:1", "192.168.1.50"] {
        assert_eq!(call(&rig, "GET", "/api/v1/system", remote, &[], "").status, 401, "control: {remote} must reach authentication");
        assert_eq!(call(&rig, "GET", "/api/v1/setup/status", remote, &[], "").status, 200, "control: {remote} must reach the public handler");
    }
    eprintln!("untrusted-source sweep: {} routes x {} sources, {requests} requests, all 403, no side effects", all.len(), untrusted.len());
}

#[test]
fn an_untrusted_source_cannot_run_first_run_setup() {
    let rig = rig("setup-untrusted", false);
    let before = state(&rig);
    assert!(before.admin.is_none(), "fixture should start unconfigured");
    let body = r#"{"admin_password":"attacker chosen passphrase"}"#;
    let json = [("Content-Type", "application/json")];
    for remote in ["198.51.100.9:1", "8.8.8.8:1", "[2001:db8::1]:1"] {
        let reply = call(&rig, "POST", "/api/v1/setup/apply", remote, &json, body);
        assert_eq!(reply.status, 403, "{remote}: {}", reply.body);
        assert_eq!(reply.body.trim(), UNTRUSTED);
    }
    assert_nothing_ran(&rig, &before, "untrusted setup attempts");
    assert_eq!(call(&rig, "GET", "/api/v1/setup/status", TRUSTED_LAN, &[], "").json()["is_configured"], false, "still unconfigured");

    // Design fact (recorded, not a defect claim): from a trusted source, first-run
    // setup is unauthenticated by design, so whoever on the trusted network posts
    // first claims the appliance.
    let claimed = call(&rig, "POST", "/api/v1/setup/apply", TRUSTED_LAN, &json, body);
    assert_eq!(claimed.status, 200, "{}", claimed.body);
    assert!(rig.helper.0.load(Ordering::SeqCst) >= 1, "setup applies through the helper");
    assert_eq!(call(&rig, "GET", "/api/v1/setup/status", TRUSTED_LAN, &[], "").json(), serde_json::json!({"is_configured": true}));
    eprintln!("FACT: first-run setup/apply is unauthenticated from any trusted_networks source until an administrator exists");
}

#[test]
fn setup_is_refused_after_completion_and_nothing_changes() {
    let rig = rig("setup-closed", true);
    let before = state(&rig);
    assert!(before.admin.is_some());
    let json = [("Content-Type", "application/json")];
    let bodies = [
        r#"{"admin_password":"attacker chosen passphrase"}"#.to_string(),
        r#"{"admin_password":"attacker chosen passphrase","wan_interface":"eth9","lan_interface":"eth8","pppoe_username":"u","pppoe_password":"p"}"#
            .to_string(),
        r#"{"admin_password":"short"}"#.to_string(),
        "not json at all".to_string(),
        String::new(),
    ];
    for remote in [LOOPBACK, TRUSTED_LAN] {
        for body in &bodies {
            let reply = call(&rig, "POST", "/api/v1/setup/apply", remote, &json, body);
            assert_eq!(reply.status, 403, "{remote} body `{body}` -> {} {}", reply.status, reply.body);
            assert!(reply.body.contains("already configured"), "{}", reply.body);
        }
    }
    assert_nothing_ran(&rig, &before, "setup/apply after completion");

    // The original credential still works, the attacker's does not.
    let bad = call(&rig, "POST", "/api/v1/auth/login", LOOPBACK, &json, r#"{"password":"attacker chosen passphrase"}"#);
    assert_eq!(bad.status, 401);
    let good = call(&rig, "POST", "/api/v1/auth/login", LOOPBACK, &json, &format!(r#"{{"password":"{PASSWORD}"}}"#));
    assert_eq!(good.status, 200, "{}", good.body);
}

fn keys(value: &Value) -> BTreeSet<String> {
    value.as_object().map(|o| o.keys().cloned().collect()).unwrap_or_default()
}

fn set(items: &[&str]) -> BTreeSet<String> {
    items.iter().map(|s| s.to_string()).collect()
}

/// Records exactly what an unauthenticated caller (from a trusted source) learns.
#[test]
fn setup_endpoints_disclosure_before_and_after_setup() {
    // ── before setup ──
    let fresh = rig("disclosure-fresh", false);
    let status = call(&fresh, "GET", "/api/v1/setup/status", TRUSTED_LAN, &[], "");
    assert_eq!(status.status, 200);
    let body = status.json();
    assert_eq!(
        keys(&body),
        set(&["is_configured", "wan_interface", "lan_interface", "lan_ip", "recovery_required", "recovery_reason"]),
        "setup/status discloses a different field set before setup: {body}"
    );
    assert_eq!(body["is_configured"], false);
    let defaults = default_config();
    assert_eq!(body["wan_interface"], defaults.wan.interface.as_str());
    assert_eq!(body["lan_interface"], defaults.lan.interface.as_str());
    assert_eq!(body["lan_ip"], defaults.lan.ip_address.as_str());
    assert_eq!(body["recovery_required"], false);
    eprintln!("FACT before setup, GET /setup/status discloses: {body}");

    let interfaces = call(&fresh, "GET", "/api/v1/setup/interfaces", TRUSTED_LAN, &[], "");
    let interfaces_body = interfaces.json();
    match interfaces.status {
        200 => {
            // Linux host: the shape is the serialised `RoleRecommendation`.
            assert!(keys(&interfaces_body).is_subset(&set(&["wan", "lan", "interfaces", "warnings"])), "{interfaces_body}");
            for item in interfaces_body["interfaces"].as_array().into_iter().flatten() {
                assert!(keys(item).is_subset(&set(&["name", "mac_address", "up", "carrier", "carrier_known", "physical", "default_route", "score"])), "{item}");
            }
            eprintln!("FACT before setup, GET /setup/interfaces (live host) discloses: {interfaces_body}");
        }
        503 => {
            // This host has no /sys/class/net (Windows): the handler answers with its error shape only.
            assert_eq!(keys(&interfaces_body), set(&["error"]), "{interfaces_body}");
            eprintln!(
                "NOTE: this host cannot enumerate interfaces; setup/interfaces answered 503 {interfaces_body}. The 200 shape is checked below against a fake sysfs tree."
            );
        }
        other => panic!("setup/interfaces before setup answered {other}: {}", interfaces.body),
    }

    // The 200 shape, deterministically: same type the handler serialises, fed from a fake sysfs.
    let sys = std::env::temp_dir().join(format!("mr-preauth-sysfs-{}", std::process::id()));
    let _sys_guard = TempDir(sys.clone());
    let _ = std::fs::remove_dir_all(&sys);
    for (i, (name, mac)) in [("eth0", "02:00:00:00:00:01"), ("eth1", "02:00:00:00:00:02")].iter().enumerate() {
        let dir = sys.join(name);
        std::fs::create_dir_all(dir.join("device")).unwrap();
        std::fs::write(dir.join("address"), format!("{mac}\n")).unwrap();
        std::fs::write(dir.join("flags"), "0x1003\n").unwrap();
        std::fs::write(dir.join("carrier"), "1\n").unwrap();
        std::fs::write(dir.join("ifindex"), format!("{}\n", i + 2)).unwrap();
    }
    let listed = vec![
        minimalrouter::network::Interface { name: "eth0".into(), hardware_addr: vec![2, 0, 0, 0, 0, 1], up: true },
        minimalrouter::network::Interface { name: "eth1".into(), hardware_addr: vec![2, 0, 0, 0, 0, 2], up: true },
    ];
    let recommendation = minimalrouter::network::discover_from(sys.to_str().unwrap(), "/nonexistent/route", &listed).unwrap();
    let shape = serde_json::to_value(&recommendation).unwrap();
    assert!(keys(&shape).is_subset(&set(&["wan", "lan", "interfaces", "warnings"])), "{shape}");
    let first = &shape["interfaces"][0];
    assert_eq!(keys(first), set(&["name", "mac_address", "up", "carrier", "carrier_known", "physical", "default_route", "score"]), "{first}");
    assert!(first["mac_address"].as_str().unwrap().contains(':'), "MAC addresses are part of the pre-setup disclosure");
    eprintln!("FACT before setup, GET /setup/interfaces (200) discloses per interface: {:?} plus wan/lan recommendation", keys(first));

    // ── after setup ──
    let done = rig("disclosure-done", true);
    for remote in [LOOPBACK, TRUSTED_LAN] {
        let status = call(&done, "GET", "/api/v1/setup/status", remote, &[], "");
        assert_eq!(status.status, 200);
        assert_eq!(
            status.json(),
            serde_json::json!({"is_configured": true}),
            "setup/status must reveal only the routing hint after setup, got {}",
            status.body
        );

        let interfaces = call(&done, "GET", "/api/v1/setup/interfaces", remote, &[], "");
        assert_eq!(interfaces.status, 404, "setup/interfaces still answers after setup: {}", interfaces.body);
        let unknown = call(&done, "GET", "/api/v1/setup/no-such-endpoint", remote, &[], "");
        assert_eq!(unknown.status, 404);
        assert_eq!(interfaces.body, unknown.body, "after setup, setup/interfaces is distinguishable from a path that does not exist");
        assert!(!interfaces.body.contains("eth"), "interface names leaked: {}", interfaces.body);
    }
    // The authenticated twin stays behind the session.
    assert_eq!(call(&done, "GET", "/api/v1/system/interfaces", TRUSTED_LAN, &[], "").status, 401);
    eprintln!(
        "FACT after setup: setup/status -> {{\"is_configured\":true}} only; setup/interfaces -> 404 identical to an unknown path; system/interfaces -> 401"
    );
}
