//! Health, startup timeline, diagnostics bundle, firewall runtime evidence,
//! traffic accounting and the WAN diagnosis endpoint.

use serde::Serialize;
use std::collections::BTreeMap;
use std::time::Duration as StdDuration;

use super::lan::device_labels;
use super::*;
use crate::apply::FirewallCounters;
use crate::health;

#[derive(Clone, Default)]
pub(crate) struct FirewallObservation {
    counters: Option<FirewallCounters>,
    last_increase: BTreeMap<String, Time>,
}

#[derive(Serialize, Default)]
struct FirewallRuntime {
    available: bool,
    observed_at: Option<Time>,
    default_deny: bool,
    policies: BTreeMap<String, String>,
    loaded_revision: Option<config::Revision>,
    revision_matches: bool,
    configuration_match: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    generation: String,
}

impl Server {
    /// Fed by the minute collector; HTTP reads never launch privileged
    /// commands on the UI's behalf.
    pub fn publish_firewall_observation(&self, value: Option<FirewallCounters>) {
        let mut slot = self.ext.firewall.lock().unwrap_or_else(|e| e.into_inner());
        let Some(value) = value else {
            *slot = None;
            return;
        };
        let mut increases = BTreeMap::new();
        if let Some(previous) = slot.as_ref().and_then(|p| p.counters.as_ref().map(|c| (c, &p.last_increase))) {
            let (counters, last) = previous;
            if counters.generation == value.generation {
                for (key, n) in &value.rule_packets {
                    if let Some(before) = counters.rule_packets.get(key).filter(|b| n >= b) {
                        if n > before {
                            increases.insert(key.clone(), value.observed_at);
                        } else if let Some(at) = last.get(key) {
                            increases.insert(key.clone(), *at);
                        }
                    }
                }
            }
        }
        *slot = Some(FirewallObservation { counters: Some(value), last_increase: increases });
    }

    fn firewall_runtime(&self, revision: config::Revision) -> (FirewallRuntime, FirewallObservation) {
        let mut runtime = FirewallRuntime { configuration_match: "unverified".into(), ..Default::default() };
        let Some(observation) = self.ext.firewall.lock().unwrap_or_else(|e| e.into_inner()).clone() else {
            return (runtime, FirewallObservation::default());
        };
        let Some(c) = observation.counters.clone() else { return (runtime, observation) };
        runtime.observed_at = Some(c.observed_at);
        let age = Time::now().sub(&c.observed_at);
        runtime.available = age.0 >= 0 && age <= Duration::secs(180);
        runtime.policies = c.policies.clone();
        runtime.loaded_revision = c.loaded_revision;
        runtime.generation = c.generation.clone();
        runtime.revision_matches = runtime.available && c.loaded_revision == Some(revision);
        runtime.default_deny = runtime.available && ["input", "forward", "output"].iter().all(|chain| c.policies.get(*chain).map(String::as_str) == Some("drop"));
        (runtime, observation)
    }

    pub(crate) fn firewall_health(&self, revision: config::Revision) -> health::Check {
        let (runtime, _) = self.firewall_runtime(revision);
        let mut check = health::Check { id: "firewall".into(), label: "Firewall base policy".into(), state: health::STATE_UNKNOWN.into(), summary: "Fresh firewall observation unavailable; configured policy is not runtime evidence.".into() };
        if runtime.available {
            check.state = health::STATE_DEGRADED.into();
            check.summary = "Expected default-drop base chains were not observed.".into();
            if runtime.default_deny {
                check.state = health::STATE_HEALTHY.into();
                check.summary = "Input, forward and output base policies are drop. Full ruleset equivalence is not verified.".into();
            }
            if runtime.default_deny && !runtime.revision_matches {
                check.state = health::STATE_WARNING.into();
                check.summary = "Default-drop base policies observed; current configuration revision marker is missing or differs.".into();
            }
        }
        check
    }

    pub(crate) fn handle_firewall_status(&self, _r: &mut Request<'_>) -> Response {
        #[derive(Serialize)]
        struct RuleStatus {
            id: String,
            name: String,
            scope: &'static str,
            packets: Option<u64>,
            last_increase_observed_at: Option<Time>,
        }
        let cfg = self.engine.get_current_config();
        let (runtime, observation) = self.firewall_runtime(cfg.revision);
        let pending = self.engine.get_pending_transaction().map(|tx| json!({"id": tx.id, "state": tx.current_state, "confirmation_deadline": tx.confirmation_deadline}));
        let rules: Vec<RuleStatus> = cfg
            .firewall
            .custom_rules
            .iter()
            .map(|rule| {
                let mut item = RuleStatus { id: rule.id.clone(), name: rule.name.clone(), scope: config::firewall_rule_scope(rule), packets: None, last_increase_observed_at: None };
                if let (true, true, true, Some(counters)) = (runtime.available, runtime.revision_matches, rule.enabled, observation.counters.as_ref()) {
                    let key = crate::services::firewall_rule_counter(rule);
                    item.packets = counters.rule_packets.get(&key).copied();
                    item.last_increase_observed_at = observation.last_increase.get(&key).copied();
                }
                item
            })
            .collect();
        write_json(
            200,
            &json!({
                "revision": cfg.revision, "runtime": runtime, "rules": rules, "engine": self.engine.get_status(), "pending_transaction": pending,
                "warnings": crate::services::firewall_warnings(&cfg),
                "limitations": ["Base policies and revision markers do not prove equivalence of all live rules or end-to-end reachability.", "Rule counts are since the current table generation, sampled every minute; last increase is an observation time, not a packet timestamp."],
            }),
        )
    }

    pub(crate) fn handle_firewall_explain(&self, r: &mut Request<'_>) -> Response {
        let query = r.query();
        let Some(port) = config::go_atoi(&query.get("port")) else { return Response::error("port must be an integer", 400) };
        let flow = crate::services::FirewallFlow { source_ip: query.get("source_ip"), destination_ip: query.get("destination_ip"), protocol: query.get("protocol"), port };
        match crate::services::explain_firewall_flow(&self.engine.get_current_config(), &flow) {
            Ok(result) => write_json(200, &result),
            Err((_, err)) => Response::error(&err.to_string(), 400),
        }
    }

    pub fn configure_accounting_store(&self, store: Option<Arc<crate::accounting::Store>>) {
        self.subsystems_mut().accounting = store;
    }

    pub(crate) fn handle_get_accounting(&self, r: &mut Request<'_>) -> Response {
        let cfg = self.engine.get_current_config();
        let mut snapshot = crate::accounting::Snapshot { enabled: cfg.accounting.enabled, updated_at: Time::now_utc(), ..Default::default() };
        let Some(store) = self.subsystems().accounting.clone() else { return write_json(200, &snapshot) };
        snapshot.available = true;
        // Disabling promises deletion; rows awaiting the collector's next
        // tick must not be served against the setting.
        if !cfg.accounting.enabled {
            return write_json(200, &snapshot);
        }
        let raw = r.query().get("months");
        let mut months = 3;
        if !raw.is_empty() {
            match config::go_atoi(&raw).filter(|n| (1..=24).contains(n)) {
                Some(n) => months = n,
                None => return Response::error("months must be between 1 and 24", 400),
            }
        }
        let Ok(mut usage) = store.months(months) else { return Response::error("Traffic accounting is unavailable", 503) };
        let labels = device_labels(&cfg);
        for month in usage.iter_mut() {
            for device in month.devices.iter_mut() {
                if let Some((hostname, mac)) = labels.get(&device.address) {
                    device.hostname = hostname.clone();
                    device.mac = mac.clone();
                }
            }
        }
        snapshot.months = usage;
        write_json(200, &snapshot)
    }

    pub(crate) fn handle_accounting_insights(&self, r: &mut Request<'_>) -> Response {
        let mut period = r.query().get("period");
        if period.is_empty() {
            period = "today".into();
        }
        let now = Time::now_utc();
        let (from, until, _) = match crate::accounting::insights_range(now, &period) {
            Ok(range) => range,
            Err(err) => return Response::error(&err.to_string(), 400),
        };
        let cfg = self.engine.get_current_config();
        let store = self.subsystems().accounting.clone();
        let mut result = crate::accounting::Insights { available: store.is_some(), enabled: cfg.accounting.enabled, period: period.clone(), from, until, ..Default::default() };
        if let Some(store) = store.filter(|_| cfg.accounting.enabled) {
            match store.insights(now, &period) {
                Ok(insights) => result = insights,
                Err(_) => return Response::error("Traffic history is unavailable", 503),
            }
            let labels = device_labels(&cfg);
            for device in result.devices.iter_mut() {
                if let Some((hostname, mac)) = labels.get(&device.address) {
                    device.hostname = hostname.clone();
                    device.mac = mac.clone();
                }
            }
        }
        write_json(200, &result)
    }

    pub(crate) fn handle_firewall_activity(&self, _r: &mut Request<'_>) -> Response {
        let now = Time::now_utc();
        let result = match self.subsystems().accounting.clone() {
            Some(store) => store.firewall_activity(now),
            None => {
                let until = now.truncate(Duration::MINUTE).add(Duration::MINUTE);
                Ok(crate::accounting::FirewallActivity { from: until.add(Duration::hours(-24)), until: now, ..Default::default() })
            }
        };
        match result {
            Ok(activity) => write_json(200, &activity),
            Err(_) => Response::error("Firewall activity unavailable", 503),
        }
    }

    pub(crate) fn health_snapshot(&self) -> health::Snapshot {
        let cfg = self.engine.get_current_config();
        let runtime = runtime_snapshot(&cfg.wan.interface, cfg.runtime_lan_interface(), &appliance_data_dir());
        let facts = health::inspect_runtime_facts(&cfg);
        let monitor = self.subsystems().gateway.clone();
        let (gateway, gateway_configured) = match &monitor {
            Some(monitor) => (monitor.summary(), true),
            None => (Default::default(), false),
        };
        let store = self.credential_store();
        let mut backup_unavailable = store.is_none();
        let mut last_backup = None;
        if let Some(store) = &store {
            match store.last_backup_export() {
                Ok(last) => last_backup = last,
                Err(err) => {
                    backup_unavailable = true;
                    log::info!("[HEALTH] backup export status unavailable: {}", err);
                }
            }
        }
        let (resolves, dns_error) = health::probe_functional_dns(StdDuration::from_secs(4));
        health::build(&health::Input {
            firewall: Some(self.firewall_health(cfg.revision)),
            runtime,
            engine: self.engine.get_status(),
            gateway,
            gateway_configured,
            update_trust_configured: self.update_trust_configured(),
            facts,
            last_backup_at: last_backup,
            backup_history_unavailable: backup_unavailable,
            dns_resolves: Some(resolves),
            dns_error: dns_error.unwrap_or_default(),
            now: Time::now_utc(),
            config: cfg,
        })
    }

    pub(crate) fn handle_get_health(&self, _r: &mut Request<'_>) -> Response {
        write_json(200, &self.health_snapshot())
    }

    /// Called after TLS initialization and listener binding: registering
    /// routes alone is not evidence of management readiness.
    pub fn start_startup_capture(&self, stop: crate::shutdown::Shutdown) {
        let cfg = self.engine.get_current_config();
        let recorder = match crate::startup::Recorder::new(&appliance_data_dir()) {
            Ok(recorder) => recorder,
            Err(err) => {
                log::info!("[STARTUP] timeline unavailable: {}", err);
                return;
            }
        };
        recorder.event("reconcile", "Canonical configuration reconciled");
        recorder.ready("management");
        std::thread::spawn(move || recorder.run(&stop, cfg.wan.enabled, &cfg.wireguard.interface, cfg.wireguard.enabled));
    }

    pub(crate) fn handle_startup_boots(&self, _r: &mut Request<'_>) -> Response {
        let Ok(boots) = crate::startup::load(&appliance_data_dir()) else {
            return Response::error(r#"{"error":"startup diagnostics unavailable"}"#, 503);
        };
        let summaries: Vec<_> = boots.iter().map(|b| crate::startup::summarize(b, Time::now())).collect();
        Response::json(200, &json!({"boots": summaries, "retained_boots": crate::startup::MAX_BOOTS, "capture_minutes": crate::startup::WINDOW.0 / Duration::MINUTE.0, "generated_at": Time::now_utc()}))
    }

    pub(crate) fn handle_startup_boot(&self, r: &mut Request<'_>) -> Response {
        match crate::startup::load_boot(&appliance_data_dir(), &r.path_value("id")) {
            Ok(boot) => Response::json(200, &json!({"boot": boot, "status": boot.capture_status(Time::now()), "generated_at": Time::now_utc()})),
            Err(err) if err.downcast_ref::<std::io::Error>().is_some_and(|e| e.kind() == std::io::ErrorKind::NotFound) => Response::error("Startup capture not found or no longer retained", 404),
            Err(_) => Response::error("Startup diagnostics unavailable", 503),
        }
    }

    pub(crate) fn build_recovery_diagnostics(&self, cfg: &SystemConfig) -> Result<Vec<u8>> {
        let base = telemetry::build_diagnostic_bundle(cfg)?;
        let mut bundle: serde_json::Map<String, Value> = serde_json::from_slice(&base)?;
        bundle.remove("service_health");
        let mut errors: Vec<&str> = Vec::new();
        bundle.insert("health".into(), serde_json::to_value(self.health_snapshot())?);
        let runtime = runtime_snapshot(&cfg.wan.interface, cfg.runtime_lan_interface(), &appliance_data_dir());
        bundle.insert(
            "runtime".into(),
            json!({"available": runtime.available, "os": runtime.os, "architecture": runtime.architecture, "uptime_seconds": runtime.uptime_seconds, "cpu_percent": runtime.cpu_load_percent, "memory_used_bytes": runtime.memory_used_bytes, "memory_total_bytes": runtime.memory_total_bytes, "storage": runtime.storage}),
        );
        bundle.insert("engine".into(), serde_json::to_value(self.engine.get_status())?);
        if let Some(pending) = self.engine.get_pending_transaction() {
            bundle.insert("pending_transaction".into(), json!({"id": pending.id, "state": pending.current_state, "confirmation_deadline": pending.confirmation_deadline}));
        }
        let operation = {
            let _guard = self.recovery_mu.lock().unwrap_or_else(|e| e.into_inner());
            self.recovery_operation()
        };
        match operation {
            Err(_) => errors.push("Recovery operation unavailable"),
            Ok(Some(mut op)) => {
                // Outcomes, not DNS exception domains or reasons.
                op.dns_policy = None;
                bundle.insert("recovery_operation".into(), serde_json::to_value(op)?);
            }
            Ok(None) => {}
        }
        if let Some(store) = self.engine_store() {
            match store.last_backup_export() {
                Ok(last) => {
                    bundle.insert("last_backup_export_at".into(), serde_json::to_value(last)?);
                }
                Err(_) => errors.push("Backup export history unavailable"),
            }
            match store.query_audit_events(&config::AuditQuery { limit: 100, category: "recovery".into(), ..Default::default() }) {
                Ok(page) => {
                    bundle.insert("recovery_events".into(), serde_json::to_value(page)?);
                }
                Err(_) => errors.push("Recovery audit history unavailable"),
            }
        }
        match crate::startup::load(&appliance_data_dir()) {
            Ok(boots) => {
                let summaries: Vec<_> = boots.iter().map(|b| crate::startup::summarize(b, Time::now())).collect();
                bundle.insert("startup".into(), serde_json::to_value(summaries)?);
            }
            Err(_) => errors.push("Startup history unavailable"),
        }
        if let Some(filter) = self.dns_filter() {
            match filter.status(Instant::now() + StdDuration::from_secs(30)) {
                Ok(status) => {
                    let applied = &status.applied;
                    bundle.insert("dns_filter".into(), json!({"revision": applied.policy.revision, "categories": applied.policy.categories, "exception_count": applied.policy.exceptions.len(), "healthy": applied.healthy, "updating": status.updating, "error": status.error, "applied_at": applied.applied_at}));
                }
                Err(_) => errors.push("DNS status unavailable"),
            }
        }
        bundle.insert("collection_errors".into(), json!(errors));
        Ok(crate::goish::json::marshal_indent(&bundle, "  ")?)
    }

    /// Probes the WAN path layer by layer: PPPoE link, public reachability,
    /// DNS and HTTPS. The first failing layer names the cause.
    pub(crate) fn handle_network_diagnose(&self, _r: &mut Request<'_>) -> Response {
        #[derive(Serialize)]
        struct Check {
            ok: bool,
            detail: &'static str,
        }
        let Some(monitor) = self.subsystems().gateway.clone() else {
            return write_json(503, &json!({"error": "Gateway monitoring is unavailable."}));
        };
        let summary = match monitor.probe_now() {
            Ok(summary) => summary,
            Err(err) => return write_json(409, &json!({"error": err.to_string()})),
        };
        let mut checks: BTreeMap<&str, Check> = BTreeMap::new();
        let link = summary.link.connected;
        checks.insert("pppoe", Check { ok: link, detail: if link { "PPPoE link is connected" } else { "PPPoE link is disconnected" } });
        let reachable = summary.targets.iter().any(|t| t.reachable);
        checks.insert("internet", Check { ok: reachable, detail: if reachable { "Public monitoring target reachable" } else { "No monitoring target replied" } });
        let dns_ok = lookup_with_timeout("example.com:443", StdDuration::from_secs(3));
        checks.insert("dns", Check { ok: dns_ok, detail: if dns_ok { "DNS resolution works" } else { "DNS lookup failed" } });
        let agent = ureq::AgentBuilder::new().timeout(StdDuration::from_secs(5)).timeout_connect(StdDuration::from_secs(3)).redirects(0).build();
        let https_ok = match agent.head("https://example.com/").call() {
            Ok(resp) => (200..500).contains(&resp.status()),
            Err(ureq::Error::Status(code, _)) => (200..500).contains(&code),
            Err(_) => false,
        };
        checks.insert("https", Check { ok: https_ok, detail: if https_ok { "HTTPS connectivity works" } else { "HTTPS request failed" } });
        let (cause, overall) = if !checks["pppoe"].ok {
            ("pppoe_link", "failed")
        } else if !checks["internet"].ok {
            ("internet_reachability", "failed")
        } else if !checks["dns"].ok {
            ("dns", "degraded")
        } else if !checks["https"].ok {
            ("https", "degraded")
        } else {
            ("healthy", "healthy")
        };
        write_json(200, &json!({"overall": overall, "cause": cause, "checks": checks}))
    }
}

/// The system resolver bounded by a timeout (getaddrinfo has no deadline).
fn lookup_with_timeout(host: &'static str, timeout: StdDuration) -> bool {
    let (tx, rx) = std::sync::mpsc::channel();
    std::thread::spawn(move || {
        let _ = tx.send(std::net::ToSocketAddrs::to_socket_addrs(host).map(|mut a| a.next().is_some()).unwrap_or(false));
    });
    rx.recv_timeout(timeout).unwrap_or(false)
}
