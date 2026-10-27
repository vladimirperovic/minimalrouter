//! The MCP bridge binary against the Rust API over TLS: certificate pinning,
//! login, session cookie, the read-only session and real tool calls.

use std::io::{BufRead, BufReader, Write};
use std::net::TcpListener;
use std::process::{Command, Stdio};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::{Duration, Instant};

use minimalrouter::api::{RateLimiterInterface, Server, SessionManagerInterface};
use minimalrouter::apply::{ApplyRequest, ApplyResponse, Client, Engine};
use minimalrouter::auth_persistent::{PersistentRateLimiter, PersistentSessionManager};
use minimalrouter::config::{default_config, SQLiteStore};
use minimalrouter::goish::errors::Result;
use minimalrouter::httpd::{self, CertResolver, Request, Router};
use minimalrouter::tlsutil::CertManager;

struct AcceptAll;

impl Client for AcceptAll {
    fn apply(&self, _deadline: Instant, req: &ApplyRequest) -> Result<ApplyResponse> {
        Ok(ApplyResponse { id: req.id.clone(), success: true, verified: true, ..Default::default() })
    }
}

#[test]
fn mcp_bridge_reads_through_the_tls_api() {
    let dir = std::env::temp_dir().join(format!("mr-mcp-e2e-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);
    let store = Arc::new(SQLiteStore::new_file_store(dir.to_str().unwrap()).unwrap());
    let password = "correct horse battery";
    let hash = minimalrouter::auth::hash_password(password).unwrap();
    let mut cfg = default_config();
    cfg.revision = store.get_latest_config().unwrap().revision + 1;
    store.commit_initial_setup(cfg.clone(), &hash).unwrap();
    let engine = Engine::with_client(store.get_latest_config().unwrap(), Some(store.clone()), Arc::new(AcceptAll));
    let sessions: Arc<dyn SessionManagerInterface> = PersistentSessionManager::new(store.clone());
    let limiter: Arc<dyn RateLimiterInterface> = PersistentRateLimiter::new(store.clone(), 50, minimalrouter::goish::time::Duration::secs(60));
    let server = Server::with_auth(engine, sessions, limiter, &hash, Some(store.clone()));
    let mut router = Router::new();
    server.register_routes(&mut router);
    let router = Arc::new(router);
    let handler: httpd::Handler = Arc::new(move |r: &mut Request<'_>| router.dispatch(r));

    let (cert, key) = CertManager::new(dir.to_str().unwrap()).ensure_certificate(&cfg).unwrap();
    let ca_path = dir.join("router-ca.pem");
    std::fs::write(&ca_path, &cert).unwrap();
    let password_path = dir.join("password");
    std::fs::write(&password_path, format!("{}\n", password)).unwrap();
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        std::fs::set_permissions(&password_path, std::fs::Permissions::from_mode(0o600)).unwrap();
    }
    let tls = Arc::new(CertResolver::from_pem(&cert, &key).unwrap()).server_config().unwrap();
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let port = listener.local_addr().unwrap().port();
    let stop = Arc::new(AtomicBool::new(false));
    let stop_server = stop.clone();
    let serving = std::thread::spawn(move || httpd::serve(listener, Some(tls), handler, httpd::ServerOptions { shutdown_drain: Duration::from_millis(200), ..Default::default() }, &stop_server).unwrap());

    let mut child = Command::new(env!("CARGO_BIN_EXE_minimalrouter-mcp"))
        .env("MINIMALROUTER_API_URL", format!("https://127.0.0.1:{}", port))
        .env("MINIMALROUTER_CA_CERT", &ca_path)
        .env("MINIMALROUTER_PASSWORD_FILE", &password_path)
        .env_remove("MINIMALROUTER_MCP_MODE")
        .env_remove("MINIMALROUTER_TOTP_CODE")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let mut stdin = child.stdin.take().unwrap();
    let mut lines = BufReader::new(child.stdout.take().unwrap()).lines();
    let mut call = |request: &str| {
        writeln!(stdin, "{}", request).unwrap();
        stdin.flush().unwrap();
        let line = lines.next().expect("response line").unwrap();
        serde_json::from_str::<serde_json::Value>(&line).unwrap()
    };

    let init = call(r#"{"jsonrpc":"2.0","id":1,"method":"initialize"}"#);
    assert!(init["result"]["instructions"].as_str().unwrap().contains("read-only mode"));
    let config = call(r#"{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_full_config","arguments":{}}}"#);
    let text = config["result"]["content"][0]["text"].as_str().unwrap();
    assert!(config["result"]["isError"].is_null(), "{text}");
    assert!(text.contains("\"password\":\"[REDACTED]\""), "redacted configuration over the bridge");
    let events = call(r#"{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_security_events","arguments":{"limit":5,"category":"security"}}}"#);
    assert!(events["result"]["content"][0]["text"].as_str().unwrap().contains("\"events\""), "{events}");
    let blocked = call(r#"{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"create_snapshot","arguments":{"label":"x"}}}"#);
    assert_eq!(blocked["result"]["isError"], true);

    drop(stdin);
    assert!(child.wait().unwrap().success());
    let audit = store.query_audit_events(&minimalrouter::config::AuditQuery { limit: 50, ..Default::default() }).unwrap();
    let login = audit.events.iter().find(|e| e.event_type == "auth.login_succeeded").expect("bridge logged in");
    assert_eq!(login.details.get("mode").map(String::as_str), Some("read_only"), "the bridge starts with a read-only session");

    stop.store(true, Ordering::SeqCst);
    serving.join().unwrap();
    drop(server);
    drop(store);
    std::fs::remove_dir_all(dir).ok();
}
