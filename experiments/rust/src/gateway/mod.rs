//! Read-only WAN quality monitoring: bounded ICMP probes, PPPoE link state,
//! a seven-day raw history with hourly rollups and long-lived insights.

mod monitor;
mod store;

pub use monitor::{LinkReader, Monitor, Prober};
pub use store::Store;

use regex::Regex;
use serde::{Deserialize, Serialize};
use std::collections::HashSet;
use std::sync::LazyLock;

use crate::errorf;
use crate::goish::errors::Result;
use crate::goish::json::{is_zero_f64, List};
use crate::goish::net;
use crate::goish::time::{Duration, Time};

/// Operator-facing quality classification for the active WAN.
pub type HealthState = String;
pub const STATE_UNKNOWN: &str = "unknown";
pub const STATE_HEALTHY: &str = "healthy";
pub const STATE_DEGRADED: &str = "degraded";
pub const STATE_OFFLINE: &str = "offline";
pub const STATE_FLAPPING: &str = "flapping";

/// One bounded probe result for a configured IPv4 target.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct TargetResult {
    pub target: String,
    pub reachable: bool,
    pub packets_sent: i64,
    pub packets_received: i64,
    pub packet_loss_percent: f64,
    #[serde(skip_serializing_if = "is_zero_f64")]
    pub latency_ms: f64,
    #[serde(skip_serializing_if = "is_zero_f64")]
    pub jitter_ms: f64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
}

/// The local PPPoE data-plane state, read without making changes.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct LinkStatus {
    pub connected: bool,
    pub interface: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub local_ip: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub peer_ip: String,
}

/// The persisted, bounded gateway quality record.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Sample {
    pub timestamp: Time,
    pub state: HealthState,
    pub link: LinkStatus,
    pub targets: List<TargetResult>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub peer_probe: Option<TargetResult>,
    #[serde(skip_serializing_if = "is_zero_f64")]
    pub latency_ms: f64,
    #[serde(skip_serializing_if = "is_zero_f64")]
    pub jitter_ms: f64,
    pub packet_loss_percent: f64,
    #[serde(rename = "pppoe_uptime_seconds")]
    pub pppoe_uptime: i64,
}

/// The compact current status returned to the dashboard.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Summary {
    pub available: bool,
    pub enabled: bool,
    pub state: HealthState,
    pub timestamp: Time,
    pub link: LinkStatus,
    #[serde(skip_serializing_if = "List::is_empty")]
    pub targets: List<TargetResult>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub peer_probe: Option<TargetResult>,
    #[serde(skip_serializing_if = "is_zero_f64")]
    pub latency_ms: f64,
    #[serde(skip_serializing_if = "is_zero_f64")]
    pub jitter_ms: f64,
    pub packet_loss_percent: f64,
    #[serde(rename = "pppoe_uptime_seconds")]
    pub pppoe_uptime: i64,
    #[serde(rename = "reconnects_1h")]
    pub reconnects_1h: i64,
    #[serde(rename = "reconnects_24h")]
    pub reconnects_24h: i64,
}

/// A storage-efficient point for 1h/24h/7d charts.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct HistoryPoint {
    pub timestamp: Time,
    pub state: HealthState,
    #[serde(skip_serializing_if = "is_zero_f64")]
    pub latency_ms: f64,
    #[serde(skip_serializing_if = "is_zero_f64")]
    pub jitter_ms: f64,
    pub packet_loss_percent: f64,
    #[serde(rename = "pppoe_uptime_seconds")]
    pub pppoe_uptime: i64,
}

/// Observed samples in which the WAN was unusable (link down, or link up with
/// every target unreachable). Unknown ends mark missing evidence.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Outage {
    pub started_at: Time,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub ended_at: Option<Time>,
    pub last_observed_at: Time,
    pub duration_seconds: i64,
    pub outcome: String,
    pub partial: bool,
    pub cause: String,
}

pub const OUTAGE_CAUSE_PPPOE_LINK: &str = "pppoe_link";
pub const OUTAGE_CAUSE_INTERNET: &str = "internet";

/// WAN address transitions only; no destinations or traffic metadata.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct PublicIPChange {
    pub timestamp: Time,
    pub old_ip: String,
    pub new_ip: String,
}

/// Sampled PPPoE availability; SampledHours makes partial coverage explicit.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Insights {
    pub window_days: i64,
    pub available: bool,
    pub sampled_hours: i64,
    pub samples: i64,
    pub up_samples: i64,
    pub uptime_percent: f64,
    pub outages: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub first_sample: Option<Time>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub last_sample: Option<Time>,
    pub public_ip_changes: List<PublicIPChange>,
}

/// Bounded, read-only WAN check settings, stored outside the canonical
/// configuration because they cannot change forwarding or privileged state.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Settings {
    pub enabled: bool,
    pub targets: List<String>,
    pub interval_seconds: i64,
}

pub fn default_settings() -> Settings {
    Settings { enabled: true, targets: vec!["1.1.1.1".to_string(), "8.8.8.8".to_string()].into(), interval_seconds: 30 }
}

impl Settings {
    pub fn interval(&self) -> Duration {
        if self.interval_seconds < 15 || self.interval_seconds > 300 { Duration::secs(30) } else { Duration::secs(self.interval_seconds) }
    }

    pub fn validate(&self) -> Result<()> {
        if self.targets.len() != 2 {
            return Err(errorf!("targets must contain exactly two public IPv4 addresses"));
        }
        let mut seen = HashSet::new();
        for (index, target) in self.targets.iter().enumerate() {
            let trimmed = target.trim();
            let ip = net::parse_ip(trimmed).and_then(|ip| ip.to4());
            let Some(ip) = ip.filter(|_| target == trimmed && !trimmed.contains(':')) else {
                return Err(errorf!("target {} must be a normalized dotted-quad IPv4 address", index + 1));
            };
            if !public_monitoring_ipv4(&ip) {
                return Err(errorf!("target {} must be a public unicast IPv4 address", index + 1));
            }
            if !seen.insert(ip.to_string()) {
                return Err(errorf!("monitoring targets must be different"));
            }
        }
        if self.interval_seconds < 15 || self.interval_seconds > 300 {
            return Err(errorf!("interval_seconds must be between 15 and 300"));
        }
        Ok(())
    }
}

fn public_monitoring_ipv4(ip: &net::IP) -> bool {
    const RESERVED: [&str; 14] = [
        "0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
        "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
    ];
    ip.to4().is_some() && !RESERVED.iter().filter_map(|c| net::parse_cidr(c)).any(|(_, n)| n.contains(ip))
}

const DEGRADED_LATENCY_MS: f64 = 150.0;
const DEGRADED_JITTER_MS: f64 = 50.0;
const DEGRADED_LOSS_PCT: f64 = 5.0;
const FLAPPING_PER_HOUR: i64 = 3;

/// Conservative: a single target failure degrades the link; all targets
/// failing or PPPoE down marks it offline; repeated reconnects are flapping.
pub fn classify(link: &LinkStatus, targets: &[TargetResult], reconnects_1h: i64) -> HealthState {
    if !link.connected {
        return STATE_OFFLINE.into();
    }
    if reconnects_1h >= FLAPPING_PER_HOUR {
        return STATE_FLAPPING.into();
    }
    if targets.is_empty() {
        return STATE_UNKNOWN.into();
    }
    let reachable = targets.iter().filter(|r| r.reachable).count();
    let degraded = targets.iter().any(|r| {
        !r.reachable || r.packet_loss_percent >= DEGRADED_LOSS_PCT || r.latency_ms >= DEGRADED_LATENCY_MS || r.jitter_ms >= DEGRADED_JITTER_MS
    });
    if reachable == 0 {
        STATE_OFFLINE.into()
    } else if degraded {
        STATE_DEGRADED.into()
    } else {
        STATE_HEALTHY.into()
    }
}

pub fn aggregate_targets(targets: &[TargetResult]) -> (f64, f64, f64) {
    if targets.is_empty() {
        return (0.0, 0.0, 100.0);
    }
    let loss = targets.iter().map(|r| r.packet_loss_percent).sum::<f64>() / targets.len() as f64;
    let reachable: Vec<&TargetResult> = targets.iter().filter(|r| r.reachable).collect();
    if reachable.is_empty() {
        return (0.0, 0.0, loss);
    }
    let n = reachable.len() as f64;
    (reachable.iter().map(|r| r.latency_ms).sum::<f64>() / n, reachable.iter().map(|r| r.jitter_ms).sum::<f64>() / n, loss)
}

pub(crate) fn state_severity(state: &str) -> i32 {
    match state {
        STATE_OFFLINE => 4,
        STATE_FLAPPING => 3,
        STATE_DEGRADED => 2,
        STATE_HEALTHY => 1,
        _ => 0,
    }
}

static PING_TIME: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"time[=<]([0-9]+(?:\.[0-9]+)?)\s*ms").unwrap());
static PACKETS: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"([0-9]+)\s+packets transmitted,\s+([0-9]+)\s+(?:packets )?received").unwrap());
static LOSS: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"([0-9]+(?:\.[0-9]+)?)%\s+packet loss").unwrap());

pub fn parse_ping_output(target: &str, output: &str) -> Result<TargetResult> {
    let mut result = TargetResult { target: target.into(), packet_loss_percent: 100.0, ..Default::default() };
    let packets = PACKETS.captures(output).ok_or_else(|| errorf!("unrecognized ping packet summary"))?;
    result.packets_sent = crate::config::go_atoi(&packets[1]).unwrap_or(0);
    result.packets_received = crate::config::go_atoi(&packets[2]).unwrap_or(0);
    if result.packets_sent <= 0 {
        return Err(errorf!("invalid transmitted packet count"));
    }
    result.packet_loss_percent = match LOSS.captures(output) {
        Some(loss) => loss[1].parse().unwrap_or(0.0),
        None => (result.packets_sent - result.packets_received) as f64 / result.packets_sent as f64 * 100.0,
    };
    result.reachable = result.packets_received > 0;
    let values: Vec<f64> = PING_TIME.captures_iter(output).filter_map(|c| c[1].parse().ok()).collect();
    if !values.is_empty() {
        result.latency_ms = values.iter().sum::<f64>() / values.len() as f64;
        if values.len() > 1 {
            result.jitter_ms = values.windows(2).map(|w| (w[1] - w[0]).abs()).sum::<f64>() / (values.len() - 1) as f64;
        }
    }
    Ok(result)
}

/// iproute2's one-line PPP address: `inet 203.0.113.10 peer 198.51.100.1/32`.
pub fn parse_peer_output(output: &str) -> (String, String) {
    let fields: Vec<&str> = output.trim().split_whitespace().collect();
    let (mut local, mut peer) = (String::new(), String::new());
    for (i, field) in fields.iter().enumerate() {
        let next = fields.get(i + 1).map(|v| v.split('/').next().unwrap_or("").to_string());
        match *field {
            "inet" => local = next.unwrap_or(local),
            "peer" => peer = next.unwrap_or(peer),
            _ => {}
        }
    }
    (local, peer)
}

pub(crate) fn find_fixed_binary(name: &str) -> String {
    for dir in ["/bin/", "/usr/bin/", "/sbin/", "/usr/sbin/"] {
        let path = format!("{}{}", dir, name);
        if std::fs::metadata(&path).is_ok() {
            return path;
        }
    }
    String::new()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ping_output_parsing() {
        let out = "PING 1.1.1.1 (1.1.1.1): 56 data bytes\n64 bytes from 1.1.1.1: seq=0 ttl=57 time=10.0 ms\n64 bytes from 1.1.1.1: seq=1 ttl=57 time=14.0 ms\n64 bytes from 1.1.1.1: seq=2 ttl=57 time=12.0 ms\n\n--- 1.1.1.1 ping statistics ---\n4 packets transmitted, 3 packets received, 25% packet loss\n";
        let r = parse_ping_output("1.1.1.1", out).unwrap();
        assert_eq!((r.packets_sent, r.packets_received), (4, 3));
        assert_eq!(r.packet_loss_percent, 25.0);
        assert_eq!(r.latency_ms, 12.0);
        assert_eq!(r.jitter_ms, 3.0);
        assert_eq!(parse_peer_output("7: ppp0 inet 203.0.113.10 peer 198.51.100.1/32 scope global"), ("203.0.113.10".into(), "198.51.100.1".into()));
    }

    #[test]
    fn settings_and_classification() {
        default_settings().validate().unwrap();
        let mut bad = default_settings();
        bad.targets = vec!["10.0.0.1".to_string(), "8.8.8.8".to_string()].into();
        assert_eq!(bad.validate().unwrap_err().to_string(), "target 1 must be a public unicast IPv4 address");
        let link = LinkStatus { connected: true, ..Default::default() };
        let ok = TargetResult { reachable: true, latency_ms: 10.0, ..Default::default() };
        assert_eq!(classify(&link, &[ok.clone(), ok.clone()], 0), STATE_HEALTHY);
        assert_eq!(classify(&link, &[ok.clone(), TargetResult::default()], 0), STATE_DEGRADED);
        assert_eq!(classify(&link, &[ok], 3), STATE_FLAPPING);
        assert_eq!(classify(&LinkStatus::default(), &[], 0), STATE_OFFLINE);
    }
}
