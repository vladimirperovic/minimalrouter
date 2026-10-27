//! First-run setup and the bounded WAN speed test.

use serde::Serialize;
use std::io::Read;
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::Mutex as StdMutex;
use std::time::Duration as StdDuration;

use super::*;

impl Server {
    pub(crate) fn handle_setup_status(&self, _r: &mut Request<'_>) -> Response {
        if self.administrator_configured() {
            // Only a routing hint for the UI once setup is complete; no
            // interfaces, addresses or recovery details for anonymous clients.
            return write_json(200, &json!({"is_configured": true}));
        }
        let cfg = self.engine.get_current_config();
        let status = self.engine.get_status();
        write_json(200, &json!({"is_configured": false, "wan_interface": cfg.wan.interface, "lan_interface": cfg.lan.interface, "lan_ip": cfg.lan.ip_address, "recovery_required": status.recovery_required, "recovery_reason": status.recovery_reason}))
    }

    pub(crate) fn handle_setup_apply(&self, r: &mut Request<'_>) -> Response {
        if !self.rate_limiter.allow(&remote_ip(&r.remote_addr)) {
            return Response::json(429, &json!({"error": "Too many setup attempts. Please wait."}));
        }
        // The canonical store decides: a credential created by the local
        // console closes this unauthenticated endpoint too.
        match self.admin_credential_hash() {
            Err(err) => {
                log::info!("[AUTH] Setup refused: administrator state unavailable: {}", err);
                return Response::json(503, &json!({"error": "Administrator state is unavailable."}));
            }
            Ok(hash) if !hash.is_empty() => {
                log::info!("[AUTH] Blocked wizard re-run attempt from {}", r.remote_addr);
                return Response::json(403, &json!({"error": "System is already configured. Use the dashboard to change settings."}));
            }
            Ok(_) => {}
        }
        let status = self.engine.get_status();
        if status.recovery_required {
            return Response::json(503, &json!({"error": "Initial setup is blocked until local recovery succeeds.", "reason": status.recovery_reason}));
        }
        #[derive(serde::Deserialize, Default)]
        #[serde(default)]
        struct WizardSetupRequest {
            wan_interface: String,
            pppoe_username: String,
            pppoe_password: String,
            admin_password: String,
            lan_interface: String,
            lan_ip_address: String,
        }
        let Ok(req) = decode_json::<WizardSetupRequest>(r) else { return Response::error("Invalid JSON payload", 400) };
        if req.admin_password.chars().count() < 12 {
            return Response::error("Administrator password must be at least 12 characters long", 422);
        }
        if req.pppoe_username.is_empty() != req.pppoe_password.is_empty() {
            return Response::error("Provide both PPPoE username and password, or leave both empty", 422);
        }
        let hashed = match auth::hash_password(&req.admin_password) {
            Ok(hash) => hash,
            Err(err) => return Response::error(&format!("Failed to hash password: {}", err), 500),
        };
        let mut cfg = config::default_config();
        // First-run setup replaces the pre-setup revision exactly once.
        cfg.revision = self.engine.get_current_config().revision;
        // External and wireless integrations are always opt-in.
        cfg.cloudflare.ddns_enabled = false;
        cfg.cloudflare.tunnel_enabled = false;
        cfg.wifi.enabled = false;
        if !req.wan_interface.is_empty() {
            cfg.wan.interface = req.wan_interface;
        }
        cfg.wan.enabled = !req.pppoe_username.is_empty() || !req.pppoe_password.is_empty();
        cfg.wan.username = req.pppoe_username;
        cfg.wan.password = req.pppoe_password;
        if !req.lan_interface.is_empty() {
            cfg.lan.interface = req.lan_interface;
        }
        if !req.lan_ip_address.is_empty() {
            if req.lan_ip_address != cfg.lan.ip_address {
                return Response::error("Complete first-run setup on the default LAN address; change the LAN address afterward using commit-confirmed configuration", 422);
            }
            cfg.lan.cidr = format!("{}/24", req.lan_ip_address);
            cfg.lan.ip_address = req.lan_ip_address;
        }
        let Some(store) = self.engine_store() else { return Response::error("Initial setup store unavailable", 503) };
        let tx_id = format!("wizard-setup-{}", Time::now().unix_nano());
        let commit = |applied: &SystemConfig| store.commit_initial_setup(applied.clone(), &hashed);
        let (tx, result) = self.engine.process_initial_setup(&tx_id, cfg.clone(), &commit);
        self.audit_config_request(r, Some(&tx));
        if let Err(err) = result {
            let status = if tx.current_state == apply::State::RecoveryRequired { 503 } else { 422 };
            return Response::json(status, &json!({"error": format!("Wizard apply failed: {}", err), "tx": redact_transaction(Some(&tx))}));
        }
        // Network revision and credential were committed together; only now
        // is the hash published in memory.
        *self.admin_hash.write().unwrap_or_else(|e| e.into_inner()) = hashed;
        log::info!("[AUTH] Wizard completed: admin password set, system configured from {}", r.remote_addr);
        self.append_audit("system.setup_completed", &audit_actor(&r.remote_addr), details(&[("wan_interface", &cfg.wan.interface), ("lan_interface", &cfg.lan.interface)]));
        let redirect = format!("https://{}", net::join_host_port(&cfg.lan.ip_address, &cfg.system.https_port.to_string()));
        // Setup already succeeded; a transient session failure only means an
        // explicit login.
        match self.session_mgr.create_with_mode(false) {
            None => {
                log::info!("[AUTH] Initial setup committed but durable session creation failed; explicit login required");
                self.append_audit("system.setup_session_failed", &audit_actor(&r.remote_addr), details(&[("result", "setup_committed_login_required")]));
                write_json(200, &json!({"success": true, "requires_login": true, "redirect_url": redirect, "tx": redact_transaction(Some(&tx))}))
            }
            Some(session) => write_json(200, &json!({"success": true, "csrf_token": session.csrf_token, "redirect_url": redirect, "tx": redact_transaction(Some(&tx))}))
                .with_header("Set-Cookie", cookie_header(&session.id, auth::ABSOLUTE_TIMEOUT.0 / 1_000_000_000, self.session_mgr.secure_cookies())),
        }
    }
}

const SPEEDTEST_DOWNLOAD_BYTES: u64 = 50 << 20;
const SPEEDTEST_UPLOAD_BYTES: u64 = 25 << 20;
const ESTIMATE_DOWNLOAD_BYTES: u64 = 8 << 20;
const ESTIMATE_UPLOAD_BYTES: u64 = 4 << 20;
const SPEEDTEST_SUGGESTED_PCT: f64 = 90.0;
/// Per-phase transfer budget: fast lines finish on bytes, slow lines yield a
/// smaller but real sample instead of timing out.
const SPEEDTEST_PHASE: StdDuration = StdDuration::from_secs(15);
const ESTIMATE_PHASE: StdDuration = StdDuration::from_secs(8);
const MINIMUM_MEASURED_BYTES: u64 = 256 << 10;
/// Bytes handed to the kernel are not bytes received, so the upload sample
/// is sized from a probe instead of being cut short.
const UPLOAD_PROBE_BYTES: u64 = 256 << 10;
const UPLOAD_PROBE_TIMEOUT: StdDuration = StdDuration::from_secs(30);
const MINIMUM_SAMPLE_FRACTION: f64 = 0.5;
const SPEEDTEST_HOST: &str = "https://speed.cloudflare.com";

/// Two concurrent runs would each report half the line.
static SPEEDTEST: StdMutex<()> = StdMutex::new(());

#[derive(Serialize)]
struct SpeedtestResult {
    download_mbps: f64,
    upload_mbps: f64,
    suggested_download_mbps: i64,
    suggested_upload_mbps: i64,
    qos_temporarily_bypassed: bool,
    estimate: bool,
}

/// IPv4 only (the appliance has no IPv6 default route); bounded setup and
/// header waits, but no overall timeout: each phase has its own budget.
fn speedtest_agent(read_timeout: StdDuration) -> ureq::Agent {
    ureq::AgentBuilder::new()
        .timeout_connect(StdDuration::from_secs(15))
        .timeout_read(read_timeout)
        .resolver(|addr: &str| -> std::io::Result<Vec<std::net::SocketAddr>> { Ok(std::net::ToSocketAddrs::to_socket_addrs(addr)?.filter(|a| a.is_ipv4()).collect()) })
        .build()
}

fn mbps(bytes: u64, seconds: f64) -> f64 {
    bytes as f64 * 8.0 / 1e6 / seconds
}

fn positive_seconds(d: StdDuration) -> f64 {
    if d.is_zero() { 0.001 } else { d.as_secs_f64() }
}

fn round_pct(mbps: f64, pct: f64) -> i64 {
    ((mbps * pct / 100.0) as i64).max(1)
}

fn measure_download(sample: u64, budget: StdDuration) -> std::result::Result<f64, String> {
    let response = speedtest_agent(StdDuration::from_secs(30)).get(&format!("{}/__down?bytes={}", SPEEDTEST_HOST, sample)).call().map_err(|e| match e {
        ureq::Error::Status(code, _) => format!("download endpoint returned HTTP {}", code),
        other => other.to_string(),
    })?;
    if !(200..300).contains(&response.status()) {
        return Err(format!("download endpoint returned HTTP {}", response.status()));
    }
    // Bounded: a misbehaving host cannot turn this into an unbounded pull.
    let mut reader = response.into_reader().take(sample);
    let (start, mut n, mut stopped_by_budget) = (Instant::now(), 0u64, false);
    let mut buf = vec![0u8; 64 * 1024];
    loop {
        if start.elapsed() >= budget {
            stopped_by_budget = true;
            break;
        }
        match reader.read(&mut buf) {
            Ok(0) => break,
            Ok(read) => n += read as u64,
            Err(_) if start.elapsed() >= budget => {
                stopped_by_budget = true;
                break;
            }
            Err(err) => return Err(err.to_string()),
        }
    }
    let elapsed = start.elapsed();
    if !stopped_by_budget && (n as f64) < sample as f64 * MINIMUM_SAMPLE_FRACTION {
        return Err(format!("download sample was too small to measure ({} of {} bytes)", n, sample));
    }
    if n < MINIMUM_MEASURED_BYTES {
        return Err(format!("download moved too little data to measure ({} bytes in {}s)", n, elapsed.as_secs_f64().round()));
    }
    Ok(mbps(n, positive_seconds(elapsed)))
}

/// Starts the clock at the first byte the transport reads from the body, so
/// DNS and handshakes are not charged to the upload.
struct TimedBody {
    remaining: u64,
    created: Instant,
    first_read: Arc<AtomicI64>,
    sent: Arc<AtomicI64>,
}

impl Read for TimedBody {
    fn read(&mut self, buf: &mut [u8]) -> std::io::Result<usize> {
        let _ = self.first_read.compare_exchange(0, self.created.elapsed().as_nanos() as i64 + 1, Ordering::SeqCst, Ordering::SeqCst);
        let n = buf.len().min(self.remaining as usize);
        if n == 0 {
            return Ok(0);
        }
        crate::goish::rand::fill(&mut buf[..n]).map_err(|e| std::io::Error::other(e.to_string()))?;
        self.remaining -= n as u64;
        self.sent.fetch_add(n as i64, Ordering::SeqCst);
        Ok(n)
    }
}

/// Bytes per second received by the server for one upload.
fn upload_once(sample: u64, timeout: StdDuration) -> std::result::Result<f64, String> {
    let (first_read, sent) = (Arc::new(AtomicI64::new(0)), Arc::new(AtomicI64::new(0)));
    let created = Instant::now();
    let body = TimedBody { remaining: sample, created, first_read: first_read.clone(), sent: sent.clone() };
    let agent = ureq::AgentBuilder::new().timeout(timeout).timeout_connect(StdDuration::from_secs(15)).resolver(|addr: &str| -> std::io::Result<Vec<std::net::SocketAddr>> { Ok(std::net::ToSocketAddrs::to_socket_addrs(addr)?.filter(|a| a.is_ipv4()).collect()) }).build();
    let response = agent
        .post(&format!("{}/__up?bytes={}", SPEEDTEST_HOST, sample))
        .set("Content-Type", "application/octet-stream")
        .set("Content-Length", &sample.to_string())
        .send(body)
        .map_err(|e| match e {
            ureq::Error::Status(code, _) => format!("upload endpoint returned HTTP {}", code),
            other => other.to_string(),
        })?;
    let answered = created.elapsed();
    let _ = std::io::copy(&mut response.into_reader().take(1 << 20), &mut std::io::sink());
    let (first, sent) = (first_read.load(Ordering::SeqCst), sent.load(Ordering::SeqCst));
    if first == 0 {
        return Err("upload endpoint answered without reading the request body; no upload rate was measured".into());
    }
    if (sent as f64) < sample as f64 * MINIMUM_SAMPLE_FRACTION {
        return Err(format!("upload sample was too small to measure ({} of {} bytes)", sent, sample));
    }
    let elapsed = answered.saturating_sub(StdDuration::from_nanos((first - 1) as u64));
    Ok(sent as f64 / positive_seconds(elapsed))
}

fn measure_upload(max_bytes: u64, budget: StdDuration) -> std::result::Result<f64, String> {
    let probe = UPLOAD_PROBE_BYTES.min(max_bytes);
    let probe_rate = upload_once(probe, UPLOAD_PROBE_TIMEOUT)?;
    let sample = ((probe_rate * budget.as_secs_f64()) as u64).max(MINIMUM_MEASURED_BYTES).min(max_bytes);
    if sample <= probe {
        // The probe already filled the phase on a very slow line.
        return Ok(probe_rate * 8.0 / 1e6);
    }
    Ok(upload_once(sample, budget * 2 + StdDuration::from_secs(15))? * 8.0 / 1e6)
}

impl Server {
    pub(crate) fn handle_speedtest(&self, r: &mut Request<'_>) -> Response {
        log::info!("[API] POST {} from {}", r.path, r.remote_addr);
        let mode = r.query().get("mode");
        if !mode.is_empty() && mode != "estimate" {
            return Response::error("Unsupported speed test mode", 400);
        }
        let estimate = mode == "estimate";
        // The first-run estimate is advisory and kept small.
        let (download, upload, phase) = if estimate { (ESTIMATE_DOWNLOAD_BYTES, ESTIMATE_UPLOAD_BYTES, ESTIMATE_PHASE) } else { (SPEEDTEST_DOWNLOAD_BYTES, SPEEDTEST_UPLOAD_BYTES, SPEEDTEST_PHASE) };
        let Ok(_guard) = SPEEDTEST.try_lock() else { return Response::error("A speed test is already running.", 409) };
        let qos_was_enabled = self.engine.get_current_config().qos.enabled;
        let (mut down, mut up) = (0.0, 0.0);
        let result = self.engine.with_qos_bypassed(|| {
            down = measure_download(download, phase).map_err(|e| crate::errorf!("download speed test failed: {}", e))?;
            up = measure_upload(upload, phase).map_err(|e| crate::errorf!("upload speed test failed: {}", e))?;
            Ok(())
        });
        if let Err(err) = result {
            return Response::error(&format!("Speed test failed: {}", err), 500);
        }
        Response::json(
            200,
            &SpeedtestResult {
                download_mbps: down,
                upload_mbps: up,
                suggested_download_mbps: round_pct(down, SPEEDTEST_SUGGESTED_PCT),
                suggested_upload_mbps: round_pct(up, SPEEDTEST_SUGGESTED_PCT),
                qos_temporarily_bypassed: qos_was_enabled,
                estimate,
            },
        )
    }
}
