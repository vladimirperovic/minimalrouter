//! DNS filtering, DNS activity history and DNS risk alerts.

use serde::Serialize;
use std::collections::{BTreeMap, HashMap};
use std::sync::LazyLock;
use std::time::Duration as StdDuration;

use super::lan::device_labels;
use super::*;
use crate::accounting::{DNSActivity, DNSCollectionStatus, DNSCollector, DNSQuery, DNSSettings, DEFAULT_DNS_RETENTION_DAYS};
use crate::dnsrisk;
use crate::services::{self, ProfileStatus};
use crate::telemetry::DHCPLease;

const STATUS_DEADLINE: StdDuration = StdDuration::from_secs(30);

/// Highlighting-only extensions of the device-profile service lists.
const EXTRA_ADULT_DOMAINS: [&str; 17] = [
    "phncdn.com", "xvideos-cdn.com", "xhamster.com", "xhcdn.com", "fansly.com", "redtube.com", "youporn.com", "tube8.com", "spankbang.com", "eporner.com", "beeg.com", "motherless.com",
    "chaturbate.com", "stripchat.com", "bongacams.com", "livejasmin.com", "cam4.com",
];

const SERVICE_NAMES: [&str; 11] = ["youtube", "steam", "wiki", "tiktok", "instagram", "facebook", "roblox", "epic", "twitch", "adult", "gaming"];

/// Site -> device-profile service; names in sorted order, first claim wins.
/// The aggregate "gaming" service only repeats other services' domains.
static DNS_CATEGORIES: LazyLock<BTreeMap<String, String>> = LazyLock::new(|| {
    let mut lists: BTreeMap<&str, Vec<&str>> = BTreeMap::new();
    for name in SERVICE_NAMES.iter().filter(|n| **n != "gaming") {
        lists.entry(name).or_default().extend(services::service_domains(name).unwrap_or(&[]));
    }
    lists.entry("adult").or_default().extend(EXTRA_ADULT_DOMAINS);
    let mut out = BTreeMap::new();
    for (name, domains) in lists {
        for domain in domains {
            out.entry(domain.to_string()).or_insert_with(|| name.to_string());
        }
    }
    out
});

fn category(site: &str) -> String {
    DNS_CATEGORIES.get(site).cloned().unwrap_or_default()
}

/// Go's anonymous struct embedding: the fields of both values in one object.
fn merge<A: Serialize, B: Serialize>(a: &A, b: &B) -> Value {
    let mut left = serde_json::to_value(a).unwrap_or(Value::Null);
    if let (Some(map), Ok(Value::Object(right))) = (left.as_object_mut(), serde_json::to_value(b)) {
        map.extend(right);
    }
    left
}

#[derive(Serialize, Clone)]
struct DNSFilterDevice {
    ip: String,
    name: String,
    reserved: bool,
}

#[derive(Serialize)]
struct DNSFilterContext {
    config_revision: config::Revision,
    router_time: String,
    timezone: String,
    bundled_enabled: bool,
    profiles: Vec<ProfileStatus>,
    devices: Vec<DNSFilterDevice>,
    blockers: Vec<String>,
}

#[derive(Serialize)]
struct DNSDecisionLayer {
    source: String,
    action: String,
    reason: String,
}

fn layer(source: &str, action: &str, reason: String) -> DNSDecisionLayer {
    DNSDecisionLayer { source: source.into(), action: action.into(), reason }
}

fn local_dns_match(cfg: &SystemConfig, leases: &[DHCPLease], domain: &str) -> Option<(&'static str, String)> {
    if let Some(record) = cfg.dns.records.iter().find(|r| r.name.trim_end_matches('.').eq_ignore_ascii_case(domain)) {
        return Some(("local_record", record.ip.clone()));
    }
    if !cfg.dhcp.enabled {
        return None;
    }
    let matches = |name: &str| !name.is_empty() && name != "*" && (name.eq_ignore_ascii_case(domain) || format!("{}.{}", name, cfg.system.domain).eq_ignore_ascii_case(domain));
    // Only active leases evidence a DHCP name; a reservation is not proof the
    // resolver publishes it.
    leases.iter().find(|l| matches(&l.hostname)).map(|l| ("dhcp_name", l.ip_address.clone()))
}

fn explain_dns_domain(cfg: &SystemConfig, leases: &[DHCPLease], mut check: dnsfilter::Check, device: &str, now: Time) -> Value {
    let mut layers = Vec::new();
    let coverage = [
        "A policy check is not proof of a blocked query or a visit.",
        "Device schedules depend on router DNS answers and IPv4 destination sets; runtime enforcement is not measured per profile.",
        "External encrypted DNS, VPNs and mobile data can bypass DNS category filtering.",
    ];
    if cfg.adguard.enabled && !check.exception {
        for domain in services::builtin_blocklist() {
            if check.domain == *domain || check.domain.ends_with(&format!(".{}", domain)) {
                check.action = "Block".into();
                check.matches.push(dnsfilter::Match { category: "bundled".into(), domain: domain.to_string(), enabled: true });
            }
        }
    }
    for m in &check.matches {
        layers.push(layer(&m.category, if m.enabled { "block" } else { "inactive" }, format!("Matches {}", m.domain)));
    }
    if check.exception {
        layers.push(layer("exception", "allow", "An exception allows this domain and its subdomains through DNS lists; device schedules still apply.".into()));
    }
    let local = local_dns_match(cfg, leases, &check.domain);
    if let Some((kind, address)) = &local {
        check.action = "Local DNS record".into();
        layers.push(layer(kind, "local", format!("Local address {} takes precedence over DNS lists.", address)));
    }
    let mut profile_out = None;
    if let Some(profile) = services::device_profile_statuses(cfg, now).into_iter().find(|p| p.ip_addresses.iter().any(|ip| ip == device)) {
        let matched = profile.services.iter().any(|service| services::service_domains(service).unwrap_or(&[]).iter().any(|d| check.domain == *d || check.domain.ends_with(&format!(".{}", d))));
        if matched {
            layers.push(layer("device_schedule", &profile.state, format!("Configured schedule for {}; actual packet enforcement remains unverified.", profile.name)));
            if profile.state == "blocked" && local.is_none() && check.action != "Block" {
                check.action = "Scheduled block".into();
            }
        }
        profile_out = Some(profile);
    }
    if layers.is_empty() {
        layers.push(layer("lists", "no_match", "No matching rule in the available local catalogs. This does not prove unrestricted access.".into()));
    }
    let mut out = serde_json::to_value(&check).unwrap_or(Value::Null);
    if let Some(map) = out.as_object_mut() {
        map.insert("config_revision".into(), json!(cfg.revision));
        if !device.is_empty() {
            map.insert("device_ip".into(), json!(device));
        }
        map.insert("scope".into(), json!("configured_policy"));
        map.insert("layers".into(), serde_json::to_value(layers).unwrap_or(Value::Null));
        if let Some(profile) = profile_out {
            map.insert("profile".into(), serde_json::to_value(profile).unwrap_or(Value::Null));
        }
        map.insert("coverage".into(), json!(coverage));
    }
    out
}

/// An empty value or one IP address in canonical form.
fn parse_dns_device(raw: &str) -> Option<String> {
    if raw.is_empty() {
        return Some(String::new());
    }
    if raw.contains('%') {
        return None;
    }
    let ip: std::net::IpAddr = raw.parse().ok()?;
    Some(match ip {
        std::net::IpAddr::V6(v6) => v6.to_ipv4_mapped().map(|v4| v4.to_string()).unwrap_or_else(|| v6.to_string()),
        v4 => v4.to_string(),
    })
}

/// A short lowercase fragment of a site name.
fn parse_dns_search(raw: &str) -> Option<String> {
    let search = raw.trim().to_lowercase();
    (search.len() <= 64 && search.bytes().all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || b"-_.".contains(&c))).then_some(search)
}

impl Server {
    pub fn configure_dns_filter_service(self: &Arc<Self>, service: Option<Arc<dnsfilter::Service>>) {
        if let Some(service) = &service {
            let server = Arc::downgrade(self);
            service.set_observer(Box::new(move |op: &dnsfilter::Operation| {
                let Some(server) = server.upgrade() else { return };
                let event = if op.state == "running" { format!("dns_filter.{}_requested", op.kind) } else { format!("dns_filter.{}", op.state) };
                server.append_audit(
                    &event,
                    &op.actor,
                    details(&[("operation_id", &op.id), ("kind", &op.kind), ("phase", &op.phase), ("base_revision", &op.base_revision.to_string()), ("applied_revision", &op.applied_revision.to_string()), ("error", &op.error)]),
                );
            }));
        }
        self.configure_dns_filter(service);
    }

    pub fn configure_dns_activity(&self, collector: Option<Arc<DNSCollector>>) {
        self.subsystems_mut().dns_collector = collector;
    }

    pub fn configure_dns_risk(&self, service: Option<Arc<dnsrisk::Service>>) {
        self.subsystems_mut().dns_risk = service;
    }

    fn require_dns_filter(&self) -> std::result::Result<Arc<dnsfilter::Service>, Response> {
        self.dns_filter().ok_or_else(|| Response::error("DNS filtering is unavailable", 503))
    }

    fn dns_filter_context(&self) -> DNSFilterContext {
        let (cfg, now) = (self.engine.get_current_config(), Time::now());
        let mut devices: BTreeMap<String, DNSFilterDevice> = BTreeMap::new();
        for lease in telemetry::current_dhcp_leases() {
            devices.insert(lease.ip_address.clone(), DNSFilterDevice { ip: lease.ip_address.clone(), name: lease.hostname.clone(), reserved: false });
        }
        for lease in cfg.dhcp.static_leases.iter() {
            devices.insert(lease.ip_address.clone(), DNSFilterDevice { ip: lease.ip_address.clone(), name: lease.hostname.clone(), reserved: true });
        }
        let mut blockers = Vec::new();
        if self.engine.get_pending_transaction().is_some() {
            blockers.push("Confirm or roll back the pending network change first.".to_string());
        }
        if self.engine.get_status().recovery_required {
            blockers.push("Resolve configuration recovery before changing DNS protection.".to_string());
        }
        DNSFilterContext {
            config_revision: cfg.revision,
            router_time: now.rfc3339(),
            timezone: "Local".into(),
            bundled_enabled: cfg.adguard.enabled,
            profiles: services::device_profile_statuses(&cfg, now),
            devices: devices.into_values().collect(),
            blockers,
        }
    }

    pub(crate) fn handle_dns_filter_status(&self, _r: &mut Request<'_>) -> Response {
        let filter = match self.require_dns_filter() {
            Ok(filter) => filter,
            Err(response) => return response,
        };
        match filter.status(Instant::now() + STATUS_DEADLINE) {
            Ok(status) => write_json(200, &merge(&status, &self.dns_filter_context())),
            Err(_) => Response::error("DNS filter helper unavailable", 503),
        }
    }

    fn start_dns_filter(&self, r: &Request<'_>, filter: &Arc<dnsfilter::Service>, policy: &dnsfilter::Policy, refresh: bool) -> Response {
        if let Some(blocker) = self.dns_filter_context().blockers.first() {
            return Response::error(blocker, 409);
        }
        match filter.start_operation(policy, refresh, &audit_actor(&r.remote_addr)) {
            Ok(op) => write_json(202, &json!({"updating": true, "operation": op})),
            Err(err) => Response::error(&err.to_string(), 409),
        }
    }

    pub(crate) fn handle_dns_filter_save(&self, r: &mut Request<'_>) -> Response {
        let filter = match self.require_dns_filter() {
            Ok(filter) => filter,
            Err(response) => return response,
        };
        let Ok(policy) = decode_json::<dnsfilter::Policy>(r) else { return Response::error("Invalid DNS filter policy", 400) };
        if let Err(err) = policy.validate() {
            return Response::error(&err.to_string(), 422);
        }
        self.start_dns_filter(r, &filter, &policy, false)
    }

    pub(crate) fn handle_dns_filter_refresh(&self, r: &mut Request<'_>) -> Response {
        let filter = match self.require_dns_filter() {
            Ok(filter) => filter,
            Err(response) => return response,
        };
        let Ok(status) = filter.status(Instant::now() + STATUS_DEADLINE) else { return Response::error("DNS filter unavailable", 503) };
        self.start_dns_filter(r, &filter, &status.applied.policy, true)
    }

    pub(crate) fn handle_dns_filter_check(&self, r: &mut Request<'_>) -> Response {
        let filter = match self.require_dns_filter() {
            Ok(filter) => filter,
            Err(response) => return response,
        };
        #[derive(serde::Deserialize, Default)]
        #[serde(default)]
        struct Req {
            domain: String,
            device_ip: String,
        }
        let mut req = Req::default();
        if r.method == "GET" {
            let query = r.query();
            req.domain = query.get("domain");
            req.device_ip = query.get("device_ip");
        } else {
            match decode_json::<Req>(r) {
                Ok(parsed) => req = parsed,
                Err(_) => return Response::error("Invalid domain check", 400),
            }
        }
        if !req.device_ip.is_empty() && req.device_ip.parse::<std::net::Ipv4Addr>().is_err() {
            return Response::error("device_ip must be an IPv4 address", 422);
        }
        let trimmed = req.domain.trim();
        let domain = trimmed.strip_suffix('.').unwrap_or(trimmed).to_lowercase();
        let (cfg, leases) = (self.engine.get_current_config(), telemetry::current_dhcp_leases());
        let check = if local_dns_match(&cfg, &leases, &domain).is_some() {
            let Ok(status) = filter.status(Instant::now() + STATUS_DEADLINE) else { return Response::error("DNS filter status unavailable", 503) };
            dnsfilter::Check { domain: domain.clone(), action: "Local DNS record".into(), matches: Vec::new(), healthy: status.applied.healthy, policy_revision: status.applied.policy.revision, checked_at: Time::now_utc(), exception: false }
        } else {
            match filter.check(Instant::now() + STATUS_DEADLINE, &domain) {
                Ok(check) => check,
                Err(err) => return Response::error(&err.to_string(), 422),
            }
        };
        write_json(200, &explain_dns_domain(&cfg, &leases, check, &req.device_ip, Time::now()))
    }

    pub(crate) fn handle_dns_filter_profiles(&self, _r: &mut Request<'_>) -> Response {
        write_json(200, &self.dns_filter_context())
    }

    pub(crate) fn handle_dns_filter_operations(&self, _r: &mut Request<'_>) -> Response {
        match self.require_dns_filter() {
            Ok(filter) => write_json(200, &json!({"operations": filter.history(), "retention": dnsfilter::OPERATION_RETENTION})),
            Err(response) => response,
        }
    }

    /// Without the accounting store there is nowhere to keep the setting or
    /// the history.
    fn dns_activity_settings(&self) -> Result<(Option<Arc<crate::accounting::Store>>, Option<Arc<DNSCollector>>, DNSSettings)> {
        let subsystems = self.subsystems();
        let (Some(store), Some(collector)) = (subsystems.accounting.clone(), subsystems.dns_collector.clone()) else {
            return Ok((None, None, DNSSettings { enabled: false, retention_days: DEFAULT_DNS_RETENTION_DAYS }));
        };
        drop(subsystems);
        let settings = store.dns_settings()?;
        Ok((Some(store), Some(collector), settings))
    }

    pub(crate) fn handle_get_dns_activity_settings(&self, _r: &mut Request<'_>) -> Response {
        match self.dns_activity_settings() {
            Ok((store, _, settings)) => write_json(200, &json!({"available": store.is_some(), "enabled": settings.enabled, "retention_days": settings.retention_days})),
            Err(_) => Response::error("DNS activity settings are unavailable", 503),
        }
    }

    pub(crate) fn handle_put_dns_activity_settings(&self, r: &mut Request<'_>) -> Response {
        let Ok((Some(store), Some(collector), previous)) = self.dns_activity_settings() else { return Response::error("DNS activity is unavailable", 503) };
        let Ok(next) = decode_json::<DNSSettings>(r) else { return Response::error("Invalid DNS activity settings", 400) };
        if let Err(err) = store.set_dns_settings(&next) {
            return Response::error(&err.to_string(), 400);
        }
        if next != previous {
            self.append_audit("dns_activity.settings_changed", &audit_actor(&r.remote_addr), details(&[("enabled", &next.enabled.to_string()), ("retention_days", &next.retention_days.to_string())]));
        }
        collector.wake();
        write_json(200, &json!({"available": true, "enabled": next.enabled, "retention_days": next.retention_days}))
    }

    pub(crate) fn handle_dns_activity(&self, r: &mut Request<'_>) -> Response {
        let query = r.query();
        let mut period = query.get("period");
        if period.is_empty() {
            period = "today".into();
        }
        let Some(device) = parse_dns_device(&query.get("device")) else { return Response::error("device must be an IP address", 400) };
        let Some(search) = parse_dns_search(&query.get("q")) else {
            return Response::error("search must be up to 64 letters, digits, dots, hyphens or underscores", 400);
        };
        let now = Time::now_utc();
        let (from, until, _) = match crate::accounting::insights_range(now, &period) {
            Ok(range) => range,
            Err(err) => return Response::error(&err.to_string(), 400),
        };
        let Ok((store, collector, settings)) = self.dns_activity_settings() else { return Response::error("DNS activity is unavailable", 503) };
        let mut result = DNSActivity {
            available: store.is_some() && collector.is_some(),
            enabled: settings.enabled,
            retention_days: settings.retention_days,
            period: period.clone(),
            from,
            until,
            device: device.clone(),
            search: search.clone(),
            ..Default::default()
        };
        // Rows awaiting deletion after a disable are never served.
        if let (true, true, Some(store)) = (result.available, settings.enabled, store) {
            let sites: Vec<String> = DNS_CATEGORIES.keys().cloned().collect();
            let Ok(mut activity) = store.dns_activity(now, &DNSQuery { period: period.clone(), device, search, flagged_sites: sites, site_limit: 0 }) else {
                return Response::error("DNS activity is unavailable", 503);
            };
            activity.enabled = true;
            activity.retention_days = settings.retention_days;
            let labels = device_labels(&self.engine.get_current_config());
            for device in activity.devices.iter_mut() {
                if let Some((hostname, mac)) = labels.get(&device.address) {
                    device.hostname = hostname.clone();
                    device.mac = mac.clone();
                }
            }
            for site in activity.sites.iter_mut() {
                site.category = category(&site.site);
            }
            for flagged in activity.flagged.iter_mut() {
                flagged.category = category(&flagged.site);
                if let Some((hostname, _)) = labels.get(&flagged.address) {
                    flagged.hostname = hostname.clone();
                }
            }
            result = activity;
        }
        result.collection = collector.map(|c| c.status(now)).unwrap_or_else(|| DNSCollectionStatus { state: "unavailable".into(), ..Default::default() });
        if !settings.enabled {
            result.collection.state = "disabled".into();
        }
        write_json(200, &result)
    }

    pub(crate) fn handle_dns_activity_recent(&self, r: &mut Request<'_>) -> Response {
        let query = r.query();
        let Some(device) = parse_dns_device(&query.get("device")) else { return Response::error("device must be an IP address", 400) };
        let raw = query.get("limit");
        let mut limit = 200;
        if !raw.is_empty() {
            match config::go_atoi(&raw).filter(|n| (1..=2000).contains(n)) {
                Some(n) => limit = n as usize,
                None => return Response::error("limit must be between 1 and 2000", 400),
            }
        }
        let Ok((_, collector, settings)) = self.dns_activity_settings() else { return Response::error("DNS activity is unavailable", 503) };
        let mut entries = Vec::new();
        if let Some(collector) = collector.as_ref().filter(|_| settings.enabled) {
            entries = collector.recent(&device, limit);
            let labels: HashMap<String, (String, String)> = device_labels(&self.engine.get_current_config());
            for entry in entries.iter_mut() {
                entry.category = category(&entry.site);
                if let Some((hostname, _)) = labels.get(&entry.address) {
                    entry.hostname = hostname.clone();
                }
            }
        }
        write_json(200, &json!({"available": collector.is_some(), "enabled": settings.enabled, "entries": entries}))
    }

    pub(crate) fn handle_dns_activity_clear(&self, r: &mut Request<'_>) -> Response {
        let Some(collector) = self.subsystems().dns_collector.clone() else { return Response::error("DNS activity is unavailable", 503) };
        if collector.clear().is_err() {
            return Response::error("DNS activity history could not be deleted", 500);
        }
        self.append_audit("dns_activity.history_cleared", &audit_actor(&r.remote_addr), HashMap::new());
        write_json(200, &json!({"cleared": true}))
    }

    fn require_dns_risk(&self) -> std::result::Result<Arc<dnsrisk::Service>, Response> {
        self.subsystems().dns_risk.clone().ok_or_else(|| Response::error("DNS risk monitoring is unavailable", 503))
    }

    pub(crate) fn handle_dns_risk_summary(&self, _r: &mut Request<'_>) -> Response {
        let collection = self.subsystems().dns_collector.clone().map(|c| c.status(Time::now())).unwrap_or_else(|| DNSCollectionStatus { state: "unavailable".into(), ..Default::default() });
        let mut summary = dnsrisk::Summary::default();
        if let Some(service) = self.subsystems().dns_risk.clone() {
            match service.summary(Time::now()) {
                Ok(s) => summary = s,
                Err(_) => return Response::error("DNS risk status unavailable", 503),
            }
        }
        write_json(200, &merge(&summary, &json!({"collection": collection})))
    }

    pub(crate) fn handle_dns_risk_alerts(&self, r: &mut Request<'_>) -> Response {
        let service = match self.require_dns_risk() {
            Ok(service) => service,
            Err(response) => return response,
        };
        let query = r.query();
        let mut view = query.get("view");
        if view.is_empty() {
            view = "new".into();
        }
        let category = query.get("category");
        if (view != "new" && view != "all") || !dnsrisk::validate_category(&category) {
            return Response::error("Invalid alert filter", 400);
        }
        let (mut offset, mut limit) = (0i64, 50i64);
        for key in ["offset", "limit"] {
            let raw = query.get(key);
            if raw.is_empty() {
                continue;
            }
            let Some(n) = config::go_atoi(&raw) else { return Response::error("Invalid pagination", 400) };
            if key == "offset" {
                offset = n;
            } else {
                limit = n;
            }
        }
        if !(0..=10000).contains(&offset) || !(1..=100).contains(&limit) {
            return Response::error("Invalid pagination", 400);
        }
        match service.alerts(&view, &category, offset, limit) {
            Ok(page) => write_json(200, &page),
            Err(_) => Response::error("DNS risk alerts unavailable", 503),
        }
    }

    fn risk_mutation(&self, r: &Request<'_>, change: impl FnOnce(&dnsrisk::Service, i64) -> Result<()>, event: &str, body: Value) -> Response {
        let service = match self.require_dns_risk() {
            Ok(service) => service,
            Err(response) => return response,
        };
        let Some(id) = r.path_value("id").parse::<i64>().ok().filter(|id| *id > 0) else { return Response::error("Invalid alert ID", 400) };
        match change(&service, id) {
            Err(err) if is::<config::ErrNoRows>(&err) => Response::error("Alert or exception not found", 404),
            Err(_) => Response::error("DNS risk change could not be saved", 503),
            Ok(()) => {
                // Domain names never enter the metadata-only audit log.
                self.append_audit(event, &audit_actor(&r.remote_addr), HashMap::new());
                write_json(200, &body)
            }
        }
    }

    pub(crate) fn handle_dns_risk_acknowledge(&self, r: &mut Request<'_>) -> Response {
        self.risk_mutation(r, |s, id| s.acknowledge(id), "dns_risk.alert_reviewed", json!({"acknowledged": true}))
    }

    pub(crate) fn handle_dns_risk_ignore(&self, r: &mut Request<'_>) -> Response {
        self.risk_mutation(r, |s, id| s.ignore(id), "dns_risk.exception_added", json!({"ignored": true}))
    }

    pub(crate) fn handle_dns_risk_remove_exception(&self, r: &mut Request<'_>) -> Response {
        self.risk_mutation(r, |s, id| s.remove_exception(id), "dns_risk.exception_removed", json!({"removed": true}))
    }

    pub(crate) fn handle_dns_risk_exceptions(&self, _r: &mut Request<'_>) -> Response {
        let service = match self.require_dns_risk() {
            Ok(service) => service,
            Err(response) => return response,
        };
        match service.exceptions() {
            Ok(entries) => write_json(200, &json!({"exceptions": entries})),
            Err(_) => Response::error("DNS risk exceptions unavailable", 503),
        }
    }

    pub(crate) fn handle_dns_risk_refresh(&self, r: &mut Request<'_>) -> Response {
        let service = match self.require_dns_risk() {
            Ok(service) => service,
            Err(response) => return response,
        };
        let Ok(summary) = service.summary(Time::now()) else { return Response::error("DNS risk status unavailable", 503) };
        if !summary.enabled {
            return Response::error("Enable DNS activity recording before downloading category lists", 409);
        }
        if !service.request_refresh() {
            return Response::error("A category update was already requested; retry in five minutes", 429).with_header("Retry-After", "300");
        }
        self.append_audit("dns_risk.sources_refresh_requested", &audit_actor(&r.remote_addr), HashMap::new());
        write_json(202, &json!({"queued": true}))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn categories_and_query_parsing() {
        assert_eq!(category("googlevideo.com"), "youtube");
        assert_eq!(category("rbxcdn.com"), "roblox");
        assert_eq!(category("phncdn.com"), "adult");
        assert_eq!(parse_dns_device("::ffff:192.168.1.5").unwrap(), "192.168.1.5");
        assert!(parse_dns_device("fe80::1%eth0").is_none());
        assert_eq!(parse_dns_search(" YouTube.com ").unwrap(), "youtube.com");
        assert!(parse_dns_search("a b").is_none());
    }
}
