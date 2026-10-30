//! Run the same 3,000 request contracts used against the installed QEMU router.
//! Here TLS, HTTP, SQLite and authentication are real; only privileged apply is
//! fake, and it must receive no request. Installed service/network/chaos behavior
//! remains the separate GitHub lab's responsibility.

use std::net::TcpListener;
use std::process::Command;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::{Duration, Instant};

use minimalrouter::api::{RateLimiterInterface, Server, SessionManagerInterface};
use minimalrouter::apply::{ApplyRequest, ApplyResponse, Client, Engine};
use minimalrouter::auth_persistent::{PersistentRateLimiter, PersistentSessionManager};
use minimalrouter::config::{SQLiteStore, default_config};
use minimalrouter::goish::errors::Result;
use minimalrouter::httpd::{self, CertResolver, Request, Router};
use minimalrouter::shutdown::Shutdown;
use minimalrouter::tlsutil::CertManager;

struct NeverApply(AtomicUsize);

impl Client for NeverApply {
    fn apply(&self, _deadline: Instant, _req: &ApplyRequest) -> Result<ApplyResponse> {
        self.0.fetch_add(1, Ordering::SeqCst);
        Err(minimalrouter::errorf!("contract request reached the privileged boundary"))
    }
}

#[test]
fn three_thousand_api_contracts_over_tls() {
    let dir = std::env::temp_dir().join(format!("mr-contracts-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);
    let store = Arc::new(SQLiteStore::new_file_store(dir.to_str().unwrap()).unwrap());
    let password = hex::encode(minimalrouter::goish::rand::bytes(32).expect("generate contract fixture password"));
    let hash = minimalrouter::auth::hash_password(&password).unwrap();
    let mut cfg = default_config();
    cfg.revision = store.get_latest_config().unwrap().revision + 1;
    store.commit_initial_setup(cfg, &hash).unwrap();
    let helper = Arc::new(NeverApply(AtomicUsize::new(0)));
    let engine = Engine::with_client(store.get_latest_config().unwrap(), Some(store.clone()), helper.clone());
    let sessions: Arc<dyn SessionManagerInterface> = PersistentSessionManager::new(store.clone());
    let limiter: Arc<dyn RateLimiterInterface> = PersistentRateLimiter::new(store.clone(), 50, minimalrouter::goish::time::Duration::secs(60));
    let server = Server::with_auth(engine, sessions, limiter, &hash, Some(store));
    let mut router = Router::new();
    server.register_routes(&mut router);
    let router = Arc::new(router);
    let handler: httpd::Handler = Arc::new(move |r: &mut Request<'_>| router.dispatch(r));
    let (cert, key) = CertManager::new(dir.to_str().unwrap()).ensure_certificate(&default_config()).unwrap();
    let tls = Arc::new(CertResolver::from_pem(&cert, &key).unwrap()).server_config().unwrap();
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let port = listener.local_addr().unwrap().port();
    let stop = Shutdown::new();
    let stop_server = stop.clone();
    let serving = std::thread::spawn(move || {
        httpd::serve(listener, Some(tls), handler, httpd::ServerOptions { shutdown_drain: Duration::from_millis(200), ..Default::default() }, &stop_server)
            .unwrap()
    });
    let root = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).parent().unwrap();
    let output = root.join("rust/target/contract-matrix");
    let python = std::env::var("PYTHON").unwrap_or_else(|_| if cfg!(windows) { "python" } else { "python3" }.into());
    let status = Command::new(python)
        .args(["-I"])
        .arg(root.join("scripts/gh-lab/contracts.py"))
        .args(["--fixture", "--url", &format!("https://127.0.0.1:{port}"), "--out"])
        .arg(output)
        .env("DASH_PASS", password)
        .status();
    stop.trigger();
    serving.join().unwrap();
    drop(server); // Close SQLite handles before deleting the fixture on Windows.
    assert_eq!(helper.0.load(Ordering::SeqCst), 0, "a rejected/preview request contacted the privileged helper");
    assert!(status.expect("Python 3 is required for the contract integration test").success(), "3,000-case API matrix failed; inspect target/contract-matrix");
    std::fs::remove_dir_all(dir).unwrap();
}
