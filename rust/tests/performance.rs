//! Stable release-mode measurements of the former Go API and slot-state
//! benchmarks. API measurements dispatch synthetic requests (no TCP/TLS),
//! including construction and response assertions. Four workers contend on
//! one server. Slot reads use real files and, on Unix, real symlinks.
//! Run explicitly with `cargo test --release --test performance -- --ignored
//! --nocapture --test-threads=1`. These are throughput measurements, not Go
//! allocation-count parity, per-request latency, or router forwarding tests.

use std::hint::black_box;
use std::io::Cursor;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Barrier};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use minimalrouter::api::Server;
use minimalrouter::apply::{ApplyRequest, ApplyResponse, Client, Engine};
use minimalrouter::auth::{RateLimiter, SessionManager};
use minimalrouter::config::{SQLiteStore, default_config};
use minimalrouter::goish::errors::Result;
use minimalrouter::httpd::{Request, ResponseBody, Router};
use serde::Serialize;
use serde_json::{Value, json};

const REPETITIONS: usize = 3;
const SAMPLE_TIME: Duration = Duration::from_secs(1);
const API_WORKERS: usize = 4;
const BATCH: u64 = 32;

fn require_release_build() {
    #[cfg(debug_assertions)]
    panic!("benchmarks require cargo test --release");
}

struct Scratch(PathBuf);

impl Scratch {
    fn new(label: &str) -> Self {
        let nonce = SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos();
        let path = std::env::temp_dir().join(format!("mr-bench-{label}-{}-{nonce}", std::process::id()));
        std::fs::create_dir(&path).expect("create unique benchmark fixture");
        Self(path)
    }
}

impl Drop for Scratch {
    fn drop(&mut self) {
        std::fs::remove_dir_all(&self.0).expect("remove benchmark fixture after closing its handles");
    }
}

struct NeverApply(AtomicUsize);

impl Client for NeverApply {
    fn apply(&self, _deadline: Instant, _request: &ApplyRequest) -> Result<ApplyResponse> {
        self.0.fetch_add(1, Ordering::SeqCst);
        Err(minimalrouter::errorf!("read/rejection benchmark reached the privileged helper"))
    }
}

#[derive(Serialize)]
struct Measurement {
    name: &'static str,
    repetition: usize,
    workers: usize,
    operations: u64,
    elapsed_ns: u128,
    wall_ns_per_op: f64,
    operations_per_second: f64,
}

impl Measurement {
    fn new(name: &'static str, repetition: usize, workers: usize, operations: u64, elapsed: Duration) -> Self {
        assert!(operations > 0, "measurement must execute work");
        assert!(elapsed >= SAMPLE_TIME, "measurement must cover the complete sample window");
        let wall_ns_per_op = elapsed.as_nanos() as f64 / operations as f64;
        let operations_per_second = operations as f64 / elapsed.as_secs_f64();
        assert!(wall_ns_per_op.is_finite() && wall_ns_per_op > 0.0);
        assert!(operations_per_second.is_finite() && operations_per_second > 0.0);
        Self { name, repetition, workers, operations, elapsed_ns: elapsed.as_nanos(), wall_ns_per_op, operations_per_second }
    }
}

fn save_measurements(suite: &str, measurements: &[Measurement]) {
    let output =
        std::env::var_os("MINIMALROUTER_BENCH_OUTPUT").map(PathBuf::from).unwrap_or_else(|| Path::new(env!("CARGO_MANIFEST_DIR")).join("target/performance"));
    std::fs::create_dir_all(&output).unwrap();
    for row in measurements {
        println!("BENCHMARK {}", serde_json::to_string(row).unwrap());
    }
    let report = json!({
        "schema_version": 1,
        "suite": suite,
        "source_commit": std::env::var("MINIMALROUTER_BENCH_COMMIT").ok(),
        "target_os": std::env::consts::OS,
        "target_arch": std::env::consts::ARCH,
        "profile": "release",
        "sample_seconds": SAMPLE_TIME.as_secs(),
        "repetitions": REPETITIONS,
        "measurements": measurements,
    });
    std::fs::write(output.join(format!("{suite}.json")), serde_json::to_vec_pretty(&report).unwrap()).unwrap();
}

struct ApiCase {
    name: &'static str,
    method: &'static str,
    path: &'static str,
    request_body: &'static [u8],
    status: u16,
    response_body: Vec<u8>,
}

fn api_request(router: &Router, case: &ApiCase) -> Vec<u8> {
    let mut body = Cursor::new(case.request_body.to_vec());
    let mut request = Request::synthetic(case.method, case.path, &mut body, case.request_body.len() as u64);
    if case.method == "POST" {
        request.headers.set("Content-Type", "application/json");
    }
    let response = router.dispatch(&mut request);
    assert_eq!(response.status, case.status, "{} returned an unexpected status", case.name);
    let ResponseBody::Bytes(bytes) = response.body else { panic!("{} unexpectedly streamed its body", case.name) };
    bytes
}

fn api_sample(router: &Router, case: &ApiCase, repetition: usize) -> Measurement {
    let barrier = Barrier::new(API_WORKERS + 1);
    std::thread::scope(|scope| {
        let workers: Vec<_> = (0..API_WORKERS)
            .map(|_| {
                let barrier = &barrier;
                scope.spawn(move || {
                    barrier.wait();
                    let start = Instant::now();
                    let mut operations = 0;
                    while start.elapsed() < SAMPLE_TIME {
                        for _ in 0..BATCH {
                            assert_eq!(black_box(api_request(router, case)), case.response_body, "{} response changed", case.name);
                            operations += 1;
                        }
                    }
                    operations
                })
            })
            .collect();
        let start = Instant::now();
        barrier.wait();
        let operations = workers.into_iter().map(|worker| worker.join().expect("API benchmark worker")).sum();
        Measurement::new(case.name, repetition, API_WORKERS, operations, start.elapsed())
    })
}

#[test]
#[ignore = "explicit release-mode performance job; not a unit-test timing assertion"]
fn benchmark_api() {
    require_release_build();
    let scratch = Scratch::new("api");
    let store = Arc::new(SQLiteStore::new_file_store(scratch.0.to_str().unwrap()).unwrap());
    let initial = store.get_latest_config().unwrap();
    let helper = Arc::new(NeverApply(AtomicUsize::new(0)));
    let engine = Engine::with_client(default_config(), Some(store.clone()), helper.clone());
    // Keep both real in-memory rate-limiter paths but size their fixture budgets so
    // malformed JSON stays a parser benchmark, never a 429 fast-path benchmark.
    let server = Server::with_auth(engine, SessionManager::new(), RateLimiter::new(u32::MAX, Duration::from_secs(60)), "", None);
    server.configure_global_login_limiter(Some(RateLimiter::new(u32::MAX, Duration::from_secs(60))));
    let mut router = Router::new();
    server.register_routes(&mut router);
    let mut cases = [
        ApiCase { name: "APISetupStatusParallel", method: "GET", path: "/api/v1/setup/status", request_body: b"", status: 200, response_body: Vec::new() },
        ApiCase { name: "APIProtectedEndpointParallel", method: "GET", path: "/api/v1/system", request_body: b"", status: 401, response_body: Vec::new() },
        ApiCase { name: "APIMalformedRequestParallel", method: "POST", path: "/api/v1/auth/login", request_body: b"{", status: 400, response_body: Vec::new() },
    ];
    for case in &mut cases {
        case.response_body = api_request(&router, case);
    }
    let setup: Value = serde_json::from_slice(&cases[0].response_body).unwrap();
    assert_eq!(setup["is_configured"], false);
    assert_eq!(setup["lan_ip"], "192.168.1.1");
    assert_eq!(setup["lan_interface"], "eth1");
    assert_eq!(setup["wan_interface"], "eth0");
    assert_eq!(setup["recovery_required"], false);
    let protected: Value = serde_json::from_slice(&cases[1].response_body).unwrap();
    assert_eq!(protected, json!({"error": "Unauthorized or expired session"}));
    assert_eq!(cases[2].response_body, b"Invalid JSON\n");
    let mut measurements = Vec::new();
    for case in &cases {
        for repetition in 1..=REPETITIONS {
            measurements.push(api_sample(&router, case, repetition));
        }
    }
    assert_eq!(helper.0.load(Ordering::SeqCst), 0, "benchmarks must never call the privileged helper");
    assert_eq!(store.get_latest_config().unwrap(), initial, "read/rejection workloads changed canonical state");
    save_measurements("api", &measurements);
    drop(router);
    drop(server);
    drop(store);
}

#[cfg(unix)]
#[test]
#[ignore = "explicit release-mode performance job; requires real Unix symlinks"]
fn benchmark_update_state() {
    use minimalrouter::firmware::{SlotManager, SlotState};

    require_release_build();
    let scratch = Scratch::new("update");
    for version in ["v1.0.0", "v1.1.0"] {
        std::fs::create_dir_all(scratch.0.join("slots").join(version)).unwrap();
    }
    std::os::unix::fs::symlink("slots/v1.1.0", scratch.0.join("current")).unwrap();
    std::os::unix::fs::symlink("slots/v1.0.0", scratch.0.join("previous")).unwrap();
    let expected = SlotState { current: "v1.1.0".into(), previous: "v1.0.0".into(), minimum_version: "v1.1.0".into(), ..Default::default() };
    let manager = SlotManager { root: scratch.0.to_str().unwrap().into(), ..Default::default() };
    let mut measurements = Vec::new();
    for with_journal in [false, true] {
        let state = if with_journal {
            SlotState { current: "v1.0.0".into(), pending: "v1.1.0".into(), minimum_version: "v1.0.0".into(), ..Default::default() }
        } else {
            expected.clone()
        };
        let state_bytes = serde_json::to_vec(&state).unwrap();
        std::fs::write(scratch.0.join("state.json"), &state_bytes).unwrap();
        let journal = serde_json::to_vec(&json!({"version": 1, "kind": "activate", "old": state, "next": expected})).unwrap();
        if with_journal {
            std::fs::write(scratch.0.join("operation.json"), &journal).unwrap();
        }
        assert_eq!(manager.state().unwrap(), expected, "fixture must exercise the intended state projection");
        let name = if with_journal { "SlotManagerStateWithJournal" } else { "SlotManagerState" };
        for repetition in 1..=REPETITIONS {
            let start = Instant::now();
            let mut operations = 0;
            while start.elapsed() < SAMPLE_TIME {
                for _ in 0..BATCH {
                    assert_eq!(black_box(manager.state().expect("read update state")), expected);
                    operations += 1;
                }
            }
            measurements.push(Measurement::new(name, repetition, 1, operations, start.elapsed()));
        }
        assert_eq!(std::fs::read(scratch.0.join("state.json")).unwrap(), state_bytes, "state observation must remain read-only");
        if with_journal {
            assert_eq!(std::fs::read(scratch.0.join("operation.json")).unwrap(), journal, "observation must not recover or consume the journal");
        }
    }
    save_measurements("update", &measurements);
}
