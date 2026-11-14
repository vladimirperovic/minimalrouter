//! Network-exposure invariants for the generated appliance policy.
//!
//! The documented guarantees (`SECURITY.md` "Secure defaults"/"Trust
//! boundaries", `ARCHITECTURE.md` "Security boundaries") are turned into
//! machine-checked properties of the text the generators render. A matrix of
//! VALID configurations is rendered with `generate_nftables`, the
//! `table inet minimalrouter` is parsed chain by chain, and every invariant is
//! asserted for every sample with a message naming the sample, chain and
//! offending rule line.
//!
//! Scope and limits, stated plainly:
//! * This checks the GENERATED TEXT. It never loads rules into a kernel, so it
//!   proves what the generator asks nftables to do, not what a running
//!   appliance does (no packets are sent, nothing is routed).
//! * The invariants match on interface, port and source-set tokens, never on
//!   whole-line equality (`invariants_tolerate_equivalent_ssh_rule_shapes`
//!   proves a re-shaped SSH rule still passes).
//! * `checkers_detect_injected_violations` mutates real generated output and
//!   requires each invariant to fire, so an invariant cannot pass vacuously.

use std::collections::BTreeSet;
use std::sync::LazyLock;

use minimalrouter::config::{
    CloudflareConfig, DeviceProfile, ExtraLANConfig, FirewallRule, PortForwardRule, RestrictedIPItem, SystemConfig, WGClientConfig, WireGuardPeer,
    default_config, validate_live_candidate,
};
use minimalrouter::goish::json::List;
use minimalrouter::services::{generate_dnsmasq, generate_hostapd, generate_nftables, generate_squid_config};
use regex::Regex;

// ───────────────────────── sample matrix ─────────────────────────

fn list<T>(items: Vec<T>) -> List<T> {
    List::from(items)
}

/// Standard base64 of 32 identical bytes (`base64` is not a dependency here).
fn key(byte: u8) -> String {
    match byte {
        1 => format!("{}AQE=", "AQEB".repeat(10)),
        2 => format!("{}AgI=", "AgIC".repeat(10)),
        3 => format!("{}AwM=", "AwMD".repeat(10)),
        _ => panic!("no fixture key for {byte}"),
    }
}

struct Sample {
    name: &'static str,
    cfg: SystemConfig,
}

fn wan_on(c: &mut SystemConfig) {
    c.wan.enabled = true;
    c.wan.username = "isp-user".into();
    c.wan.password = "correct horse battery".into();
}

fn peer(id: &str, name: &str, ip: &str, endpoint: &str, enabled: bool) -> WireGuardPeer {
    WireGuardPeer {
        id: id.into(),
        name: name.into(),
        public_key: key(2),
        allowed_ips: list(vec![format!("{ip}/32")]),
        endpoint: endpoint.into(),
        enabled,
        ..Default::default()
    }
}

fn wg_on(c: &mut SystemConfig) {
    wan_on(c);
    c.wireguard.enabled = true;
    c.wireguard.private_key = key(1);
    c.wireguard.peers = list(vec![
        peer("peer-1", "Phone", "10.8.0.2", "", true),
        // An endpoint inside a range the WAN anti-spoof policy drops: this is
        // the documented known-peer-endpoint exception.
        peer("peer-2", "Office router", "10.8.0.3", "203.0.113.9:51820", true),
        peer("peer-3", "Laptop", "10.8.0.4", "93.184.216.34:51820", true),
        peer("peer-4", "Retired", "10.8.0.5", "198.51.100.77:51820", false),
    ]);
    c.trusted_networks = list(vec!["192.168.1.0/24".into(), "10.8.0.0/24".into()]);
}

fn wifi_on(c: &mut SystemConfig) {
    c.wifi.enabled = true;
    c.wifi.interface = "wlan0".into();
    c.wifi.ssid = "MinimalRouter-Test".into();
    c.wifi.passphrase = "a very long wifi pass".into();
    c.wifi.band = "5ghz".into();
    c.wifi.channel = 36;
}

fn squid_on(c: &mut SystemConfig) {
    c.squid_proxy.enabled = true;
    c.squid_proxy.port = 3128;
    c.squid_proxy.username = "proxyadmin".into();
    c.squid_proxy.password = "proxy-password-1".into();
    c.squid_proxy.restricted_ips = list(vec![
        RestrictedIPItem { hostname: "kid-pc".into(), ip_address: "192.168.1.50".into(), enabled: true },
        RestrictedIPItem { hostname: "old-pc".into(), ip_address: "192.168.1.51".into(), enabled: false },
    ]);
}

fn extra_lan(allow_from: &[&str], protocol: &str) -> ExtraLANConfig {
    ExtraLANConfig {
        id: "iot".into(),
        name: "IoT cameras".into(),
        interface: "eth2".into(),
        cidr: "192.168.60.0/24".into(),
        router_address: "192.168.60.1/24".into(),
        dst_ip: "192.168.60.10".into(),
        dst_port: 8096,
        protocol: protocol.into(),
        allow_from: list(allow_from.iter().map(|s| s.to_string()).collect()),
        enabled: true,
    }
}

fn port_forwards() -> List<PortForwardRule> {
    let rule = |id: &str, name: &str, protocol: &str, port: i64, ip: &str, enabled: bool| PortForwardRule {
        id: id.into(),
        name: name.into(),
        protocol: protocol.into(),
        external_port: port,
        internal_ip: ip.into(),
        internal_port: port,
        enabled,
    };
    list(vec![
        rule("pf-tcp", "Media server", "tcp", 8096, "192.168.1.20", true),
        rule("pf-udp", "Game udp", "udp", 27015, "192.168.1.21", true),
        rule("pf-both", "Game both", "both", 25565, "192.168.1.22", true),
        rule("pf-off", "Disabled forward", "tcp", 9999, "192.168.1.23", false),
    ])
}

/// An enabled custom rule; the optional fields are set with struct update.
fn fw(id: &str, name: &str, action: &str, direction: &str, protocol: &str, dst_port: i64) -> FirewallRule {
    FirewallRule {
        id: id.into(),
        name: name.into(),
        action: action.into(),
        direction: direction.into(),
        protocol: protocol.into(),
        dst_port,
        enabled: true,
        ..Default::default()
    }
}

fn wg_client_on(c: &mut SystemConfig) {
    c.wg_client = WGClientConfig {
        enabled: true,
        interface: "wg1".into(),
        private_key: key(1),
        address: "10.20.0.2/32".into(),
        public_key: key(2),
        endpoint: "198.51.100.7:51821".into(),
        allowed_ips: list(vec!["10.99.0.0/16".into()]),
        persistent_keepalive: 25,
        ..Default::default()
    };
}

fn ddns_on(c: &mut SystemConfig) {
    c.cloudflare = CloudflareConfig {
        ddns_enabled: true,
        ddns_provider: "noip".into(),
        ddns_user: "ddns-user".into(),
        api_token: "ddnskey-123456".into(),
        domain: "home.example.com".into(),
        ..Default::default()
    };
}

fn dns_filter_on(c: &mut SystemConfig) {
    c.adguard.enabled = true;
    c.adguard.device_profiles = list(vec![DeviceProfile {
        id: "kid-tablet".into(),
        name: "Kid tablet".into(),
        ip_addresses: list(vec!["192.168.1.50".into()]),
        services: list(vec!["youtube".into()]),
        enabled: true,
        ..Default::default()
    }]);
}

fn custom_rules_lan_mgmt() -> List<FirewallRule> {
    list(vec![
        fw("r-in-tcp", "Alt SSH", "allow", "input", "tcp", 2222),
        fw("r-in-udp", "mDNS", "allow", "input", "udp", 5353),
        FirewallRule { src_ip: "192.168.1.0/25".into(), ..fw("r-fw-allow", "Allow HTTPS", "allow", "forward", "tcp", 443) },
        FirewallRule { scope: "wan".into(), ..fw("r-fw-allow-wan", "Allow NTP", "allow", "forward", "udp", 123) },
        fw("r-fw-deny", "Block SMTP", "deny", "forward", "tcp", 25),
        FirewallRule { src_ip: "192.168.1.50".into(), scope: "wan".into(), ..fw("r-fw-deny-wan", "Block IRC", "deny", "forward", "tcp", 6667) },
        FirewallRule { enabled: false, ..fw("r-off", "Disabled input", "allow", "input", "tcp", 8443) },
    ])
}

fn samples() -> Vec<Sample> {
    let mut out = Vec::new();
    let mut add = |name: &'static str, build: &dyn Fn(&mut SystemConfig)| {
        let mut cfg = default_config();
        build(&mut cfg);
        out.push(Sample { name, cfg });
    };

    add("default", &|_| {});
    add("renamed_interfaces", &|c| {
        wan_on(c);
        c.wan.interface = "enp1s0".into();
        c.lan.interface = "enp2s0".into();
    });
    add("wan_pppoe", &|c| wan_on(c));
    add("wireguard_server", &|c| wg_on(c));
    add("wireguard_only_mgmt", &|c| {
        wg_on(c);
        c.system.management_access = "wireguard_only".into();
    });
    add("wifi", &|c| wifi_on(c));
    add("squid_restricted", &|c| squid_on(c));
    add("extra_lan_tcp", &|c| c.firewall.extra_lans = list(vec![extra_lan(&["192.168.1.0/24"], "tcp")]));
    add("extra_lan_udp_with_wan_and_wg", &|c| {
        wg_on(c);
        c.firewall.extra_lans = list(vec![extra_lan(&["192.168.1.50/32", "10.8.0.0/24"], "udp")]);
    });
    add("tunnel_port_forwards", &|c| {
        wg_on(c);
        c.firewall.port_forwards = port_forwards();
    });
    add("custom_rules", &|c| {
        wan_on(c);
        c.firewall.custom_rules = custom_rules_lan_mgmt();
    });
    add("wg_client_outbound", &|c| {
        wan_on(c);
        wg_client_on(c);
    });
    add("wg_client_and_wg_server", &|c| {
        wg_on(c);
        wg_client_on(c);
    });
    add("accounting", &|c| c.accounting.enabled = true);
    // trusted_networks narrower than the LAN, and without any IPv4 network.
    add("ssh_trusted_single_host", &|c| c.trusted_networks = list(vec!["192.168.1.10/32".into()]));
    add("ssh_trusted_ipv6_only", &|c| c.trusted_networks = list(vec!["fd00::/8".into()]));
    add("ssh_trusted_overlap_and_host_bits", &|c| {
        c.trusted_networks = list(vec!["192.168.1.77/25".into(), "192.168.1.0/24".into(), "fd00::/8".into()]);
    });
    add("ssh_wg_tunnel_not_trusted", &|c| {
        wg_on(c);
        c.trusted_networks = list(vec!["192.168.1.0/24".into()]);
    });
    add("ssh_wg_only_tunnel_trusted_only", &|c| {
        wg_on(c);
        c.system.management_access = "wireguard_only".into();
        c.trusted_networks = list(vec!["10.8.0.0/24".into()]);
    });
    add("ssh_wg_two_hosts_wifi", &|c| {
        wg_on(c);
        wifi_on(c);
        c.trusted_networks = list(vec!["192.168.1.10/32".into(), "10.8.0.2/32".into()]);
    });
    add("everything_on_lan_mgmt", &|c| {
        wg_on(c);
        wifi_on(c);
        squid_on(c);
        wg_client_on(c);
        ddns_on(c);
        dns_filter_on(c);
        c.accounting.enabled = true;
        c.firewall.port_forwards = port_forwards();
        c.firewall.custom_rules = custom_rules_lan_mgmt();
        c.firewall.extra_lans = list(vec![extra_lan(&["192.168.1.50/32", "10.8.0.0/24"], "tcp")]);
    });
    add("everything_on_wg_only", &|c| {
        wg_on(c);
        wifi_on(c);
        squid_on(c);
        wg_client_on(c);
        ddns_on(c);
        dns_filter_on(c);
        c.accounting.enabled = true;
        c.system.management_access = "wireguard_only".into();
        c.firewall.port_forwards = port_forwards();
        // No LAN input allow in wireguard_only mode (validation forbids it);
        // a DISABLED one must still render nothing.
        c.firewall.custom_rules = list(vec![
            fw("r-fw-allow", "Allow HTTPS", "allow", "forward", "tcp", 443),
            fw("r-fw-deny", "Block SMTP", "deny", "forward", "tcp", 25),
            FirewallRule { enabled: false, ..fw("r-off-ssh", "Disabled SSH", "allow", "input", "tcp", 22) },
        ]);
        c.firewall.extra_lans = list(vec![extra_lan(&["10.8.0.0/24"], "tcp")]);
    });
    out
}

/// VALID configurations that once broke the SSH invariant (inv9): a custom
/// `input allow` rule is rendered as a bare `iifname "<lan>" ... accept` with
/// no `ip saddr` set, which would reach tcp/22 from the whole LAN whatever
/// `trusted_networks` says. The generator now writes an unconditional tcp/22
/// drop for the interface ahead of custom rules. Kept out of the main matrix
/// so each result stays attributable; asserted by
/// `inv9_custom_input_rules_cannot_bypass_the_ssh_trust_boundary`.
fn bypass_samples() -> Vec<Sample> {
    let narrow = |c: &mut SystemConfig| c.trusted_networks = list(vec!["192.168.1.10/32".into()]);
    let mut out = Vec::new();
    let mut add = |name: &'static str, rules: Vec<FirewallRule>| {
        let mut cfg = default_config();
        narrow(&mut cfg);
        cfg.firewall.custom_rules = list(rules);
        out.push(Sample { name, cfg });
    };
    add("custom_input_allow_tcp22", vec![fw("r-ssh", "Open SSH", "allow", "input", "tcp", 22)]);
    add("custom_input_allow_any_protocol", vec![fw("r-any", "Open everything", "allow", "input", "any", 0)]);
    out
}

fn render(s: &Sample) -> String {
    generate_nftables(&s.cfg).unwrap_or_else(|e| panic!("[{}] generate_nftables failed: {e}", s.name))
}

// ───────────────────────── nftables parsing ─────────────────────────

struct Chain {
    name: String,
    /// The `type ... policy ...;` line (or the whole inline body).
    header: String,
    /// Rule lines, comments stripped, in order.
    rules: Vec<String>,
}

fn strip_comment(line: &str) -> String {
    line.split(" #").next().unwrap_or(line).trim().to_string()
}

fn parse_chains(nft: &str) -> Vec<Chain> {
    let mut chains = Vec::new();
    let mut current: Option<Chain> = None;
    for raw in nft.lines() {
        let line = raw.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        match current.take() {
            Some(mut chain) => {
                if line == "}" {
                    chains.push(chain);
                } else {
                    if chain.header.is_empty() && line.starts_with("type ") {
                        chain.header = line.to_string();
                    } else {
                        chain.rules.push(strip_comment(line));
                    }
                    current = Some(chain);
                }
            }
            None => {
                if let Some(rest) = line.strip_prefix("chain ")
                    && let Some((name, body)) = rest.split_once('{')
                {
                    let name = name.trim().to_string();
                    match body.trim().strip_suffix('}') {
                        Some(inline) => chains.push(Chain { name, header: inline.trim().to_string(), rules: Vec::new() }),
                        None => current = Some(Chain { name, header: String::new(), rules: Vec::new() }),
                    }
                }
            }
        }
    }
    assert!(current.is_none(), "unterminated chain in generated nftables");
    chains
}

static IIF: LazyLock<Regex> = LazyLock::new(|| Regex::new(r#"\biifname\s+(\{[^}]*\}|"[^"]*"|\S+)"#).unwrap());
static OIF: LazyLock<Regex> = LazyLock::new(|| Regex::new(r#"\boifname\s+(\{[^}]*\}|"[^"]*"|\S+)"#).unwrap());
static DPORT: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"\bdport\s+(\{[^}]*\}|[^\s{]+)").unwrap());
static SPORT: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"\bsport\s+(\{[^}]*\}|[^\s{]+)").unwrap());
static SADDR: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"\bip saddr\s+(\{[^}]*\}|[0-9][0-9./]*)").unwrap());
static VERDICT: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"\b(accept|drop|reject|queue|jump|goto)\b").unwrap());

// ───────────────────────── IPv4 prefix helpers (trusted_networks) ─────────────────────────

/// `(network address, prefix length)` of an IPv4 CIDR; `None` for anything else
/// (IPv6, garbage). Host bits are masked off.
fn v4_net(text: &str) -> Option<(u32, u32)> {
    let (addr, prefix) = text.trim().split_once('/')?;
    let addr: std::net::Ipv4Addr = addr.parse().ok()?;
    let prefix: u32 = prefix.parse().ok().filter(|p| *p <= 32)?;
    Some((mask_v4(u32::from(addr), prefix), prefix))
}

fn mask_v4(addr: u32, prefix: u32) -> u32 {
    if prefix == 0 { 0 } else { addr & (u32::MAX << (32 - prefix)) }
}

/// `inner` is the same as, or a subnet of, `outer`.
fn within(inner: (u32, u32), outer: (u32, u32)) -> bool {
    outer.1 <= inner.1 && mask_v4(inner.0, outer.1) == outer.0
}

fn tokens(value: &str) -> Vec<String> {
    value.split(|c: char| c == ',' || c == '{' || c == '}' || c.is_whitespace()).map(|t| t.trim_matches('"').to_string()).filter(|t| !t.is_empty()).collect()
}

fn names(re: &Regex, line: &str) -> Vec<String> {
    re.captures_iter(line).flat_map(|c| tokens(&c[1])).collect()
}

fn ports(re: &Regex, line: &str) -> Vec<(i64, i64)> {
    let mut out = Vec::new();
    for c in re.captures_iter(line) {
        for tok in tokens(&c[1]) {
            if let Some((a, b)) = tok.split_once('-') {
                if let (Ok(a), Ok(b)) = (a.parse(), b.parse()) {
                    out.push((a, b));
                }
            } else if let Ok(p) = tok.parse() {
                out.push((p, p));
            }
        }
    }
    out
}

fn covers(ranges: &[(i64, i64)], port: i64) -> bool {
    ranges.iter().any(|(a, b)| *a <= port && port <= *b)
}

fn verdict(line: &str) -> &str {
    line.split_whitespace().last().unwrap_or("")
}

fn is_accept(line: &str) -> bool {
    verdict(line) == "accept"
}

fn is_est_only(line: &str) -> bool {
    line.strip_suffix("accept").is_some_and(|rest| rest.trim() == "ct state established,related")
}

// ───────────────────────── zones ─────────────────────────

fn is_wan(cfg: &SystemConfig, iface: &str) -> bool {
    iface == cfg.wan.interface || iface.starts_with("ppp")
}

fn lan_rt(cfg: &SystemConfig) -> String {
    cfg.runtime_lan_interface().to_string()
}

fn wg_if(cfg: &SystemConfig) -> Option<String> {
    cfg.wireguard.enabled.then(|| cfg.wireguard.interface.clone())
}

fn extras(cfg: &SystemConfig) -> Vec<&ExtraLANConfig> {
    cfg.firewall.extra_lans.iter().filter(|l| l.enabled).collect()
}

// ───────────────────────── invariant plumbing ─────────────────────────

struct Out {
    applicable: bool,
    bad: Vec<String>,
}

impl Out {
    fn new(applicable: bool) -> Out {
        Out { applicable, bad: Vec::new() }
    }

    fn rule(&mut self, s: &Sample, chain: &str, rule: &str, why: &str) {
        self.bad.push(format!("[{}] chain {chain}: `{rule}` -- {why}", s.name));
    }

    fn note(&mut self, s: &Sample, why: &str) {
        self.bad.push(format!("[{}] {why}", s.name));
    }
}

type Check = fn(&Sample, &[Chain]) -> Out;

fn chain_of<'a>(o: &mut Out, s: &Sample, chains: &'a [Chain], name: &str) -> Option<&'a Chain> {
    let found = chains.iter().find(|c| c.name == name);
    if found.is_none() {
        o.note(s, &format!("chain {name} is missing from the generated table"));
    }
    found
}

fn assert_valid(s: &Sample) {
    if let Err(err) = validate_live_candidate(&s.cfg, None) {
        panic!("sample `{}` is not a valid configuration (fix the sample, not the invariant): {err}", s.name);
    }
}

fn run(label: &str, min_applicable: usize, check: Check) {
    let all = samples();
    let mut bad = Vec::new();
    let mut applicable = 0;
    for s in &all {
        assert_valid(s);
        let chains = parse_chains(&render(s));
        let out = check(s, &chains);
        applicable += usize::from(out.applicable);
        bad.extend(out.bad);
    }
    eprintln!("{label}: {} samples checked, {applicable} applicable, {} violations", all.len(), bad.len());
    assert!(
        applicable >= min_applicable,
        "{label}: only {applicable} applicable samples (need {min_applicable}); the matrix no longer exercises this invariant"
    );
    assert!(bad.is_empty(), "{label}: {} violation(s):\n{}", bad.len(), bad.join("\n"));
}

// ───────────────────────── invariants ─────────────────────────

/// 1. input / forward / output declare `policy drop`; no other filter hook
///    chain can accept traffic (the observation chains carry no verdict).
fn inv1_policy_drop(s: &Sample, chains: &[Chain]) -> Out {
    let mut o = Out::new(true);
    for hook in ["input", "forward", "output"] {
        if let Some(c) = chain_of(&mut o, s, chains, hook)
            && (!c.header.contains(&format!("hook {hook} ")) || !c.header.contains("policy drop"))
        {
            o.rule(s, hook, &c.header, "chain must be hooked and declare `policy drop`");
        }
    }
    for c in chains {
        // Regular (non-hook) chains such as `device_profiles` are reached by `jump`
        // from the filter chains; none of them may accept anything.
        if !c.header.starts_with("type ") && !c.header.contains("hook") {
            for rule in c.rules.iter().filter(|r| is_accept(r)) {
                o.rule(s, &c.name, rule, "a jump-target chain accepts traffic");
            }
        }
        if c.header.contains("type filter") && !matches!(c.name.as_str(), "input" | "forward" | "output") {
            // Whole-word match: the counter is called `mr_accepted`, which is not a verdict.
            let verdict_free = !VERDICT.is_match(&c.header.replace("policy accept", ""));
            if !(c.name.starts_with("observe_") && c.rules.is_empty() && verdict_free) {
                o.rule(s, &c.name, &c.header, "extra filter-hook chain that is not a verdict-free observation chain");
            }
        }
    }
    o
}

/// 2. Nothing is accepted from the WAN zone in `input` except the WireGuard
///    server's own UDP listen-port rules (and the documented known-peer
///    endpoint variant); no unqualified rule accepts anything but
///    established/related.
fn inv2_wan_input(s: &Sample, chains: &[Chain]) -> Out {
    let mut o = Out::new(true);
    let Some(input) = chain_of(&mut o, s, chains, "input") else { return o };
    let cfg = &s.cfg;
    let peer_nets: BTreeSet<String> =
        cfg.wireguard.peers.iter().filter(|p| p.enabled).filter_map(|p| p.endpoint.rsplit_once(':').map(|(h, _)| format!("{h}/32"))).collect();
    for rule in input.rules.iter().filter(|r| is_accept(r)) {
        let iifs = names(&IIF, rule);
        if iifs.is_empty() {
            if !is_est_only(rule) {
                o.rule(s, "input", rule, "accept without an iifname qualifier that is not `ct state established,related`");
            }
            continue;
        }
        if !iifs.iter().any(|n| is_wan(cfg, n)) {
            continue;
        }
        let dports = ports(&DPORT, rule);
        let wg_only = cfg.wireguard.enabled
            && dports == vec![(cfg.wireguard.listen_port, cfg.wireguard.listen_port)]
            && rule.contains("udp dport")
            && !rule.contains("tcp");
        if !wg_only {
            o.rule(s, "input", rule, "accepted from the WAN zone and is not the WireGuard UDP listen-port exception");
        }
        for (what, port) in [("management HTTPS", cfg.system.https_port), ("ssh", 22), ("DNS", 53), ("DHCP", 67)] {
            if covers(&dports, port) {
                o.rule(s, "input", rule, &format!("{what} (port {port}) accepted from the WAN zone"));
            }
        }
        if iifs.iter().any(|n| !is_wan(cfg, n)) {
            o.rule(s, "input", rule, "mixes WAN and non-WAN interfaces in one accept");
        }
        for cap in SADDR.captures_iter(rule) {
            for src in tokens(&cap[1]) {
                if !peer_nets.contains(&src) {
                    o.rule(s, "input", rule, &format!("WAN accept restricted to {src}, which is not an enabled WireGuard peer endpoint"));
                }
            }
        }
    }
    o
}

/// 3. WAN anti-spoofing precedes `ct state established,related accept` in both
///    input and forward.
fn inv3_antispoof_order(s: &Sample, chains: &[Chain]) -> Out {
    let mut o = Out::new(!s.cfg.wan.interface.is_empty());
    let cfg = &s.cfg;
    type Pred = fn(&str) -> bool;
    let kinds: [(&str, Pred); 3] = [
        ("reserved-source drop", |r| r.contains("ip saddr") && r.contains("10.0.0.0/8") && r.contains("192.168.0.0/16") && r.contains("224.0.0.0/3")),
        ("fib reverse-path drop", |r| r.contains("fib saddr . iif oif missing")),
        ("IPv6 drop", |r| r.contains("meta nfproto ipv6")),
    ];
    for hook in ["input", "forward"] {
        let Some(chain) = chain_of(&mut o, s, chains, hook) else { continue };
        let Some(est) = chain.rules.iter().position(|r| is_est_only(r)) else {
            o.note(s, &format!("chain {hook} has no `ct state established,related accept` rule"));
            continue;
        };
        for zone in [cfg.wan.interface.as_str(), "ppp*"] {
            for (what, pred) in kinds {
                let found = chain.rules.iter().position(|r| names(&IIF, r) == vec![zone.to_string()] && verdict(r) == "drop" && pred(r));
                match found {
                    Some(i) if i < est => {}
                    Some(_) => o.note(s, &format!("chain {hook}: {what} for {zone} comes AFTER the established/related accept")),
                    None => o.note(s, &format!("chain {hook}: {what} for {zone} is missing")),
                }
            }
        }
    }
    o
}

/// 4. forward: nothing entering from WAN is accepted, WAN egress exists only
///    when WAN is enabled, and every DNAT rule is bound to the WireGuard
///    server interface.
fn inv4_forward_and_dnat(s: &Sample, chains: &[Chain]) -> Out {
    let mut o = Out::new(true);
    let cfg = &s.cfg;
    let lan = lan_rt(cfg);
    let trusted_in: BTreeSet<String> = [Some(lan.clone()), wg_if(cfg)].into_iter().flatten().collect();
    if let Some(fwd) = chain_of(&mut o, s, chains, "forward") {
        for rule in fwd.rules.iter().filter(|r| is_accept(r)) {
            if is_est_only(rule) {
                continue;
            }
            let (iifs, oifs) = (names(&IIF, rule), names(&OIF, rule));
            if iifs.is_empty() {
                o.rule(s, "forward", rule, "accept without an iifname qualifier");
            }
            if oifs.is_empty() {
                o.rule(s, "forward", rule, "accept without an oifname qualifier");
            }
            if iifs.iter().any(|n| is_wan(cfg, n)) {
                o.rule(s, "forward", rule, "accepts traffic entering from the WAN zone");
            }
            if oifs.iter().any(|n| is_wan(cfg, n)) {
                if !cfg.wan.enabled {
                    o.rule(s, "forward", rule, "WAN egress accepted although WAN is not enabled");
                }
                if iifs.iter().any(|n| !trusted_in.contains(n)) {
                    o.rule(s, "forward", rule, "WAN egress accepted from a source that is neither the LAN nor the WireGuard server");
                }
            }
        }
    }
    let wg = wg_if(cfg);
    for c in chains {
        for rule in &c.rules {
            let nat = rule.contains(" dnat ") || rule.contains(" redirect") || rule.contains("tproxy");
            if !nat {
                continue;
            }
            let iifs = names(&IIF, rule);
            if c.name != "prerouting" || !cfg.wireguard.enabled || iifs.len() != 1 || Some(&iifs[0]) != wg.as_ref() {
                o.rule(s, &c.name, rule, "DNAT/redirect that is not bound to the WireGuard server interface in prerouting");
            }
        }
    }
    if !cfg.wireguard.enabled && chains.iter().any(|c| c.name == "prerouting") {
        o.note(s, "prerouting chain generated while WireGuard is disabled");
    }
    o
}

/// 5. wireguard_only: no LAN rule accepts the management HTTPS port or tcp/22.
fn inv5_wireguard_only(s: &Sample, chains: &[Chain]) -> Out {
    let cfg = &s.cfg;
    let mut o = Out::new(cfg.system.management_access == "wireguard_only");
    if !o.applicable {
        return o;
    }
    let mut lan_names: BTreeSet<String> = [cfg.lan.interface.clone(), lan_rt(cfg)].into_iter().collect();
    if cfg.wifi.enabled {
        lan_names.insert(cfg.wifi.interface.clone());
    }
    if let Some(input) = chain_of(&mut o, s, chains, "input") {
        for rule in input.rules.iter().filter(|r| is_accept(r)) {
            let dports = ports(&DPORT, rule);
            if !(covers(&dports, cfg.system.https_port) || covers(&dports, 22)) {
                continue;
            }
            let iifs = names(&IIF, rule);
            if iifs.is_empty() || iifs.iter().any(|n| lan_names.contains(n)) {
                o.rule(s, "input", rule, "LAN/unqualified rule accepts management HTTPS or tcp/22 in wireguard_only mode");
            }
        }
    }
    if let Some(output) = chain_of(&mut o, s, chains, "output") {
        for rule in output.rules.iter().filter(|r| is_accept(r)) {
            let oifs = names(&OIF, rule);
            if oifs.iter().any(|n| lan_names.contains(n)) && covers(&ports(&SPORT, rule), cfg.system.https_port) {
                o.rule(s, "output", rule, "LAN reply path for management HTTPS exists in wireguard_only mode");
            }
        }
    }
    o
}

/// 6. Extra LAN: only ICMP reaches the router, nothing is forwarded out of the
///    segment, and only the configured sources reach the single destination.
fn inv6_extra_lan(s: &Sample, chains: &[Chain]) -> Out {
    let cfg = &s.cfg;
    let segs = extras(cfg);
    let mut o = Out::new(!segs.is_empty());
    let lan = lan_rt(cfg);
    let allowed_src_if: BTreeSet<String> = [Some(lan), wg_if(cfg)].into_iter().flatten().collect();
    let (Some(input), Some(fwd)) = (chain_of(&mut o, s, chains, "input"), chain_of(&mut o, s, chains, "forward")) else { return o };
    for seg in segs {
        for rule in input.rules.iter().filter(|r| is_accept(r) && names(&IIF, r).contains(&seg.interface)) {
            let icmp_only = rule.contains("ip protocol icmp") && !rule.contains("dport") && !rule.contains("tcp") && !rule.contains("udp");
            if !icmp_only {
                o.rule(s, "input", rule, &format!("router accepts something other than ICMP from extra LAN {}", seg.interface));
            }
        }
        for rule in fwd.rules.iter().filter(|r| is_accept(r) && names(&IIF, r).contains(&seg.interface)) {
            o.rule(s, "forward", rule, &format!("forwarding out of extra LAN {} is accepted", seg.interface));
        }
        let proto = if seg.protocol.is_empty() { "tcp" } else { seg.protocol.as_str() };
        let mut sources_seen = BTreeSet::new();
        for rule in fwd.rules.iter().filter(|r| is_accept(r) && names(&OIF, r).contains(&seg.interface)) {
            let iifs = names(&IIF, rule);
            if iifs.len() != 1 || !allowed_src_if.contains(&iifs[0]) {
                o.rule(s, "forward", rule, "traffic into the extra LAN is accepted from an interface that is neither LAN nor WireGuard");
            }
            let srcs: Vec<String> = SADDR.captures_iter(rule).flat_map(|c| tokens(&c[1])).collect();
            match srcs.as_slice() {
                [src] if seg.allow_from.iter().any(|a| a == src) => {
                    sources_seen.insert(src.clone());
                }
                _ => o.rule(s, "forward", rule, &format!("source is not exactly one configured allow_from entry {:?}", seg.allow_from)),
            }
            if !rule.contains(&format!("ip daddr {} ", seg.dst_ip)) || !rule.contains(&format!("{proto} dport {} ", seg.dst_port)) {
                o.rule(s, "forward", rule, &format!("destination is not exactly {}:{} ({proto})", seg.dst_ip, seg.dst_port));
            }
        }
        for src in seg.allow_from.iter() {
            if !sources_seen.contains(src) {
                o.note(s, &format!("allow_from {src} has no accept rule into {} (checked for completeness)", seg.interface));
            }
        }
    }
    o
}

/// 7. Outbound tunnel (wg1): new connections from it are dropped in input and
///    forward; nothing but the trusted home networks may enter it.
fn inv7_wg_client(s: &Sample, chains: &[Chain]) -> Out {
    let cfg = &s.cfg;
    let mut o = Out::new(cfg.wg_client.enabled);
    if !o.applicable {
        return o;
    }
    let tun = cfg.wg_client.interface.clone();
    let trusted: BTreeSet<String> = [Some(lan_rt(cfg)), wg_if(cfg)].into_iter().flatten().collect();
    for hook in ["input", "forward"] {
        let Some(chain) = chain_of(&mut o, s, chains, hook) else { continue };
        for rule in chain.rules.iter().filter(|r| is_accept(r)) {
            if names(&IIF, rule).contains(&tun) {
                o.rule(s, hook, rule, "accepts traffic entering from the outbound tunnel");
            }
            if hook == "forward" && names(&OIF, rule).contains(&tun) && names(&IIF, rule).iter().any(|n| !trusted.contains(n)) {
                o.rule(s, hook, rule, "traffic into the outbound tunnel is accepted from a non-home interface");
            }
        }
        let has_new_drop = chain.rules.iter().any(|r| names(&IIF, r) == vec![tun.clone()] && r.contains("ct state new") && verdict(r) == "drop");
        if !has_new_drop {
            o.note(s, &format!("chain {hook}: no `iifname \"{tun}\" ct state new drop` rule"));
        }
    }
    o
}

/// 8. Management / DNS / DHCP (and proxy) accepts are always qualified by the
///    LAN or WireGuard interface, and every accept in `input` comes from a
///    known zone.
fn inv8_qualified_services(s: &Sample, chains: &[Chain]) -> Out {
    let mut o = Out::new(true);
    let cfg = &s.cfg;
    let Some(input) = chain_of(&mut o, s, chains, "input") else { return o };
    let service_ok: BTreeSet<String> = [Some(lan_rt(cfg)), wg_if(cfg)].into_iter().flatten().collect();
    let mut known: BTreeSet<String> = service_ok.clone();
    known.insert("lo".into());
    for seg in extras(cfg) {
        known.insert(seg.interface.clone());
    }
    let mut guarded = vec![cfg.system.https_port, 22, 53, 67];
    if cfg.squid_proxy.enabled {
        guarded.push(if cfg.squid_proxy.port == 0 { 3128 } else { cfg.squid_proxy.port });
    }
    for rule in input.rules.iter().filter(|r| is_accept(r)) {
        if is_est_only(rule) {
            continue;
        }
        let iifs = names(&IIF, rule);
        let dports = ports(&DPORT, rule);
        let hit: Vec<i64> = guarded.iter().copied().filter(|p| covers(&dports, *p)).collect();
        if !hit.is_empty() && (iifs.is_empty() || iifs.iter().any(|n| !service_ok.contains(n))) {
            o.rule(s, "input", rule, &format!("service port(s) {hit:?} accepted without being qualified by the LAN/WireGuard interface (iifname {iifs:?})"));
        }
        for n in &iifs {
            if !known.contains(n) && !is_wan(cfg, n) {
                o.rule(s, "input", rule, &format!("accept from unexpected interface `{n}`"));
            }
        }
    }
    o
}

/// A rule that can let tcp/22 into the router: it accepts, is not the
/// established/related rule, is bound to a non-loopback interface, and either
/// covers port 22 or restricts neither port nor protocol.
fn can_accept_ssh(rule: &str) -> bool {
    if !is_accept(rule) || is_est_only(rule) {
        return false;
    }
    let iifs = names(&IIF, rule);
    if iifs.iter().all(|n| n == "lo") && !iifs.is_empty() {
        return false;
    }
    let dports = ports(&DPORT, rule);
    if !dports.is_empty() {
        return covers(&dports, 22);
    }
    !rule.contains("ip protocol") && !rule.contains("udp ") && !rule.contains("tcp ")
}

/// The interface of an unconditional tcp/22 drop: a `drop` bound to exactly one
/// interface that matches tcp/22 and nothing narrower (no source, state,
/// meter, rate limit or other qualifier), so every later accept on that
/// interface is unable to let SSH in.
fn ssh_drop_interface(rule: &str) -> Option<String> {
    if verdict(rule) != "drop" || SADDR.is_match(rule) || !rule.contains("tcp dport") {
        return None;
    }
    if ["ct state", "meter", "limit", "saddr", "daddr", "fib", "meta", "ip protocol", "udp "].iter().any(|q| rule.contains(q)) {
        return None;
    }
    if ports(&DPORT, rule) != vec![(22, 22)] {
        return None;
    }
    match names(&IIF, rule).as_slice() {
        [only] => Some(only.clone()),
        _ => None,
    }
}

/// 9. SSH: every rule that can accept tcp/22 carries an `ip saddr` set whose
///    members all lie inside the IPv4 members of `trusted_networks`, OR is
///    preceded in the chain by that interface's unconditional tcp/22 drop (so
///    it can never see an SSH packet); with no IPv4 trusted network no
///    reachable accept may exist at all. The interfaces that must offer SSH
///    (LAN unless wireguard_only, wg0 when enabled) offer it to every trusted
///    IPv4 network (checked for completeness).
fn inv9_ssh_source_restricted(s: &Sample, chains: &[Chain]) -> Out {
    let mut o = Out::new(true);
    let cfg = &s.cfg;
    let trusted: Vec<(u32, u32)> = cfg.trusted_networks.iter().filter_map(|n| v4_net(n)).collect();
    let Some(input) = chain_of(&mut o, s, chains, "input") else { return o };
    let mut offered: Vec<(String, Vec<(u32, u32)>)> = Vec::new();
    let mut dropped: BTreeSet<String> = BTreeSet::new();
    for rule in input.rules.iter() {
        if let Some(iface) = ssh_drop_interface(rule) {
            dropped.insert(iface);
            continue;
        }
        if !can_accept_ssh(rule) {
            continue;
        }
        let iifs = names(&IIF, rule);
        if !iifs.is_empty() && iifs.iter().all(|i| dropped.contains(i)) {
            continue;
        }
        if trusted.is_empty() {
            o.rule(s, "input", rule, "can accept tcp/22 although trusted_networks lists no IPv4 network (SSH must be closed)");
            continue;
        }
        let members: Vec<String> = SADDR.captures_iter(rule).flat_map(|c| tokens(&c[1])).collect();
        if members.is_empty() {
            o.rule(s, "input", rule, "can accept tcp/22 without an `ip saddr` restriction (reachable from the whole interface)");
            continue;
        }
        let mut parsed = Vec::new();
        for m in &members {
            match v4_net(m) {
                Some(n) if trusted.iter().any(|t| within(n, *t)) => parsed.push(n),
                _ => o.rule(s, "input", rule, &format!("tcp/22 source `{m}` is not inside the IPv4 trusted_networks {:?}", cfg.trusted_networks)),
            }
        }
        for iface in names(&IIF, rule) {
            offered.push((iface, parsed.clone()));
        }
    }
    if !trusted.is_empty() {
        let mut expected: Vec<String> = Vec::new();
        if cfg.system.management_access != "wireguard_only" {
            expected.push(lan_rt(cfg));
        }
        expected.extend(wg_if(cfg));
        for iface in expected {
            let members: Vec<(u32, u32)> = offered.iter().filter(|(i, _)| *i == iface).flat_map(|(_, m)| m.iter().copied()).collect();
            if members.is_empty() {
                o.note(s, &format!("no tcp/22 accept on `{iface}` although trusted_networks has IPv4 members"));
            }
            for t in &trusted {
                if !members.is_empty() && !members.iter().any(|m| within(*t, *m)) {
                    o.note(s, &format!("trusted network {}/{} is missing from the tcp/22 source set on `{iface}`", std::net::Ipv4Addr::from(t.0), t.1));
                }
            }
        }
    }
    o
}

/// 10. Squid is an Internet-only proxy: its own egress accepts reach only the
///     WAN zone (80/443) or answer LAN clients; private tunnels are dropped.
fn inv10_squid_egress(s: &Sample, chains: &[Chain]) -> Out {
    let cfg = &s.cfg;
    let mut o = Out::new(cfg.squid_proxy.enabled);
    if !o.applicable {
        return o;
    }
    let Some(output) = chain_of(&mut o, s, chains, "output") else { return o };
    let lan = lan_rt(cfg);
    for rule in output.rules.iter().filter(|r| r.contains("meta skuid squid") && is_accept(r)) {
        let oifs = names(&OIF, rule);
        let dports = ports(&DPORT, rule);
        for n in &oifs {
            let wan_ok = is_wan(cfg, n) && !dports.is_empty() && dports.iter().all(|(a, b)| a == b && (*a == 80 || *a == 443));
            let reply_ok = *n == lan && covers(&ports(&SPORT, rule), cfg.squid_proxy.port);
            if !wan_ok && !reply_ok {
                o.rule(s, "output", rule, &format!("Squid egress accepted via `{n}`"));
            }
        }
        if oifs.is_empty() {
            o.rule(s, "output", rule, "Squid accept without an oifname qualifier");
        }
    }
    let mut private: Vec<String> = extras(cfg).iter().map(|l| l.interface.clone()).collect();
    if cfg.wireguard.enabled {
        private.push(cfg.wireguard.interface.clone());
    }
    if cfg.wg_client.enabled {
        private.push(cfg.wg_client.interface.clone());
    }
    for n in private {
        let has = output.rules.iter().any(|r| r.contains("meta skuid squid") && names(&OIF, r) == vec![n.clone()] && verdict(r) == "drop");
        if !has {
            o.note(s, &format!("output: no `meta skuid squid oifname \"{n}\" drop` rule"));
        }
    }
    o
}

// ───────────────────────── the invariant tests ─────────────────────────

#[test]
fn matrix_samples_are_valid_and_render() {
    let all = samples();
    assert!(all.len() >= 22, "matrix shrank to {} samples", all.len());
    let mut seen = BTreeSet::new();
    for s in &all {
        assert!(seen.insert(s.name), "duplicate sample name {}", s.name);
        assert_valid(s);
        let nft = render(s);
        assert!(nft.contains("table inet minimalrouter"), "[{}] unexpected table", s.name);
        assert_eq!(generate_nftables(&s.cfg).unwrap(), nft, "[{}] generator is not deterministic", s.name);
    }
    eprintln!("matrix: {} valid samples: {:?}", all.len(), all.iter().map(|s| s.name).collect::<Vec<_>>());
}

#[test]
fn inv1_input_forward_output_policy_drop() {
    run("inv1 policy drop", 16, inv1_policy_drop);
}

#[test]
fn inv2_nothing_accepted_from_wan_except_wireguard_listen_port() {
    run("inv2 WAN input", 16, inv2_wan_input);
}

#[test]
fn inv3_wan_antispoofing_precedes_established_accept() {
    run("inv3 anti-spoof order", 16, inv3_antispoof_order);
}

#[test]
fn inv4_forward_never_accepts_wan_and_dnat_is_wireguard_bound() {
    run("inv4 forward/DNAT", 16, inv4_forward_and_dnat);
}

#[test]
fn inv5_wireguard_only_management_is_not_reachable_from_lan() {
    run("inv5 wireguard_only", 2, inv5_wireguard_only);
}

#[test]
fn inv6_extra_lan_is_isolated() {
    run("inv6 extra LAN", 3, inv6_extra_lan);
}

#[test]
fn inv7_outbound_tunnel_is_established_only() {
    run("inv7 wg1", 3, inv7_wg_client);
}

#[test]
fn inv8_service_accepts_are_interface_qualified() {
    run("inv8 qualified services", 16, inv8_qualified_services);
}

#[test]
fn inv9_ssh_is_accepted_only_from_trusted_ipv4_networks() {
    run("inv9 ssh sources", 16, inv9_ssh_source_restricted);
}

/// Kept strict on purpose: both configurations below pass
/// `validate_live_candidate`; the generated policy must not let the whole LAN
/// reach tcp/22 although `trusted_networks` is a single host. If this test
/// fails, the invariant is doing its job; do not weaken it.
#[test]
fn inv9_custom_input_rules_cannot_bypass_the_ssh_trust_boundary() {
    let all = bypass_samples();
    let mut bad = Vec::new();
    for s in &all {
        assert_valid(s);
        bad.extend(inv9_ssh_source_restricted(s, &parse_chains(&render(s))).bad);
    }
    eprintln!("inv9 custom-input bypass: {} samples checked, {} violations", all.len(), bad.len());
    assert!(bad.is_empty(), "inv9 bypass: {} violation(s):\n{}", bad.len(), bad.join("\n"));
}

#[test]
fn inv10_squid_egress_is_internet_only() {
    run("inv10 squid egress", 2, inv10_squid_egress);
}

/// Guards against vacuous passes: the rules the invariants are about must
/// actually be present in the samples that are supposed to have them.
#[test]
fn positive_controls_rules_exist_where_expected() {
    let all = samples();
    let by = |n: &str| all.iter().find(|s| s.name == n).unwrap_or_else(|| panic!("no sample {n}"));
    let chains = |n: &str| parse_chains(&render(by(n)));
    let find = |chains: &[Chain], hook: &str, pred: &dyn Fn(&str) -> bool| chains.iter().find(|c| c.name == hook).unwrap().rules.iter().any(|r| pred(r));

    // Default: LAN management and DNS/DHCP exist, nothing LAN->WAN (WAN is off).
    let c = chains("default");
    assert!(
        find(&c, "input", &|r| is_accept(r) && names(&IIF, r) == vec!["eth1".to_string()] && covers(&ports(&DPORT, r), 8443)),
        "default: LAN HTTPS accept missing"
    );
    assert!(
        find(&c, "input", &|r| is_accept(r) && names(&IIF, r) == vec!["eth1".to_string()] && covers(&ports(&DPORT, r), 22)),
        "default: LAN ssh accept missing"
    );
    assert!(find(&c, "input", &|r| is_accept(r) && covers(&ports(&DPORT, r), 53) && covers(&ports(&DPORT, r), 67)), "default: LAN DNS/DHCP accept missing");
    assert!(
        !find(&c, "forward", &|r| is_accept(r) && names(&OIF, r).iter().any(|n| n == "eth0" || n.starts_with("ppp"))),
        "default: WAN egress present with WAN disabled"
    );

    // WAN enabled: LAN->WAN forwarding exists.
    let c = chains("wan_pppoe");
    assert!(
        find(&c, "forward", &|r| is_accept(r) && names(&IIF, r) == vec!["eth1".to_string()] && names(&OIF, r) == vec!["eth0".to_string()]),
        "wan_pppoe: LAN->WAN accept missing"
    );
    assert!(find(&c, "forward", &|r| is_accept(r) && names(&OIF, r) == vec!["ppp*".to_string()]), "wan_pppoe: LAN->ppp* accept missing");

    // WireGuard server: the listen-port exception exists on both WAN spellings,
    // and the known-peer variants exist for the enabled peers only.
    let c = chains("wireguard_server");
    for zone in ["eth0", "ppp*"] {
        assert!(
            find(&c, "input", &|r| is_accept(r) && names(&IIF, r) == vec![zone.to_string()] && covers(&ports(&DPORT, r), 51820) && !r.contains("ip saddr")),
            "wireguard_server: {zone} listen-port accept missing"
        );
        assert!(
            find(&c, "input", &|r| is_accept(r) && names(&IIF, r) == vec![zone.to_string()] && r.contains("203.0.113.9/32")),
            "wireguard_server: {zone} known-peer accept missing"
        );
    }
    assert!(!render(by("wireguard_server")).contains("198.51.100.77"), "disabled peer endpoint leaked into the firewall");

    // wireguard_only: the LAN HTTPS/ssh accepts are really gone, wg0 keeps them.
    let c = chains("wireguard_only_mgmt");
    assert!(
        !find(&c, "input", &|r| is_accept(r)
            && names(&IIF, r) == vec!["eth1".to_string()]
            && (covers(&ports(&DPORT, r), 8443) || covers(&ports(&DPORT, r), 22))),
        "wireguard_only: LAN management accept present"
    );
    assert!(
        find(&c, "input", &|r| is_accept(r) && names(&IIF, r) == vec!["wg0".to_string()] && covers(&ports(&DPORT, r), 8443)),
        "wireguard_only: wg0 HTTPS accept missing"
    );

    // Port forwards: 4 DNAT lines (tcp, udp, both=2); the disabled rule renders nothing.
    let nft = render(by("tunnel_port_forwards"));
    let c = parse_chains(&nft);
    let dnat: Vec<&String> = c.iter().flat_map(|c| c.rules.iter()).filter(|r| r.contains(" dnat ")).collect();
    assert_eq!(dnat.len(), 4, "expected tcp+udp+both(2) DNAT rules, got {dnat:?}");
    assert!(!nft.contains("9999"), "disabled port forward was rendered");

    // Extra LAN: one accept per allow_from entry, ICMP only into the router.
    let c = chains("extra_lan_udp_with_wan_and_wg");
    let into: Vec<&String> =
        c.iter().find(|c| c.name == "forward").unwrap().rules.iter().filter(|r| is_accept(r) && names(&OIF, r) == vec!["eth2".to_string()]).collect();
    assert_eq!(into.len(), 2, "expected one accept per allow_from entry, got {into:?}");
    assert!(
        find(&c, "input", &|r| is_accept(r) && names(&IIF, r) == vec!["eth2".to_string()] && r.contains("ip protocol icmp")),
        "extra LAN: ICMP accept missing"
    );

    // Outbound tunnel: the drops exist in both chains.
    let c = chains("wg_client_outbound");
    for hook in ["input", "forward"] {
        assert!(
            find(&c, hook, &|r| names(&IIF, r) == vec!["wg1".to_string()] && r.contains("ct state new") && verdict(r) == "drop"),
            "wg_client: {hook} new-drop missing"
        );
    }

    // SSH follows trusted_networks: one host, no IPv4 network, overlapping entries.
    assert!(render(by("default")).contains("iifname \"eth1\" ip saddr { 192.168.1.0/24 } tcp dport 22 accept"), "default: SSH accept shape changed");
    assert!(
        render(by("ssh_trusted_single_host")).contains("iifname \"eth1\" ip saddr { 192.168.1.10/32 } tcp dport 22 accept"),
        "single host: SSH set missing"
    );
    let nft = render(by("ssh_trusted_ipv6_only"));
    assert!(nft.contains("# SSH is closed on eth1: trusted_networks lists no IPv4 network"), "IPv6-only: closed-SSH marker missing");
    assert!(!find(&parse_chains(&nft), "input", &|r| can_accept_ssh(r)), "IPv6-only: a rule can still accept SSH");
    assert!(
        render(by("ssh_trusted_overlap_and_host_bits")).contains("iifname \"eth1\" ip saddr { 192.168.1.0/24 } tcp dport 22 accept"),
        "overlap: set not canonicalised"
    );
    let nft = render(by("ssh_wg_only_tunnel_trusted_only"));
    assert!(nft.contains("iifname \"wg0\" ip saddr { 10.8.0.0/24 } tcp dport 22 accept"), "wireguard_only: wg0 SSH rule missing");
    assert!(
        !find(&parse_chains(&nft), "input", &|r| can_accept_ssh(r) && names(&IIF, r) == vec!["eth1".to_string()]),
        "wireguard_only: a LAN rule can accept SSH"
    );
    let nft = render(by("ssh_wg_two_hosts_wifi"));
    for iface in ["br-lan", "wg0"] {
        assert!(
            nft.contains(&format!("iifname \"{iface}\" ip saddr {{ 192.168.1.10/32, 10.8.0.2/32 }} tcp dport 22 accept")),
            "{iface}: SSH set missing\n{nft}"
        );
    }

    // The jump-target chain exists where DNS filtering is on (so inv1 really inspects it).
    assert!(
        chains("everything_on_lan_mgmt").iter().any(|c| c.name == "device_profiles" && !c.rules.is_empty()),
        "device_profiles chain missing from the everything sample"
    );

    // Disabled custom rules render nothing.
    let nft = render(by("everything_on_wg_only"));
    assert!(!nft.contains("Disabled SSH"), "disabled custom rule rendered");
    // Custom input allow exists in the LAN-management sample, bound to LAN.
    let c = chains("custom_rules");
    assert!(
        find(&c, "input", &|r| is_accept(r) && names(&IIF, r) == vec!["eth1".to_string()] && covers(&ports(&DPORT, r), 2222)),
        "custom input allow missing"
    );
}

// ───────────────────────── checker self-tests ─────────────────────────

fn insert_into_chain(nft: &str, chain: &str, rule: &str) -> String {
    let (mut out, mut current, mut done) = (String::new(), String::new(), false);
    for line in nft.lines() {
        let t = line.trim();
        if let Some(rest) = t.strip_prefix("chain ") {
            current = rest.split_whitespace().next().unwrap_or("").to_string();
        }
        out.push_str(line);
        out.push('\n');
        if !done && current == chain && t.starts_with("type ") {
            out.push_str(&format!("    {rule}\n"));
            done = true;
        }
    }
    assert!(done, "chain {chain} not found for mutation");
    out
}

fn rewrite_chain(nft: &str, chain: &str, f: &dyn Fn(&str) -> Option<String>) -> String {
    let (mut out, mut current, mut changed) = (String::new(), String::new(), false);
    for line in nft.lines() {
        let t = line.trim();
        if let Some(rest) = t.strip_prefix("chain ") {
            current = rest.split_whitespace().next().unwrap_or("").to_string();
        }
        if current == chain {
            match f(line) {
                Some(new) if new != line => {
                    changed = true;
                    if !new.is_empty() {
                        out.push_str(&new);
                        out.push('\n');
                    }
                    continue;
                }
                _ => {}
            }
        }
        out.push_str(line);
        out.push('\n');
    }
    assert!(changed, "mutation of chain {chain} changed nothing");
    out
}

fn violations(check: Check, s: &Sample, nft: &str) -> Vec<String> {
    check(s, &parse_chains(nft)).bad
}

#[test]
fn checkers_detect_injected_violations() {
    let all = samples();
    let by = |n: &str| all.iter().find(|s| s.name == n).unwrap();
    let must_fire = |what: &str, check: Check, sample: &str, mutated: String| {
        let s = by(sample);
        assert!(violations(check, s, &render(s)).is_empty(), "{what}: baseline for {sample} is not clean");
        let bad = violations(check, s, &mutated);
        assert!(!bad.is_empty(), "{what}: the invariant did not fire on a deliberately injected violation in {sample}");
    };
    let nft = |n: &str| render(by(n));

    must_fire("WAN HTTPS accept", inv2_wan_input, "wan_pppoe", insert_into_chain(&nft("wan_pppoe"), "input", "iifname \"eth0\" tcp dport 8443 accept"));
    must_fire("ppp ssh accept", inv2_wan_input, "wan_pppoe", insert_into_chain(&nft("wan_pppoe"), "input", "iifname \"ppp*\" tcp dport 22 accept"));
    must_fire("unqualified DNS accept", inv2_wan_input, "default", insert_into_chain(&nft("default"), "input", "udp dport 53 accept"));
    must_fire(
        "unqualified DNS accept (service qualifier)",
        inv8_qualified_services,
        "default",
        insert_into_chain(&nft("default"), "input", "udp dport 53 accept"),
    );
    must_fire(
        "WAN wg-port accept for a stranger",
        inv2_wan_input,
        "wireguard_server",
        insert_into_chain(&nft("wireguard_server"), "input", "iifname \"eth0\" ip saddr { 8.8.8.8/32 } udp dport 51820 accept"),
    );
    must_fire(
        "forward policy accept",
        inv1_policy_drop,
        "default",
        rewrite_chain(&nft("default"), "forward", &|l| Some(l.replace("policy drop", "policy accept"))),
    );
    must_fire(
        "observation chain gains a verdict",
        inv1_policy_drop,
        "default",
        nft("default").replace("counter name mr_accepted; }", "counter name mr_accepted; accept }"),
    );
    must_fire(
        "forward accept from ppp",
        inv4_forward_and_dnat,
        "wan_pppoe",
        insert_into_chain(&nft("wan_pppoe"), "forward", "iifname \"ppp*\" oifname \"eth1\" accept"),
    );
    must_fire(
        "WAN egress with WAN off",
        inv4_forward_and_dnat,
        "default",
        insert_into_chain(&nft("default"), "forward", "iifname \"eth1\" oifname \"eth0\" accept"),
    );
    must_fire(
        "WAN DNAT",
        inv4_forward_and_dnat,
        "tunnel_port_forwards",
        insert_into_chain(&nft("tunnel_port_forwards"), "prerouting", "iifname \"eth0\" tcp dport 80 dnat ip to 192.168.1.9:80"),
    );
    must_fire(
        "anti-spoof removed from forward",
        inv3_antispoof_order,
        "wan_pppoe",
        rewrite_chain(&nft("wan_pppoe"), "forward", &|l| l.contains("fib saddr . iif oif missing").then(String::new)),
    );
    must_fire(
        "ipv6 drop removed from input",
        inv3_antispoof_order,
        "default",
        rewrite_chain(&nft("default"), "input", &|l| l.contains("meta nfproto ipv6").then(String::new)),
    );
    must_fire(
        "LAN https in wireguard_only",
        inv5_wireguard_only,
        "wireguard_only_mgmt",
        insert_into_chain(&nft("wireguard_only_mgmt"), "input", "iifname \"eth1\" tcp dport 8443 accept"),
    );
    must_fire(
        "LAN ssh in wireguard_only",
        inv5_wireguard_only,
        "wireguard_only_mgmt",
        insert_into_chain(&nft("wireguard_only_mgmt"), "input", "iifname \"eth1\" ip saddr { 192.168.1.0/24 } tcp dport 22 accept"),
    );
    must_fire(
        "extra LAN forwards to WAN",
        inv6_extra_lan,
        "extra_lan_udp_with_wan_and_wg",
        insert_into_chain(&nft("extra_lan_udp_with_wan_and_wg"), "forward", "iifname \"eth2\" oifname \"eth0\" accept"),
    );
    must_fire(
        "extra LAN ssh to router",
        inv6_extra_lan,
        "extra_lan_tcp",
        insert_into_chain(&nft("extra_lan_tcp"), "input", "iifname \"eth2\" tcp dport 22 accept"),
    );
    must_fire(
        "extra LAN open destination",
        inv6_extra_lan,
        "extra_lan_tcp",
        insert_into_chain(
            &nft("extra_lan_tcp"),
            "forward",
            "iifname \"eth1\" ip saddr 192.168.1.0/24 ip daddr 192.168.60.99 tcp dport 8096 oifname \"eth2\" accept",
        ),
    );
    must_fire(
        "wg1 initiates in input",
        inv7_wg_client,
        "wg_client_outbound",
        insert_into_chain(&nft("wg_client_outbound"), "input", "iifname \"wg1\" tcp dport 8443 accept"),
    );
    must_fire(
        "wg1 new-drop removed",
        inv7_wg_client,
        "wg_client_outbound",
        rewrite_chain(&nft("wg_client_outbound"), "forward", &|l| (l.contains("iifname \"wg1\"") && l.contains("ct state new drop")).then(String::new)),
    );
    must_fire(
        "squid to wg0",
        inv10_squid_egress,
        "everything_on_lan_mgmt",
        insert_into_chain(&nft("everything_on_lan_mgmt"), "output", "meta skuid squid oifname \"wg0\" tcp dport 443 accept"),
    );
    must_fire(
        "squid drop to tunnel removed",
        inv10_squid_egress,
        "everything_on_lan_mgmt",
        rewrite_chain(&nft("everything_on_lan_mgmt"), "output", &|l| (l.contains("meta skuid squid oifname \"wg1\" drop")).then(String::new)),
    );

    // SSH (inv9).
    must_fire(
        "ssh without ip saddr",
        inv9_ssh_source_restricted,
        "default",
        insert_into_chain(&nft("default"), "input", "iifname \"eth1\" tcp dport 22 accept"),
    );
    must_fire(
        "ssh from outside trusted_networks",
        inv9_ssh_source_restricted,
        "ssh_trusted_single_host",
        insert_into_chain(&nft("ssh_trusted_single_host"), "input", "iifname \"eth1\" ip saddr { 192.168.1.0/24 } tcp dport 22 accept"),
    );
    must_fire(
        "ssh set widened",
        inv9_ssh_source_restricted,
        "ssh_trusted_single_host",
        rewrite_chain(&nft("ssh_trusted_single_host"), "input", &|l| {
            Some(l.replace("{ 192.168.1.10/32 } tcp dport 22", "{ 192.168.1.10/32, 10.0.0.0/8 } tcp dport 22"))
        }),
    );
    must_fire(
        "ssh open with no IPv4 trusted network",
        inv9_ssh_source_restricted,
        "ssh_trusted_ipv6_only",
        insert_into_chain(&nft("ssh_trusted_ipv6_only"), "input", "iifname \"eth1\" ip saddr { 192.168.1.0/24 } tcp dport 22 accept"),
    );
    must_fire(
        "unrestricted LAN accept can carry ssh",
        inv9_ssh_source_restricted,
        "default",
        insert_into_chain(&nft("default"), "input", "iifname \"eth1\" counter accept"),
    );
    must_fire(
        "ssh accept removed",
        inv9_ssh_source_restricted,
        "default",
        rewrite_chain(&nft("default"), "input", &|l| l.contains("tcp dport 22 accept").then(String::new)),
    );
    must_fire(
        "ssh on wg0 without saddr",
        inv9_ssh_source_restricted,
        "wireguard_server",
        insert_into_chain(&nft("wireguard_server"), "input", "iifname \"wg0\" tcp dport 22 accept"),
    );
    // The custom-rule samples: clean as generated, flagged once the SSH drop is
    // removed, weakened, or moved behind the custom accept.
    let drop_line = "iifname \"eth1\" tcp dport 22 drop";
    for s in bypass_samples() {
        let nft = render(&s);
        assert!(violations(inv9_ssh_source_restricted, &s, &nft).is_empty(), "{}: baseline must be clean", s.name);
        assert!(nft.contains(drop_line), "{}: the SSH drop is missing from the generated policy", s.name);
        let is_custom_accept = |l: &str| l.contains("counter name mr_rule_") && l.trim_end().ends_with("accept");
        let removed = rewrite_chain(&nft, "input", &|l| (l.trim() == drop_line).then(String::new));
        let weakened =
            rewrite_chain(&nft, "input", &|l| (l.trim() == drop_line).then(|| "    iifname \"eth1\" ip saddr { 10.0.0.0/8 } tcp dport 22 drop".to_string()));
        let moved = rewrite_chain(&removed, "input", &|l| is_custom_accept(l).then(|| format!("{l}\n    {drop_line}")));
        for (what, mutated) in [("drop removed", removed), ("drop restricted to a source", weakened), ("drop moved after the custom accept", moved)] {
            assert!(!violations(inv9_ssh_source_restricted, &s, &mutated).is_empty(), "{}: inv9 did not fire with the {what}", s.name);
        }
    }
    // A drop for a different interface does not cover the LAN accept.
    let s = &bypass_samples()[0];
    let other_if = rewrite_chain(&render(s), "input", &|l| (l.trim() == drop_line).then(|| "    iifname \"eth9\" tcp dport 22 drop".to_string()));
    assert!(!violations(inv9_ssh_source_restricted, s, &other_if).is_empty(), "a drop on another interface must not satisfy inv9");
}

/// The matchers key on interface/port/source tokens, not on whole-line text:
/// splitting the SSH accept into a rate-limit drop plus a `counter accept`
/// (same interface and source set) must leave every invariant satisfied.
#[test]
fn invariants_tolerate_equivalent_ssh_rule_shapes() {
    let checks: [(&str, Check); 10] = [
        ("inv1", inv1_policy_drop),
        ("inv2", inv2_wan_input),
        ("inv3", inv3_antispoof_order),
        ("inv4", inv4_forward_and_dnat),
        ("inv5", inv5_wireguard_only),
        ("inv6", inv6_extra_lan),
        ("inv7", inv7_wg_client),
        ("inv8", inv8_qualified_services),
        ("inv9", inv9_ssh_source_restricted),
        ("inv10", inv10_squid_egress),
    ];
    let mut rewritten = 0;
    for s in samples().iter().filter(|s| render(s).contains("tcp dport 22 accept")) {
        let nft = rewrite_chain(&render(s), "input", &|l| {
            l.trim_end().ends_with("tcp dport 22 accept").then(|| {
                let limited = l.replace(
                    "tcp dport 22 accept",
                    "tcp dport 22 ct state new meter ssh_rate { ip saddr timeout 1m limit rate over 6/minute burst 10 packets } drop",
                );
                format!("{limited}\n{}", l.replace("tcp dport 22 accept", "tcp dport 22 counter accept"))
            })
        });
        rewritten += 1;
        for (label, check) in checks {
            let bad = violations(check, s, &nft);
            assert!(bad.is_empty(), "{label} broke on an equivalent SSH rule shape: {}", bad.join("\n"));
        }
    }
    assert!(rewritten >= 10, "only {rewritten} samples carried an SSH accept");
}

// ───────────────────────── other generators ─────────────────────────

#[test]
fn dnsmasq_binds_only_loopback_lan_and_wireguard_and_blocks_rebinding() {
    let mut checked = 0;
    for s in samples() {
        assert_valid(&s);
        let cfg = &s.cfg;
        let text = generate_dnsmasq(cfg).unwrap();
        let allowed_if: BTreeSet<String> = [Some(lan_rt(cfg)), wg_if(cfg)].into_iter().flatten().collect();
        let interfaces: BTreeSet<String> = text.lines().filter_map(|l| l.strip_prefix("interface=")).map(str::to_string).collect();
        assert_eq!(interfaces, allowed_if, "[{}] dnsmasq `interface=` set differs from LAN(+wg0)\n{text}", s.name);
        let listens: Vec<&str> = text.lines().filter_map(|l| l.strip_prefix("listen-address=")).collect();
        assert_eq!(listens.len(), 1, "[{}] expected exactly one listen-address line\n{text}", s.name);
        let got: BTreeSet<&str> = listens[0].split(',').collect();
        let mut want: BTreeSet<String> = ["127.0.0.1".to_string(), cfg.lan.ip_address.clone()].into_iter().collect();
        if cfg.wireguard.enabled {
            want.insert(cfg.wireguard.address.split('/').next().unwrap().to_string());
        }
        assert_eq!(got, want.iter().map(String::as_str).collect::<BTreeSet<_>>(), "[{}] dnsmasq listen-address set", s.name);
        assert!(text.lines().any(|l| l == "stop-dns-rebind"), "[{}] stop-dns-rebind missing", s.name);
        for banned in ["except-interface", "listen-address=0.0.0.0", "interface=lo\n"] {
            assert!(!text.contains(banned), "[{}] dnsmasq contains `{banned}`", s.name);
        }
        // Neither the WAN interface, the extra LAN segments nor the outbound tunnel is a listener.
        for seg in extras(cfg) {
            assert!(!text.contains(&seg.interface), "[{}] dnsmasq mentions extra LAN interface {}", s.name, seg.interface);
            assert!(!text.contains("192.168.60.1"), "[{}] dnsmasq mentions the extra LAN router address", s.name);
        }
        assert!(!interfaces.contains(&cfg.wan.interface) && !interfaces.iter().any(|i| i.starts_with("ppp")), "[{}] dnsmasq listens on WAN", s.name);
        checked += 1;
    }
    eprintln!("dnsmasq: {checked} samples checked");
}

#[test]
fn hostapd_is_wpa2_ccmp_without_wps_and_bridges_into_the_lan() {
    let mut checked = 0;
    for s in samples() {
        let text = generate_hostapd(&s.cfg).unwrap();
        if !s.cfg.wifi.enabled {
            assert!(
                !text.lines().any(|l| l.starts_with("interface=") || l.starts_with("ssid=")),
                "[{}] hostapd renders an AP while Wi-Fi is off\n{text}",
                s.name
            );
            continue;
        }
        checked += 1;
        let lines: BTreeSet<&str> = text.lines().collect();
        for want in ["wpa=2", "rsn_pairwise=CCMP", "bridge=br-lan", "ieee80211w=1"] {
            assert!(lines.contains(want), "[{}] hostapd is missing `{want}`\n{text}", s.name);
        }
        for l in text.lines() {
            let key = l.split('=').next().unwrap_or("");
            assert!(!key.starts_with("wps") && key != "config_methods" && key != "device_name", "[{}] WPS-related hostapd line `{l}`", s.name);
            assert!(!l.to_ascii_lowercase().contains("tkip") && !l.contains("wpa_pairwise"), "[{}] weak pairwise cipher line `{l}`", s.name);
            assert!(!l.starts_with("wep_") && !l.starts_with("wpa=1") && !l.starts_with("wpa=3"), "[{}] legacy cipher line `{l}`", s.name);
            assert!(!l.starts_with("macaddr_acl=") || l == "macaddr_acl=0", "[{}] unexpected ACL line `{l}`", s.name);
        }
    }
    assert!(checked >= 2, "need at least two Wi-Fi samples, got {checked}");
}

#[test]
fn squid_listens_on_the_lan_address_and_ends_with_deny_all() {
    for s in samples() {
        let cfg = &s.cfg;
        let text = generate_squid_config(cfg).unwrap();
        let body: Vec<&str> = text.lines().map(str::trim).filter(|l| !l.is_empty() && !l.starts_with('#')).collect();
        let http_ports: Vec<&str> = body.iter().copied().filter(|l| l.starts_with("http_port ")).collect();
        assert_eq!(
            http_ports,
            vec![format!("http_port {}:{}", cfg.lan.ip_address, cfg.squid_proxy.port)],
            "[{}] squid http_port must be bound to the LAN address",
            s.name
        );
        assert!(!text.contains("0.0.0.0:") && !text.contains("https_port") && !text.contains("intercept"), "[{}] squid listens beyond the LAN address", s.name);
        assert_eq!(body.last().copied(), Some("http_access deny all"), "[{}] squid config must end with `http_access deny all`", s.name);
        let allows: Vec<&&str> = body.iter().filter(|l| l.starts_with("http_access allow")).collect();
        if cfg.squid_proxy.enabled {
            assert_eq!(allows, vec![&"http_access allow localnet authenticated"], "[{}] squid allow rules must require authentication", s.name);
            let deny_dst = body.iter().position(|l| *l == "http_access deny blocked_dst").unwrap_or(usize::MAX);
            let allow = body.iter().position(|l| l.starts_with("http_access allow")).unwrap();
            assert!(deny_dst < allow, "[{}] blocked_dst deny must precede the allow rule", s.name);
            let blocked = body.iter().find(|l| l.starts_with("acl blocked_dst dst ")).expect("blocked_dst acl");
            for net in ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "169.254.0.0/16", "100.64.0.0/10", "192.168.1.0/24"] {
                assert!(blocked.split_whitespace().any(|t| t == net), "[{}] blocked_dst misses {net}: {blocked}", s.name);
            }
            if cfg.wireguard.enabled {
                assert!(blocked.split_whitespace().any(|t| t == "10.8.0.0/24"), "[{}] blocked_dst misses the WireGuard subnet", s.name);
            }
            for seg in extras(cfg) {
                assert!(blocked.split_whitespace().any(|t| t == seg.cidr), "[{}] blocked_dst misses extra LAN {}", s.name, seg.cidr);
            }
        } else {
            assert!(allows.is_empty(), "[{}] disabled squid still has an allow rule", s.name);
        }
    }
}

// ───────────────────────── design facts (recorded, not guesses) ─────────────────────────

/// Wi-Fi clients are bridged into the LAN: hostapd joins `br-lan` and every
/// LAN-scoped firewall/DHCP/DNS rule is rendered against `br-lan`, so a Wi-Fi
/// client has exactly the LAN's trust. The raw radio interface never appears
/// in a firewall rule. hostapd's client isolation is not configured.
#[test]
fn design_fact_wifi_clients_are_in_the_lan_zone() {
    let s = samples().into_iter().find(|s| s.name == "wifi").unwrap();
    let nft = render(&s);
    let chains = parse_chains(&nft);
    let input = chains.iter().find(|c| c.name == "input").unwrap();
    let hostapd = generate_hostapd(&s.cfg).unwrap();
    let dnsmasq = generate_dnsmasq(&s.cfg).unwrap();
    assert!(hostapd.lines().any(|l| l == "bridge=br-lan"));
    for port in [8443, 22] {
        assert!(
            input.rules.iter().any(|r| is_accept(r) && names(&IIF, r) == vec!["br-lan".to_string()] && covers(&ports(&DPORT, r), port)),
            "Wi-Fi/LAN clients reach tcp/{port} through br-lan:\n{nft}"
        );
    }
    assert!(!nft.contains("wlan0"), "the radio interface must not be a separate firewall zone");
    assert!(!nft.contains("\"eth1\""), "with Wi-Fi on, the physical LAN port is a bridge member, not a rule interface");
    assert!(dnsmasq.lines().any(|l| l == "interface=br-lan"));
    let isolation = hostapd.lines().any(|l| l.starts_with("ap_isolate"));
    eprintln!("FACT: hostapd client isolation (ap_isolate) configured: {isolation}; bridge=br-lan; Wi-Fi clients share the LAN zone");
}

/// Management HTTPS is the opposite case to SSH: the packet policy scopes it by
/// interface only (the source gate is `routerd`'s per-request trusted_networks
/// check), so narrowing `trusted_networks` does not change the HTTPS accept.
/// SSH, which has no application gate, does follow `trusted_networks`.
#[test]
fn design_fact_https_is_interface_scoped_ssh_is_source_scoped() {
    let all = samples();
    let s = all.iter().find(|s| s.name == "ssh_trusted_single_host").unwrap();
    let chains = parse_chains(&render(s));
    let input = chains.iter().find(|c| c.name == "input").unwrap();
    let on = |port: i64| -> Vec<&String> { input.rules.iter().filter(|r| is_accept(r) && covers(&ports(&DPORT, r), port)).collect() };
    let https = on(8443);
    let ssh = on(22);
    assert_eq!(https.len(), 1, "{https:?}");
    assert!(!SADDR.is_match(https[0]), "HTTPS accept unexpectedly source-scoped: {}", https[0]);
    assert_eq!(ssh.len(), 1, "{ssh:?}");
    assert!(ssh[0].contains("ip saddr { 192.168.1.10/32 }"), "{}", ssh[0]);
    eprintln!("FACT: with trusted_networks=[192.168.1.10/32] -> HTTPS `{}`; SSH `{}`", https[0], ssh[0]);
}

/// hostapd renders no client isolation, WPS or TKIP; it offers WPA2-PSK and
/// WPA3-SAE (transition mode) with protected management frames optional.
#[test]
fn design_fact_hostapd_profile() {
    let s = samples().into_iter().find(|s| s.name == "wifi").unwrap();
    let text = generate_hostapd(&s.cfg).unwrap();
    let kv = |k: &str| text.lines().find_map(|l| l.strip_prefix(&format!("{k}=")).map(str::to_string));
    assert_eq!(kv("wpa_key_mgmt").as_deref(), Some("WPA-PSK SAE"));
    assert_eq!(kv("ieee80211w").as_deref(), Some("1"), "management-frame protection is optional (1), not required (2)");
    assert!(kv("ap_isolate").is_none(), "client isolation is not configured");
    eprintln!("FACT: hostapd wpa_key_mgmt={:?} ieee80211w={:?} ap_isolate={:?}", kv("wpa_key_mgmt"), kv("ieee80211w"), kv("ap_isolate"));
}
