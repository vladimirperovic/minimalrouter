use regex::Regex;
use serde::Serialize;
use std::collections::BTreeMap;
use std::sync::LazyLock;

use crate::buildinfo;
use crate::config::SystemConfig;
use crate::goish::json;
use crate::goish::time::Time;

static PASSWORD: LazyLock<Regex> = LazyLock::new(|| Regex::new(r#"(?i)("?password"?\s*[:=]\s*)"[^"]+""#).unwrap());
static TOKEN: LazyLock<Regex> = LazyLock::new(|| Regex::new(r#"(?i)("?token"?\s*[:=]\s*)"[^"]+""#).unwrap());
static KEY: LazyLock<Regex> = LazyLock::new(|| Regex::new(r#"(?i)("?private_key"?\s*[:=]\s*)"[^"]+""#).unwrap());
static SECRET: LazyLock<Regex> = LazyLock::new(|| Regex::new(r#"(?i)("?secret"?\s*[:=]\s*)"[^"]+""#).unwrap());

/// Redacts sensitive credentials from logs and export payloads.
pub fn redact_secrets(input: &str) -> String {
    let mut out = input.to_string();
    for pattern in [&*PASSWORD, &*TOKEN, &*KEY, &*SECRET] {
        out = pattern.replace_all(&out, "${1}\"[REDACTED]\"").into_owned();
    }
    out
}

fn redact(value: &mut String) {
    if !value.is_empty() {
        *value = "[REDACTED]".into();
    }
}

/// A detached copy with secret and identifying fields scrubbed.
pub fn redacted_system_config(cfg: &SystemConfig) -> SystemConfig {
    let mut clean = cfg.clone();
    redact(&mut clean.wan.password);
    // The PPPoE login names the subscriber's ISP account.
    redact(&mut clean.wan.username);
    redact(&mut clean.squid_proxy.password);
    redact(&mut clean.wifi.passphrase);
    clean.wifi.ssid.clear();
    redact(&mut clean.wireguard.private_key);
    redact(&mut clean.wg_client.private_key);
    redact(&mut clean.wg_client.preshared_key);
    for peer in clean.wireguard.peers.iter_mut() {
        peer.name.clear();
        // Public keys and endpoints identify each remote device.
        peer.public_key.clear();
        peer.endpoint.clear();
        redact(&mut peer.preshared_key);
    }
    clean.wg_client.public_key.clear();
    clean.wg_client.endpoint.clear();
    for profile in clean.adguard.device_profiles.iter_mut() {
        profile.name.clear();
    }
    for lease in clean.dhcp.static_leases.iter_mut() {
        lease.hostname.clear();
        lease.mac.clear();
    }
    for record in clean.dns.records.iter_mut() {
        record.name.clear();
    }
    for lan in clean.firewall.extra_lans.iter_mut() {
        lan.name.clear();
    }
    redact(&mut clean.cloudflare.api_token);
    redact(&mut clean.cloudflare.tunnel_token);
    clean.cloudflare.domain.clear();
    clean.cloudflare.ddns_user.clear();
    clean
}

/// A sanitized export of system state. It still contains private topology
/// (CIDRs, static IPs), so it must be treated as PRIVATE.
#[derive(Serialize)]
pub struct DiagnosticBundle {
    pub timestamp: Time,
    pub app_version: String,
    pub git_commit: String,
    pub build_date: String,
    pub privacy: String,
    #[serde(rename = "config")]
    pub redacted_config: SystemConfig,
    pub service_health: BTreeMap<String, String>,
}

pub fn build_diagnostic_bundle(cfg: &SystemConfig) -> crate::goish::errors::Result<Vec<u8>> {
    let service_health = [
        ("pppd", "not-collected"),
        ("nftables", "not-collected"),
        ("dnsmasq", "not-collected"),
        ("wireguard", "not-implemented"),
        ("cloudflared", "not-implemented"),
    ]
    .iter()
    .map(|(k, v)| (k.to_string(), v.to_string()))
    .collect();
    let bundle = DiagnosticBundle {
        timestamp: Time::now(),
        app_version: buildinfo::display_version(),
        git_commit: buildinfo::COMMIT.into(),
        build_date: buildinfo::BUILD_DATE.into(),
        privacy: "PRIVATE — contains network topology; do not share publicly".into(),
        redacted_config: redacted_system_config(cfg),
        service_health,
    };
    let data = json::marshal_indent(&bundle, "  ")?;
    Ok(redact_secrets(&String::from_utf8_lossy(&data)).into_bytes())
}
