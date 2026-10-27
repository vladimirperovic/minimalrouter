//! The management API end to end over real HTTP: first-run setup, login,
//! session cookie, CSRF/Origin/Fetch-Metadata enforcement, redaction,
//! read-only sessions and logout, with a fake privileged helper.

use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};

use minimalrouter::api::{RateLimiterInterface, Server, SessionManagerInterface};
use minimalrouter::apply::{ApplyRequest, ApplyResponse, Client, Engine};
use minimalrouter::auth_persistent::{PersistentRateLimiter, PersistentSessionManager};
use minimalrouter::config::{default_config, SQLiteStore};
use minimalrouter::goish::errors::Result;
use minimalrouter::httpd::{self, Request, Router};

struct AcceptAll;

impl Client for AcceptAll {
    fn apply(&self, _deadline: Instant, req: &ApplyRequest) -> Result<ApplyResponse> {
        Ok(ApplyResponse { id: req.id.clone(), success: true, verified: true, ..Default::default() })
    }
}

struct Reply {
    status: u16,
    headers: String,
    body: String,
}

fn exchange(addr: std::net::SocketAddr, method: &str, path: &str, headers: &[(&str, &str)], body: &str) -> Reply {
    let mut stream = TcpStream::connect(addr).unwrap();
    let mut request = format!("{} {} HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\nContent-Length: {}\r\n", method, path, body.len());
    for (k, v) in headers {
        request.push_str(&format!("{}: {}\r\n", k, v));
    }
    request.push_str("\r\n");
    request.push_str(body);
    stream.write_all(request.as_bytes()).unwrap();
    let mut raw = String::new();
    stream.read_to_string(&mut raw).unwrap();
    let (head, body) = raw.split_once("\r\n\r\n").unwrap();
    let status = head.split(' ').nth(1).unwrap().parse().unwrap();
    Reply { status, headers: head.to_string(), body: body.to_string() }
}

fn cookie(reply: &Reply) -> String {
    let line = reply.headers.lines().find(|l| l.to_ascii_lowercase().starts_with("set-cookie:")).expect("session cookie");
    line.split_once(':').unwrap().1.trim().split(';').next().unwrap().to_string()
}

#[test]
fn setup_login_and_csrf_over_http() {
    let dir = std::env::temp_dir().join(format!("mr-api-e2e-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);
    let store = Arc::new(SQLiteStore::new_file_store(dir.to_str().unwrap()).unwrap());
    let engine = Engine::with_client(default_config(), Some(store.clone()), Arc::new(AcceptAll));
    let sessions: Arc<dyn SessionManagerInterface> = PersistentSessionManager::with_secure_cookies(store.clone(), false);
    let limiter: Arc<dyn RateLimiterInterface> = PersistentRateLimiter::new(store.clone(), 50, minimalrouter::goish::time::Duration::secs(60));
    let server = Server::with_auth(engine, sessions, limiter, "", Some(store.clone()));
    let mut router = Router::new();
    server.register_routes(&mut router);
    let router = Arc::new(router);
    let handler: httpd::Handler = Arc::new(move |r: &mut Request<'_>| router.dispatch(r));
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let addr = listener.local_addr().unwrap();
    let stop = Arc::new(AtomicBool::new(false));
    let stop_server = stop.clone();
    let serving = std::thread::spawn(move || httpd::serve(listener, None, handler, httpd::ServerOptions { shutdown_drain: Duration::from_millis(200), ..Default::default() }, &stop_server).unwrap());

    let json = [("Content-Type", "application/json")];
    let status = exchange(addr, "GET", "/api/v1/setup/status", &[], "");
    assert_eq!(status.status, 200);
    assert!(status.body.contains("\"is_configured\":false"), "{}", status.body);
    assert!(status.headers.contains("Strict-Transport-Security: max-age=63072000"));

    assert_eq!(exchange(addr, "GET", "/api/v1/system", &[], "").status, 401);
    let short = exchange(addr, "POST", "/api/v1/setup/apply", &json, r#"{"admin_password":"short"}"#);
    assert_eq!(short.status, 422);
    let unknown = exchange(addr, "POST", "/api/v1/setup/apply", &json, r#"{"admin_password":"correct horse battery","surprise":1}"#);
    assert_eq!(unknown.status, 400, "unknown fields are rejected: {}", unknown.body);

    let setup = exchange(addr, "POST", "/api/v1/setup/apply", &json, r#"{"admin_password":"correct horse battery"}"#);
    assert_eq!(setup.status, 200, "{}", setup.body);
    assert!(setup.body.contains("\"success\":true"));
    assert_eq!(exchange(addr, "POST", "/api/v1/setup/apply", &json, r#"{"admin_password":"correct horse battery"}"#).status, 403);

    let wrong = exchange(addr, "POST", "/api/v1/auth/login", &json, r#"{"password":"nope nope nope"}"#);
    assert_eq!(wrong.status, 401);
    let login = exchange(addr, "POST", "/api/v1/auth/login", &json, r#"{"password":"correct horse battery"}"#);
    assert_eq!(login.status, 200, "{}", login.body);
    let session = cookie(&login);
    let csrf: serde_json::Value = serde_json::from_str(login.body.trim()).unwrap();
    let csrf = csrf["csrf_token"].as_str().unwrap().to_string();

    let config = exchange(addr, "GET", "/api/v1/config", &[("Cookie", &session)], "");
    assert_eq!(config.status, 200);
    assert!(config.body.contains("\"password\":\"[REDACTED]\""), "secrets are redacted");

    let snapshot_body = r#"{"label":"before change"}"#;
    assert_eq!(exchange(addr, "POST", "/api/v1/snapshots", &[("Cookie", &session), ("Content-Type", "application/json")], snapshot_body).status, 403, "missing CSRF token");
    assert_eq!(
        exchange(addr, "POST", "/api/v1/snapshots", &[("Cookie", &session), ("X-CSRF-Token", &csrf), ("Sec-Fetch-Site", "cross-site")], snapshot_body).status,
        403,
        "cross-site fetch metadata"
    );
    assert_eq!(exchange(addr, "POST", "/api/v1/snapshots", &[("Cookie", &session), ("X-CSRF-Token", &csrf), ("Origin", "https://evil.example")], snapshot_body).status, 403, "foreign origin");
    let created = exchange(addr, "POST", "/api/v1/snapshots", &[("Cookie", &session), ("X-CSRF-Token", &csrf), ("Content-Type", "application/json")], snapshot_body);
    assert_eq!(created.status, 200, "{}", created.body);
    let listed = exchange(addr, "GET", "/api/v1/snapshots", &[("Cookie", &session)], "");
    assert!(listed.body.contains("before change"));
    assert_eq!(exchange(addr, "DELETE", "/api/v1/config", &[("Cookie", &session)], "").status, 405);

    let observer = exchange(addr, "POST", "/api/v1/auth/login", &json, r#"{"password":"correct horse battery","read_only":true}"#);
    let observer_cookie = cookie(&observer);
    let observer_csrf: serde_json::Value = serde_json::from_str(observer.body.trim()).unwrap();
    let observer_csrf = observer_csrf["csrf_token"].as_str().unwrap().to_string();
    let refused = exchange(addr, "POST", "/api/v1/snapshots", &[("Cookie", &observer_cookie), ("X-CSRF-Token", &observer_csrf), ("Content-Type", "application/json")], snapshot_body);
    assert_eq!(refused.status, 403, "read-only sessions cannot mutate");

    let logout = exchange(addr, "POST", "/api/v1/auth/logout", &[("Cookie", &session), ("X-CSRF-Token", &csrf)], "");
    assert_eq!(logout.status, 200);
    assert!(logout.headers.contains("Max-Age=0"));
    assert_eq!(exchange(addr, "GET", "/api/v1/config", &[("Cookie", &session)], "").status, 401, "revoked session");

    let events = store.query_audit_events(&minimalrouter::config::AuditQuery { limit: 100, ..Default::default() }).unwrap();
    let kinds: Vec<&str> = events.events.iter().map(|e| e.event_type.as_str()).collect();
    for expected in ["system.setup_completed", "auth.login_succeeded", "auth.login_failed", "auth.csrf_rejected", "auth.origin_rejected", "auth.read_only_rejected", "api.mutation"] {
        assert!(kinds.contains(&expected), "missing audit event {expected}: {kinds:?}");
    }

    stop.store(true, Ordering::SeqCst);
    serving.join().unwrap();
    drop(server);
    drop(store);
    std::fs::remove_dir_all(dir).ok();
}
