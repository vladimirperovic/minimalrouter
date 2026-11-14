//! Differential tests of every service generator against the Go output.

use std::collections::BTreeMap;

use minimalrouter::config::SystemConfig;
use minimalrouter::goish::{errors::Result, json, net, time::Time};
use minimalrouter::services::{self, FirewallFlow};
use serde::Deserialize;

#[path = "parity_config.rs"]
#[allow(dead_code)]
mod parity_config;

#[derive(Deserialize, Default)]
struct TextResult {
    #[serde(default)]
    text: String,
    #[serde(default)]
    error: String,
}

#[derive(Deserialize, Debug, PartialEq, Clone)]
struct ExplainResult {
    verdict: String,
    reason: String,
    rule_id: String,
    source_zone: String,
    destination_zone: String,
    #[serde(default)]
    error: String,
}

#[derive(Deserialize, Debug, PartialEq)]
struct ProfileResult {
    id: String,
    state: String,
    allowed_now: Option<bool>,
    next_change_at: String,
    next_state: String,
    missing_reservations: Option<Vec<String>>,
}

#[derive(Deserialize)]
struct Case {
    name: String,
    input: String,
    nftables: TextResult,
    dnsmasq: TextResult,
    pppoe: Vec<String>,
    hostapd: TextResult,
    qos: TextResult,
    squid: TextResult,
    ddns: TextResult,
    tunnel: TextResult,
    doh: TextResult,
    wireguard: TextResult,
    wireguard_runtime: TextResult,
    wg_client_runtime: TextResult,
    adblock: TextResult,
    counters: Option<Vec<String>>,
    warnings: Vec<String>,
    firewall_changes: Vec<String>,
    network_changes: Vec<String>,
    explain: BTreeMap<String, ExplainResult>,
    profiles: BTreeMap<String, Vec<ProfileResult>>,
}

const ADBLOCK_HOSTS: &str = "0.0.0.0 ads.example.com\n127.0.0.1 Tracker.Example.NET.\n# comment\n0.0.0.0 localhost\n::1 bad_domain\n0.0.0.0 ads.example.com\n";
const PROFILE_TIMES: [&str; 4] = ["2026-10-26T12:34:00+01:00", "2026-10-31T23:59:30+01:00", "2026-11-01T06:59:00Z", "2026-11-02T20:00:00+01:00"];

fn flow_probes() -> Vec<FirewallFlow> {
    let f = |s: &str, d: &str, p: &str, port: i64| FirewallFlow { source_ip: s.into(), destination_ip: d.into(), protocol: p.into(), port };
    vec![
        f("192.168.1.30", "8.8.8.8", "tcp", 443),
        f("192.168.1.30", "8.8.8.8", "udp", 53),
        f("192.168.1.50", "8.8.8.8", "tcp", 23),
        f("192.168.1.50", "192.168.1.1", "tcp", 22),
        f("192.168.1.50", "192.168.1.1", "tcp", 8443),
        f("192.168.1.50", "192.168.1.1", "udp", 67),
        f("192.168.1.50", "192.168.1.1", "tcp", 3128),
        f("10.8.0.2", "10.8.0.1", "tcp", 27015),
        f("10.8.0.2", "192.168.1.20", "udp", 27015),
        f("10.8.0.2", "192.168.60.10", "tcp", 8096),
        f("192.168.1.5", "192.168.60.10", "tcp", 8096),
        f("192.168.60.10", "192.168.60.1", "icmp", 0),
        f("192.168.60.10", "8.8.8.8", "tcp", 80),
        f("10.20.1.1", "192.168.1.5", "tcp", 22),
        f("192.168.1.5", "10.20.1.1", "tcp", 22),
        f("203.0.113.5", "192.168.1.5", "tcp", 80),
        f("192.168.1.5", "192.168.1.6", "icmp", 0),
        f("192.168.1.5", "8.8.8.8", "icmp", 3),
        f("1.1.1.1", "fe80::1", "tcp", 1),
        f("192.168.1.5", "8.8.8.8", "gre", 0),
    ]
}

// Deliberate deviations from Go v0.3.3. Everything else stays under strict
// byte-for-byte parity, and the fixtures themselves are never edited. The
// expected Go value is transformed here, never the Rust output. Listed in
// `fixtures/README.md`.
//
// 1. SSH (tcp/22) from the LAN and WireGuard interfaces is no longer accepted
//    from the whole interface. Go emitted `iifname "<if>" tcp dport 22 accept`;
//    Rust emits that accept restricted to the IPv4 members of
//    `trusted_networks`, or a comment when there are none. Mirrors
//    `write_ssh_accept` in `mr-services/src/services/nftables.rs`.
// 2. The firewall explanation of router-destined tcp/22 from a source outside
//    those networks is a deny instead of the management allow (probe 3 only).

const GO_MANAGEMENT_ALLOW: &str = "Management port is allowed by packet policy; trusted networks and authentication still apply.";
const SSH_UNTRUSTED_DENY: &str = "SSH is accepted only from trusted management networks.";
const SSH_PROBE: usize = 3;

/// The interface of a Go `    iifname "<if>" tcp dport 22 accept` line.
fn go_ssh_accept_interface(line: &str) -> Option<&str> {
    let iface = line.strip_prefix("    iifname \"")?.strip_suffix("\" tcp dport 22 accept")?;
    (!iface.contains('"')).then_some(iface)
}

/// Go's expected nftables text with each SSH accept line replaced by what the
/// generator now writes. The SSH line is the one directly followed by that
/// interface's DNS accept: with `https_port` 22 the HTTPS accept is textually
/// identical and precedes it, so it is left alone. Only text Go emitted is
/// transformed, so an interface shared by the WireGuard and LAN blocks, a
/// bridge LAN or a missing LAN rule need no special case.
fn with_ssh_deviation(cfg: &SystemConfig, go_text: &str) -> String {
    let sources = services::trusted_ipv4_sources(cfg).join(", ");
    let mut out = String::with_capacity(go_text.len());
    let lines: Vec<&str> = go_text.split_inclusive('\n').collect();
    for (i, line) in lines.iter().copied().enumerate() {
        let Some(iface) = go_ssh_accept_interface(line.trim_end_matches('\n')) else {
            out.push_str(line);
            continue;
        };
        if !lines.get(i + 1).is_some_and(|next| next.starts_with(&format!("    iifname \"{iface}\" udp dport "))) {
            out.push_str(line);
            continue;
        }
        if sources.is_empty() {
            out.push_str(&format!("    # SSH is closed on {iface}: trusted_networks lists no IPv4 network\n"));
        } else {
            out.push_str(&format!("    iifname \"{iface}\" ip saddr {{ {sources} }} tcp dport 22 accept\n"));
        }
    }
    out
}

/// Go's expected explanation, with the SSH deviation applied to probe 3.
fn with_explain_deviation(cfg: &SystemConfig, index: usize, want: ExplainResult) -> ExplainResult {
    if index != SSH_PROBE || want.verdict != "allow" || want.reason != GO_MANAGEMENT_ALLOW {
        return want;
    }
    let source = net::parse_ip(&flow_probes()[index].source_ip);
    let trusted = services::trusted_ipv4_sources(cfg).iter().filter_map(|n| net::parse_cidr(n)).any(|(_, n)| n.contains_opt(source.as_ref()));
    if trusted {
        return want;
    }
    ExplainResult { verdict: "deny".into(), reason: SSH_UNTRUSTED_DENY.into(), ..want }
}

fn check(failures: &mut Vec<String>, case: &str, what: &str, got: Result<String>, want: &TextResult) {
    match got {
        Ok(text) if want.error.is_empty() && text == want.text => {}
        Err(err) if !want.error.is_empty() && err.to_string() == want.error => {}
        Ok(text) => failures.push(format!("{case}: {what} differs\n--- rust\n{}\n--- go\n{}{}", text, want.text, want.error)),
        Err(err) => {
            failures.push(format!("{case}: {what} error differs\n  rust: {err}\n  go:   {}", if want.error.is_empty() { "(no error)" } else { &want.error }))
        }
    }
}

#[test]
fn services_match_go() {
    let mut bases: BTreeMap<String, SystemConfig> = BTreeMap::new();
    let mut failures = Vec::new();
    let mut total = 0;
    let mut go_panics = 0;
    for line in parity_config::case_lines("services_cases.jsonl") {
        let case: Case = serde_json::from_str(&line).unwrap();
        total += 1;
        let cfg: SystemConfig = json::unmarshal(case.input.as_bytes()).expect("Go decoded this input");
        if !case.name.contains('-') {
            bases.insert(case.name.clone(), cfg.clone());
        }
        let base = bases[if case.name.starts_with("default") { "default" } else { "full" }].clone();
        let n = &case.name;
        let want_nftables = TextResult { text: with_ssh_deviation(&cfg, &case.nftables.text), error: case.nftables.error.clone() };
        check(&mut failures, n, "nftables", services::generate_nftables(&cfg), &want_nftables);
        check(&mut failures, n, "dnsmasq", services::generate_dnsmasq(&cfg), &case.dnsmasq);
        let bundle = services::generate_pppoe(&cfg).unwrap();
        if vec![bundle.peer_config, bundle.chap_secrets, bundle.pap_secrets] != case.pppoe {
            failures.push(format!("{n}: pppoe differs"));
        }
        check(&mut failures, n, "hostapd", services::generate_hostapd(&cfg), &case.hostapd);
        check(&mut failures, n, "qos", services::generate_qos(&cfg), &case.qos);
        check(&mut failures, n, "squid", services::generate_squid_config(&cfg), &case.squid);
        check(&mut failures, n, "ddns", services::generate_dynamic_dns(&cfg), &case.ddns);
        check(&mut failures, n, "tunnel", services::generate_cloudflare_tunnel(&cfg), &case.tunnel);
        check(&mut failures, n, "doh", services::generate_doh_proxy(&cfg), &case.doh);
        check(&mut failures, n, "wireguard", services::generate_wireguard(&cfg.wireguard), &case.wireguard);
        check(&mut failures, n, "wireguard_runtime", services::generate_wireguard_runtime(&cfg.wireguard), &case.wireguard_runtime);
        check(&mut failures, n, "wg_client_runtime", services::generate_wireguard_client_runtime(&cfg.wg_client), &case.wg_client_runtime);
        check(&mut failures, n, "adblock", services::generate_ad_block_conf(&cfg, ADBLOCK_HOSTS.as_bytes()), &case.adblock);
        let counters: Vec<String> = cfg.firewall.custom_rules.iter().map(services::firewall_rule_counter).collect();
        if counters != case.counters.clone().unwrap_or_default() {
            failures.push(format!("{n}: rule counters differ"));
        }
        let warnings = services::firewall_warnings(&cfg);
        if warnings != case.warnings {
            failures.push(format!("{n}: firewall warnings differ: {:?} vs {:?}", warnings, case.warnings));
        }
        if services::describe_firewall_changes(&base.firewall, &cfg.firewall) != case.firewall_changes {
            failures.push(format!("{n}: firewall changes differ"));
        }
        let network = services::describe_network_changes(&base, &cfg);
        if network != case.network_changes {
            failures.push(format!("{n}: network changes differ: {:?} vs {:?}", network, case.network_changes));
        }
        for (i, flow) in flow_probes().iter().enumerate() {
            let (explanation, error) = match services::explain_firewall_flow(&cfg, flow) {
                Ok(e) => (e, String::new()),
                Err((e, err)) => (e, err.to_string()),
            };
            let got = ExplainResult {
                verdict: explanation.verdict,
                reason: explanation.reason,
                rule_id: explanation.rule_id,
                source_zone: explanation.source_zone,
                destination_zone: explanation.destination_zone,
                error,
            };
            let want = with_explain_deviation(&cfg, i, case.explain[&i.to_string()].clone());
            if got != want {
                failures.push(format!("{n}: explain[{i}] differs\n  rust: {:?}\n  go:   {:?}", got, want));
            }
        }
        for at in PROFILE_TIMES {
            let want = case.profiles.get(at).map(|v| v.as_slice()).unwrap_or(&[]);
            if want.first().is_some_and(|p| p.id == "<go panic>") {
                go_panics += 1;
                continue;
            }
            let now = Time::parse_rfc3339(at).unwrap();
            let got: Vec<ProfileResult> = services::device_profile_statuses(&cfg, now)
                .into_iter()
                .map(|s| ProfileResult {
                    id: s.id,
                    state: s.state,
                    allowed_now: s.allowed_now,
                    next_change_at: s.next_change_at.map(|t| t.rfc3339()).unwrap_or_default(),
                    next_state: s.next_state,
                    missing_reservations: Some(s.missing_reservations),
                })
                .collect();
            if got != want {
                failures.push(format!("{n}: profiles at {at} differ\n  rust: {:?}\n  go:   {:?}", got, want));
            }
        }
    }
    if !failures.is_empty() {
        panic!("{} differences across {} services cases:\n{}", failures.len(), total, failures.iter().take(8).cloned().collect::<Vec<_>>().join("\n"));
    }
    eprintln!("{} services cases identical to Go ({} profile evaluations skipped where Go panics)", total, go_panics);
}
