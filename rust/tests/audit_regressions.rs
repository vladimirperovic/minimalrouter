//! Regression tests for verified independent audit findings.

#[test]
fn wan_changes_require_confirmation_when_management_uses_wireguard() {
    let mut current = minimalrouter::config::default_config();
    let mut candidate = current.clone();
    candidate.wan.mtu -= 8;
    assert!(!minimalrouter::config::requires_confirmation(&current, &candidate));
    current.system.management_access = "wireguard_only".into();
    candidate.system.management_access = "wireguard_only".into();
    assert!(minimalrouter::config::requires_confirmation(&current, &candidate));
    candidate = current.clone();
    candidate.wan.enabled = !current.wan.enabled;
    assert!(minimalrouter::config::requires_confirmation(&current, &candidate));
}

#[test]
fn disabled_wireguard_rendering_rejects_directive_injection() {
    let mut cfg = minimalrouter::config::default_config();
    cfg.wireguard.enabled = false;
    cfg.wireguard.private_key = "key\nListenPort = 1".into();
    assert!(minimalrouter::services::generate_wireguard(&cfg.wireguard).is_err());
    assert!(minimalrouter::services::generate_wireguard_runtime(&cfg.wireguard).is_err());
    cfg.wireguard.private_key.clear();
    cfg.wireguard.address = "10.0.0.1/24\n[Peer]".into();
    assert!(minimalrouter::services::generate_wireguard_runtime(&cfg.wireguard).is_err());
}

use std::path::PathBuf;
use std::sync::{Arc, LazyLock};
use std::time::Instant;

use minimalrouter::api::{RateLimiterInterface, Server, SessionManagerInterface};
use minimalrouter::apply::{ApplyRequest, ApplyResponse, Client, Engine};
use minimalrouter::auth_persistent::{PersistentRateLimiter, PersistentSessionManager};
use minimalrouter::config::{SQLiteStore, default_config};
use minimalrouter::goish::errors::Result;
use minimalrouter::httpd::{Request, Router};

struct Helper;
impl Client for Helper {
    fn apply(&self, _deadline: Instant, req: &ApplyRequest) -> Result<ApplyResponse> {
        Ok(ApplyResponse { id: req.id.clone(), success: true, verified: true, ..Default::default() })
    }
}

struct TempDir(PathBuf);
impl Drop for TempDir {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

static PASSWORD: LazyLock<String> = LazyLock::new(|| {
    let mut bytes = [0u8; 32];
    minimalrouter::goish::rand::fill(&mut bytes).unwrap();
    hex::encode(bytes)
});
static HASH: LazyLock<String> = LazyLock::new(|| minimalrouter::auth::hash_password(&PASSWORD).unwrap());

struct Rig {
    router: Router,
    _store: Arc<SQLiteStore>,
    _dir: TempDir,
}

/// The same limiter wiring as `routerd` main(): 5/60 s per source, 100/60 s router-wide, both persistent.
fn production_like_rig(tag: &str) -> Rig {
    let dir = std::env::temp_dir().join(format!("mr-audit-poc-{tag}-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);
    let guard = TempDir(dir.clone());
    let store = Arc::new(SQLiteStore::new_file_store(dir.to_str().unwrap()).unwrap());
    let mut cfg = default_config();
    cfg.revision = store.get_latest_config().unwrap().revision + 1;
    store.commit_initial_setup(cfg, &HASH).unwrap();
    let engine = Engine::with_client(store.get_latest_config().unwrap(), Some(store.clone()), Arc::new(Helper));
    let sessions: Arc<dyn SessionManagerInterface> = PersistentSessionManager::with_secure_cookies(store.clone(), false);
    let per_source: Arc<dyn RateLimiterInterface> = PersistentRateLimiter::new(store.clone(), 5, minimalrouter::goish::time::Duration::secs(60));
    let global: Arc<dyn RateLimiterInterface> = PersistentRateLimiter::new(store.clone(), 100, minimalrouter::goish::time::Duration::secs(60));
    let server = Server::with_auth(engine, sessions, per_source, &HASH, Some(store.clone()));
    server.configure_global_login_limiter(Some(global));
    let mut router = Router::new();
    server.register_routes(&mut router);
    Rig { router, _store: store, _dir: guard }
}

fn login(rig: &Rig, remote: &str, body: &str) -> (u16, String) {
    let mut cursor = std::io::Cursor::new(body.as_bytes().to_vec());
    let mut request = Request::synthetic("POST", "/api/v1/auth/login", &mut cursor, body.len() as u64);
    request.remote_addr = remote.into();
    request.headers.set("Content-Type", "application/json");
    let response = rig.router.dispatch(&mut request);
    (response.status, String::from_utf8_lossy(response.body_bytes()).into_owned())
}

#[test]
fn source_rejected_logins_do_not_exhaust_the_router_wide_budget() {
    let good = format!(r#"{{"password":"{}"}}"#, *PASSWORD);

    // Control: with no flood the administrator signs in.
    let control = production_like_rig("control");
    assert_eq!(login(&control, "192.168.1.50:40000", &good).0, 200, "control login");

    // One LAN client sends 100 login requests carrying no credential at all (a
    // one-byte invalid JSON body, so no password hashing is ever spent on it).
    let rig = production_like_rig("flood");
    let mut statuses = std::collections::BTreeMap::<u16, usize>::new();
    for _ in 0..100 {
        *statuses.entry(login(&rig, "192.168.1.66:41000", "{").0).or_default() += 1;
    }

    // The per-source limit refuses 95 attempts before the global bucket is charged.
    assert_eq!(statuses.get(&400), Some(&5));
    assert_eq!(statuses.get(&429), Some(&95));

    // The administrator, from a different address, with the correct password:
    let (status, _) = login(&rig, "192.168.1.50:40000", &good);

    assert_eq!(status, 200, "another source must still be able to log in");

    // The global limit still bounds a distributed flood from fresh sources.
    let distributed = production_like_rig("distributed");
    for host in 2..22 {
        for _ in 0..5 {
            assert_eq!(login(&distributed, &format!("192.168.1.{host}:41000"), "{").0, 400);
        }
    }
    assert_eq!(login(&distributed, "192.168.1.50:40000", &good).0, 429);
}

#[test]
fn pppoe_rejects_secret_file_indirection_at_admission_and_generation() {
    let mut cfg = default_config();
    cfg.wan.enabled = true;
    cfg.wan.username = "user".into();
    cfg.wan.password = "@/synthetic/root-only-file".into();
    assert!(cfg.validate().is_err());
    assert!(minimalrouter::services::generate_pppoe(&cfg).is_err());
    cfg.wan.enabled = false;
    assert!(cfg.validate().is_err());
    assert!(minimalrouter::services::generate_pppoe(&cfg).is_err());
    for literal in ["user@example.test", "@relative", "prefix@/literal"] {
        cfg.wan.password = literal.into();
        assert!(cfg.validate().is_ok());
        assert!(minimalrouter::services::generate_pppoe(&cfg).is_ok());
    }
}

#[test]
fn firmware_manifest_read_is_bounded_and_valid_manifest_still_loads() {
    let dir = std::env::temp_dir().join(format!("mr-manifest-cap-{}", std::process::id()));
    std::fs::create_dir_all(&dir).unwrap();
    let _guard = TempDir(dir.clone());
    let path = dir.join("manifest.json");
    std::fs::File::create(&path).unwrap().set_len((1 << 20) + 1).unwrap();
    assert!(minimalrouter::firmware::load_manifest(path.to_str().unwrap()).unwrap_err().to_string().contains("too large"));
    let manifest = minimalrouter::firmware::FirmwareManifest { version: "0.4.1".into(), ..Default::default() };
    minimalrouter::firmware::save_manifest(&manifest, path.to_str().unwrap()).unwrap();
    assert_eq!(minimalrouter::firmware::load_manifest(path.to_str().unwrap()).unwrap().version, "0.4.1");
}
