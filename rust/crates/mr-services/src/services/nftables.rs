use std::collections::HashSet;
use std::fmt::Write;

use super::device_profiles::{active_managed_services, write_device_profile_objects};
use super::firewall_counter::firewall_rule_counter;
use crate::config::{PortForwardRule, SystemConfig, firewall_rule_scope};
use crate::goish::errors::{Result, wrapf};
use crate::goish::net::{self, IPNet};

/// Per-host byte counters, read (never written) by routerd through two exact
/// allowlisted `nft -j list set` commands. The table is recreated on every
/// apply, so the collector treats any decrease as a reset.
pub const ACCOUNTING_SET_RX: &str = "acct_rx";
pub const ACCOUNTING_SET_TX: &str = "acct_tx";

const RESERVED_WAN_SOURCES: &str = "{ 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.0.0.0/24, 192.0.2.0/24, 192.168.0.0/16, 198.18.0.0/15, 198.51.100.0/24, 203.0.113.0/24, 224.0.0.0/3 }";

/// Literal IPv4 addresses of enabled WireGuard server peer endpoints. Used only
/// to keep the WireGuard UDP socket reachable; never a trust identity.
fn wg_peer_endpoint_nets(cfg: &SystemConfig) -> Vec<String> {
    if !cfg.wireguard.enabled {
        return Vec::new();
    }
    let mut out = Vec::new();
    let mut seen = HashSet::new();
    for peer in cfg.wireguard.peers.iter() {
        if !peer.enabled {
            continue;
        }
        let host = net::split_host_port(&peer.endpoint).map(|(h, _)| h).unwrap_or_else(|_| peer.endpoint.clone());
        let Some(ip) = net::parse_ip(&host) else { continue };
        if ip.to4().is_none() {
            continue;
        }
        let cidr = format!("{}/32", ip);
        if seen.insert(cidr.clone()) {
            out.push(cidr);
        }
    }
    out
}

/// The narrow exception needed when a configured peer endpoint is itself in a
/// source range the WAN anti-spoof policy normally drops. Limited to UDP on
/// the WireGuard listen port.
fn write_known_wg_peer_endpoint_input_rules(buf: &mut String, cfg: &SystemConfig) {
    let endpoints = wg_peer_endpoint_nets(cfg);
    if endpoints.is_empty() || !cfg.wireguard.enabled {
        return;
    }
    let sources = endpoints.join(", ");
    let port = cfg.wireguard.listen_port;
    buf.push_str("    # Known WireGuard peer endpoints: UDP socket only, never general trust\n");
    if !cfg.wan.interface.is_empty() {
        let wan = &cfg.wan.interface;
        let _ = writeln!(buf, "    iifname \"{wan}\" ip saddr {{ {sources} }} ct state invalid drop");
        let _ = writeln!(
            buf,
            "    iifname \"{wan}\" ip saddr {{ {sources} }} udp dport {port} ct state new meter wg_known_wan_rate {{ ip saddr timeout 10s limit rate over 20/second burst 40 packets }} drop"
        );
        let _ = writeln!(buf, "    iifname \"{wan}\" ip saddr {{ {sources} }} udp dport {port} accept");
    }
    let _ = writeln!(buf, "    iifname \"ppp*\" ip saddr {{ {sources} }} ct state invalid drop");
    let _ = writeln!(
        buf,
        "    iifname \"ppp*\" ip saddr {{ {sources} }} udp dport {port} ct state new meter wg_known_ppp_rate {{ ip saddr timeout 10s limit rate over 20/second burst 40 packets }} drop"
    );
    let _ = writeln!(buf, "    iifname \"ppp*\" ip saddr {{ {sources} }} udp dport {port} accept\n");
}

/// The IPv4 broadcast address of a CIDR, or "" when not a valid IPv4 network.
pub(crate) fn lan_broadcast_address(cidr: &str) -> String {
    net::parse_cidr(cidr).and_then(|(_, n)| n.ip.to4().and_then(|_| n.ipv4_broadcast())).map(|b| b.to_string()).unwrap_or_default()
}

fn write_custom_rules(buf: &mut String, cfg: &SystemConfig, direction: &str, action: &str) {
    for rule in cfg.firewall.custom_rules.iter() {
        if !rule.enabled || rule.direction != direction || rule.action != action {
            continue;
        }
        let mut matches = vec![format!("iifname \"{}\"", cfg.lan.interface)];
        if !rule.src_ip.is_empty() {
            matches.push(format!("ip saddr {}", rule.src_ip));
        }
        // nft needs the network-layer qualifier for ICMP.
        if rule.protocol == "icmp" {
            matches.push("ip protocol icmp".into());
        } else if rule.protocol != "any" {
            matches.push(rule.protocol.clone());
        }
        if rule.protocol == "tcp" || rule.protocol == "udp" {
            matches.push(format!("dport {}", rule.dst_port));
        }
        if direction == "forward" && firewall_rule_scope(rule) == "wan" {
            // Custom forward allows are strictly LAN -> WAN egress.
            if !cfg.wan.enabled {
                continue;
            }
            matches.push(format!("oifname {{ {}, ppp* }}", cfg.wan.interface));
        }
        let verdict = if action == "allow" { "accept" } else { "drop" };
        let _ = writeln!(buf, "    # Custom LAN {} {} rule: {}", direction, action, sanitize_comment(&rule.name));
        let _ = writeln!(buf, "    {} counter name {} {}", matches.join(" "), firewall_rule_counter(rule), verdict);
    }
}

/// Strips line breaks and control characters from user text before it is
/// embedded in generated firewall comments.
pub(crate) fn sanitize_comment(value: &str) -> String {
    let value = value.replace(['\r', '\n', '\t'], " ");
    if value.chars().any(|r| (r as u32) < 0x20 && r != ' ') {
        return value.chars().map(|c| if (c as u32) < 0x20 && c != ' ' { '?' } else { c }).collect();
    }
    value
}

/// Maps an AllowFrom CIDR to the router interface that carries it.
fn extra_lan_source_interface(cfg: &SystemConfig, src_cidr: &str) -> String {
    let Some((_, src_net)) = net::parse_cidr(src_cidr) else {
        return String::new();
    };
    let src_ones = src_net.mask.size().0;
    if let Some((_, lan_net)) = net::parse_cidr(&cfg.lan.cidr) {
        if lan_net.contains(&src_net.ip) && src_ones >= lan_net.mask.size().0 {
            return cfg.runtime_lan_interface().to_string();
        }
    }
    if cfg.wireguard.enabled {
        if let Some((_, wg_net)) = net::parse_cidr(&cfg.wireguard.address) {
            if wg_net.contains(&src_net.ip) && src_ones >= wg_net.mask.size().0 {
                return cfg.wireguard.interface.clone();
            }
        }
    }
    String::new()
}

fn wg_client_interface(cfg: &SystemConfig) -> &str {
    if cfg.wg_client.interface.is_empty() { "wg1" } else { &cfg.wg_client.interface }
}

/// Confines the outbound tunnel (wg1) to established traffic.
fn write_wg_client_input_rules(buf: &mut String, cfg: &SystemConfig) {
    if !cfg.wg_client.enabled {
        return;
    }
    let iface = wg_client_interface(cfg);
    let allowed: Vec<String> = cfg
        .wg_client
        .allowed_ips
        .iter()
        .filter_map(|entry| net::parse_cidr(entry.trim()))
        .filter(|(_, n)| n.ip.to4().is_some())
        .map(|(_, n)| n.to_string())
        .collect();
    let spoof = if allowed.is_empty() { "drop".to_string() } else { format!("ip saddr != {{ {} }} drop", allowed.join(", ")) };
    let _ = writeln!(buf, "    # WireGuard client {}: established-only (remote site cannot initiate)", iface);
    let _ = writeln!(buf, "    iifname \"{}\" {}", iface, spoof);
    let _ = writeln!(buf, "    iifname \"{}\" ct state new drop\n", iface);
}

/// Trusted home networks (LAN, wg0) may dial out through the tunnel.
fn write_wg_client_forward_rules(buf: &mut String, cfg: &SystemConfig) {
    if !cfg.wg_client.enabled {
        return;
    }
    let iface = wg_client_interface(cfg);
    let _ = writeln!(buf, "    # WireGuard client {}: trusted home networks may dial out, remote site may only respond", iface);
    let mut sources = vec![cfg.lan.interface.as_str()];
    if cfg.wireguard.enabled && !cfg.wireguard.interface.is_empty() {
        sources.push(&cfg.wireguard.interface);
    }
    for src in sources {
        let _ = writeln!(buf, "    iifname \"{}\" oifname \"{}\" accept", src, iface);
    }
    let _ = writeln!(buf, "    iifname \"{}\" ct state new drop\n", iface);
}

fn write_wg_client_output_rules(buf: &mut String, cfg: &SystemConfig) {
    if !cfg.wg_client.enabled {
        return;
    }
    let Ok((_, port)) = net::split_host_port(&cfg.wg_client.endpoint) else {
        return;
    };
    if port.is_empty() {
        return;
    }
    if !cfg.wan.interface.is_empty() {
        let _ = writeln!(buf, "    oifname \"{}\" udp dport {} accept", cfg.wan.interface, port);
    }
    let _ = writeln!(buf, "    oifname \"ppp*\" udp dport {} accept", port);
}

fn write_wg_client_masquerade(buf: &mut String, cfg: &SystemConfig) {
    if cfg.wg_client.enabled {
        let _ = writeln!(buf, "    oifname \"{}\" masquerade", wg_client_interface(cfg));
    }
}

/// Protects the router itself from isolated segments.
fn write_extra_lan_input_rules(buf: &mut String, cfg: &SystemConfig) {
    for lan in cfg.firewall.extra_lans.iter() {
        if !lan.enabled || lan.interface.is_empty() || lan.cidr.is_empty() {
            continue;
        }
        let _ = writeln!(buf, "    # Extra LAN {} ({}): isolated segment, router hosts no services here", lan.interface, sanitize_comment(&lan.name));
        let _ = writeln!(buf, "    iifname \"{}\" ip saddr != {{ {} }} drop", lan.interface, lan.cidr);
        let _ = writeln!(buf, "    iifname \"{}\" ip protocol icmp accept", lan.interface);
    }
}

/// Allows exactly the configured AllowFrom networks toward the single service
/// on each segment.
fn write_extra_lan_forward_rules(buf: &mut String, cfg: &SystemConfig) {
    for lan in cfg.firewall.extra_lans.iter() {
        if !lan.enabled || lan.interface.is_empty() || lan.cidr.is_empty() || lan.dst_ip.is_empty() {
            continue;
        }
        let proto = if lan.protocol.is_empty() { "tcp" } else { &lan.protocol };
        let _ = writeln!(
            buf,
            "    # Extra LAN {} ({}): isolated segment, only {} sources reach {}:{} on {}",
            lan.interface,
            sanitize_comment(&lan.name),
            lan.cidr,
            lan.dst_ip,
            lan.dst_port,
            lan.interface
        );
        let _ = writeln!(buf, "    iifname \"{}\" ip saddr != {} drop", lan.interface, lan.cidr);
        for src in lan.allow_from.iter() {
            let iface = extra_lan_source_interface(cfg, src);
            if iface.is_empty() {
                continue;
            }
            let _ = writeln!(
                buf,
                "    iifname \"{}\" ip saddr {} ip daddr {} {} dport {} oifname \"{}\" accept",
                iface, src, lan.dst_ip, proto, lan.dst_port, lan.interface
            );
        }
    }
}

/// Bounded, timed-out per-host byte counters.
fn write_accounting_sets(buf: &mut String, cfg: &SystemConfig) {
    if !cfg.accounting.enabled || cfg.lan.interface.is_empty() {
        return;
    }
    for name in [ACCOUNTING_SET_RX, ACCOUNTING_SET_TX] {
        let _ = writeln!(buf, "  set {} {{", name);
        buf.push_str("    type ipv4_addr\n    size 512\n    flags dynamic,timeout\n    timeout 7d\n    counter\n  }\n\n");
    }
}

fn write_accounting_rules(buf: &mut String, cfg: &SystemConfig) {
    if !cfg.accounting.enabled || cfg.lan.interface.is_empty() {
        return;
    }
    buf.push_str("    # Per-device byte accounting (counters only, no verdict)\n");
    let _ = writeln!(buf, "    iifname \"{}\" update @{} {{ ip saddr }}", cfg.lan.interface, ACCOUNTING_SET_TX);
    let _ = writeln!(buf, "    oifname \"{}\" update @{} {{ ip daddr }}\n", cfg.lan.interface, ACCOUNTING_SET_RX);
}

/// Port forwards reachable over the WireGuard management tunnel only.
fn enabled_tunnel_port_forwards(cfg: &SystemConfig) -> Vec<&PortForwardRule> {
    if !cfg.wireguard.enabled {
        return Vec::new();
    }
    cfg.firewall.port_forwards.iter().filter(|r| r.enabled).collect()
}

fn wg_server_interface(cfg: &SystemConfig) -> &str {
    if cfg.wireguard.interface.is_empty() { "wg0" } else { &cfg.wireguard.interface }
}

/// The prerouting DNAT chain; every rule is bound to the WireGuard interface.
fn write_tunnel_port_forwards(buf: &mut String, cfg: &SystemConfig) {
    let rules = enabled_tunnel_port_forwards(cfg);
    if rules.is_empty() {
        return;
    }
    let wg = wg_server_interface(cfg);
    buf.push_str("  chain prerouting {\n");
    buf.push_str("    type nat hook prerouting priority dstnat; policy accept;\n\n");
    buf.push_str("    # Port forwards are reachable only over the WireGuard tunnel, never the WAN.\n");
    for rule in rules {
        for protocol in port_forward_protocols(&rule.protocol) {
            let _ = writeln!(buf, "    iifname \"{}\" {} dport {} dnat ip to {}:{}", wg, protocol, rule.external_port, rule.internal_ip, rule.internal_port);
        }
    }
    buf.push_str("  }\n\n");
}

/// DNAT rewrites the destination before forward runs, so the accept matches
/// the translated address.
fn write_tunnel_port_forward_forward(buf: &mut String, cfg: &SystemConfig) {
    let rules = enabled_tunnel_port_forwards(cfg);
    if rules.is_empty() {
        return;
    }
    let wg = wg_server_interface(cfg);
    buf.push_str("    # Tunnel port forwards: allow the DNAT-translated destination\n");
    for rule in rules {
        for protocol in port_forward_protocols(&rule.protocol) {
            let _ = writeln!(
                buf,
                "    iifname \"{}\" oifname \"{}\" ip daddr {} {} dport {} ct state new accept",
                wg, cfg.lan.interface, rule.internal_ip, protocol, rule.internal_port
            );
        }
    }
    buf.push('\n');
}

fn port_forward_protocols(protocol: &str) -> &'static [&'static str] {
    match crate::goish::strings::to_lower(protocol.trim()).as_str() {
        "both" => &["tcp", "udp"],
        "udp" => &["udp"],
        _ => &["tcp"],
    }
}

fn opt_net_string(network: Option<IPNet>) -> String {
    network.map(|n| n.to_string()).unwrap_or_else(|| "<nil>".into())
}

/// Renders an atomic, deterministic nftables configuration.
pub fn generate_nftables(cfg: &SystemConfig) -> Result<String> {
    // Scenario-level policy is enforced in the generator as a second trust
    // boundary, so the privileged helper cannot install a lockout rule.
    cfg.validate_scenario_safety().map_err(|e| wrapf("scenario safety", e))?;

    let mut runtime = cfg.clone();
    runtime.lan.interface = cfg.runtime_lan_interface().to_string();
    let cfg = &runtime;
    let lan = &cfg.lan.interface;
    let wan = &cfg.wan.interface;

    let mut buf = String::with_capacity(16 * 1024);
    buf.push_str("# Generated by Minimal Router OS — do not edit manually\n");
    buf.push_str("# Table: inet minimalrouter\n\n");
    buf.push_str("table inet minimalrouter {\n");
    let _ = writeln!(buf, "  counter mr_revision_{} {{ }}", cfg.revision);
    let mut seen_rule_counters = HashSet::new();
    for rule in cfg.firewall.custom_rules.iter() {
        let name = firewall_rule_counter(rule);
        let emitted = rule.direction != "forward" || firewall_rule_scope(rule) != "wan" || cfg.wan.enabled;
        if rule.enabled && emitted && !seen_rule_counters.contains(&name) {
            let _ = writeln!(buf, "  counter {} {{ }}", name);
            seen_rule_counters.insert(name);
        }
    }
    write_device_profile_objects(&mut buf, cfg);
    write_accounting_sets(&mut buf, cfg);
    // Observation chains have no verdict rules; the default-deny chains at
    // priority 0 remain the sole policy enforcement point.
    buf.push_str("  counter mr_seen { }\n  counter mr_accepted { }\n");
    for hook in ["input", "forward"] {
        let _ = writeln!(buf, "  chain observe_{hook}_before {{ type filter hook {hook} priority -1; policy accept; counter name mr_seen; }}");
        let _ = writeln!(buf, "  chain observe_{hook}_after {{ type filter hook {hook} priority 1; policy accept; counter name mr_accepted; }}");
    }

    // Input chain.
    buf.push_str("  chain input {\n");
    buf.push_str("    type filter hook input priority filter; policy drop;\n\n");
    buf.push_str("    # Allow loopback\n");
    buf.push_str("    iifname \"lo\" accept\n\n");
    if !wan.is_empty() {
        write_known_wg_peer_endpoint_input_rules(&mut buf, cfg);
        buf.push_str("    # Drop spoofed/reserved WAN sources before connection tracking accepts\n");
        write_wan_antispoof(&mut buf, wan);
    }
    if !lan.is_empty() {
        let _ = write!(
            buf,
            "    # LAN source anti-spoofing (0.0.0.0 is needed for DHCP discovery)\n    iifname \"{}\" ip saddr != {{ 0.0.0.0, {} }} drop\n\n",
            lan, cfg.lan.cidr
        );
    }
    buf.push_str("    # Reject invalid packets before service accepts\n");
    buf.push_str("    ct state invalid drop\n");
    write_custom_rules(&mut buf, cfg, "input", "deny");
    buf.push_str("    # Allow established and related connections\n");
    buf.push_str("    ct state established,related accept\n\n");

    if cfg.wireguard.enabled {
        let wg_network = opt_net_string(net::parse_cidr(&cfg.wireguard.address).map(|(_, n)| n));
        let wg = &cfg.wireguard.interface;
        let port = cfg.wireguard.listen_port;
        buf.push_str("    # WireGuard endpoint with a per-source flood guard\n");
        if !wan.is_empty() {
            let _ = writeln!(
                buf,
                "    iifname \"{wan}\" udp dport {port} ct state new meter wg_wan_rate {{ ip saddr timeout 10s limit rate over 20/second burst 40 packets }} drop"
            );
            let _ = writeln!(buf, "    iifname \"{wan}\" udp dport {port} accept");
        }
        let _ = writeln!(
            buf,
            "    iifname \"ppp*\" udp dport {port} ct state new meter wg_ppp_rate {{ ip saddr timeout 10s limit rate over 20/second burst 40 packets }} drop"
        );
        let _ = writeln!(buf, "    iifname \"ppp*\" udp dport {port} accept");
        buf.push_str("    # Authenticated tunnel services\n");
        let _ = writeln!(buf, "    iifname \"{wg}\" ip saddr != {wg_network} drop");
        let _ = writeln!(buf, "    iifname \"{wg}\" tcp dport {} accept", cfg.system.https_port);
        let _ = writeln!(buf, "    iifname \"{wg}\" tcp dport 22 accept");
        let _ = writeln!(buf, "    iifname \"{wg}\" udp dport 53 accept");
        let _ = writeln!(buf, "    iifname \"{wg}\" tcp dport 53 accept\n");
    }

    write_wg_client_input_rules(&mut buf, cfg);

    if cfg.squid_proxy.enabled {
        let port = if cfg.squid_proxy.port == 0 { 3128 } else { cfg.squid_proxy.port };
        if !lan.is_empty() {
            let _ = writeln!(buf, "    # Allow Squid Proxy from LAN ({}, port {})", lan, port);
            let _ = writeln!(buf, "    iifname \"{}\" tcp dport {} accept\n", lan, port);
        }
    }

    if !lan.is_empty() {
        let _ = writeln!(buf, "    # Allow essential network services from LAN ({}) only", lan);
        if cfg.system.management_access != "wireguard_only" {
            let _ = writeln!(buf, "    iifname \"{}\" tcp dport {} ct state new limit rate over 30/second burst 60 packets drop", lan, cfg.system.https_port);
            let _ = writeln!(buf, "    iifname \"{}\" tcp dport {} accept", lan, cfg.system.https_port);
            let _ = writeln!(buf, "    iifname \"{}\" tcp dport 22 accept", lan);
        } else {
            buf.push_str("    # Management HTTPS is intentionally unavailable on LAN; use WireGuard\n");
        }
        let _ = writeln!(buf, "    iifname \"{}\" udp dport {{ 53, 67 }} accept", lan);
        let _ = writeln!(buf, "    iifname \"{}\" tcp dport 53 accept", lan);
        let _ = writeln!(buf, "    iifname \"{}\" ip protocol icmp accept\n", lan);
    }
    write_extra_lan_input_rules(&mut buf, cfg);
    if cfg.wireguard.enabled {
        let wg_network = opt_net_string(net::parse_cidr(&cfg.wireguard.address).map(|(_, n)| n));
        let _ = writeln!(buf, "    iifname \"{}\" ip saddr != {} drop\n", cfg.wireguard.interface, wg_network);
    }
    write_custom_rules(&mut buf, cfg, "input", "allow");
    buf.push_str("    # Reject WAN unsolicited input\n");
    buf.push_str("    drop\n");
    buf.push_str("  }\n\n");

    // Forward chain.
    buf.push_str("  chain forward {\n");
    buf.push_str("    type filter hook forward priority filter; policy drop;\n\n");
    if !wan.is_empty() {
        // A configured WireGuard peer endpoint is never a forward trust identity.
        buf.push_str("    # WAN anti-spoofing precedes state acceptance\n");
        write_wan_antispoof(&mut buf, wan);
    }
    if !lan.is_empty() {
        let _ = writeln!(buf, "    iifname \"{}\" ip saddr != {} drop\n", lan, cfg.lan.cidr);
    }
    buf.push_str("    # Reject invalid before established state\n");
    buf.push_str("    ct state invalid drop\n");
    if !active_managed_services(cfg).is_empty() {
        buf.push_str("    # Device schedules run before established acceptance so expired streams are cut\n");
        buf.push_str("    jump device_profiles\n");
    }
    write_custom_rules(&mut buf, cfg, "forward", "deny");

    if cfg.squid_proxy.enabled && !cfg.squid_proxy.restricted_ips.is_empty() {
        buf.push_str("    # Block direct WAN traffic for Restricted IP Alias (Must use Squid Proxy)\n");
        for item in cfg.squid_proxy.restricted_ips.iter() {
            let ip = item.ip_address.trim();
            if !ip.is_empty() && item.enabled {
                let _ = writeln!(buf, "    ip saddr {} drop", ip);
            }
        }
        buf.push('\n');
    }

    // Counters must precede every accept: accept is terminal for the chain.
    write_accounting_rules(&mut buf, cfg);
    buf.push_str("    # Allow established/related\n");
    buf.push_str("    ct state established,related accept\n\n");
    buf.push_str("    # TCP MSS clamping prevents PPPoE fragmentation stalls\n");
    buf.push_str("    tcp flags syn tcp option maxseg size set rt mtu\n\n");
    write_custom_rules(&mut buf, cfg, "forward", "allow");
    write_extra_lan_forward_rules(&mut buf, cfg);

    if cfg.wan.enabled && !lan.is_empty() {
        let _ = writeln!(buf, "    # Allow LAN ({}) out to WAN", lan);
        let _ = writeln!(buf, "    iifname \"{}\" oifname \"{}\" accept", lan, wan);
        let _ = writeln!(buf, "    iifname \"{}\" oifname \"ppp*\" accept\n", lan);
    }
    if cfg.wireguard.enabled {
        let wg = &cfg.wireguard.interface;
        buf.push_str("    # Authenticated WireGuard clients may reach LAN and optional full-tunnel WAN\n");
        let _ = writeln!(buf, "    iifname \"{}\" oifname \"{}\" accept", wg, lan);
        if cfg.wan.enabled {
            let _ = writeln!(buf, "    iifname \"{}\" oifname \"{}\" accept", wg, wan);
            let _ = writeln!(buf, "    iifname \"{}\" oifname \"ppp*\" accept", wg);
        }
        buf.push('\n');
    }
    write_tunnel_port_forward_forward(&mut buf, cfg);
    write_wg_client_forward_rules(&mut buf, cfg);
    buf.push_str("  }\n\n");

    // Output chain.
    buf.push_str("  chain output {\n");
    buf.push_str("    type filter hook output priority filter; policy drop;\n\n");
    buf.push_str("    # The appliance itself has a narrow egress allowlist\n");
    buf.push_str("    oifname \"lo\" accept\n");
    if !wan.is_empty() {
        buf.push_str("    # Never leak source addresses that are not assigned to the appliance itself.\n");
        buf.push_str("    # fib-based (rather than a static private-range list) so an ISP-assigned\n");
        buf.push_str("    # private or CGNAT WAN address (10.x, 100.64.0.0/10) remains routable.\n");
        buf.push_str("    # NOTE: fib saddr . iif oif is not supported in the output hook (kernel\n");
        buf.push_str("    # rejects it), hence fib saddr type != local here; forward chain uses\n");
        buf.push_str("    # fib saddr . iif oif which is valid there.\n");
        let _ = writeln!(buf, "    oifname \"{}\" fib saddr type != local drop", wan);
        buf.push_str("    oifname \"ppp*\" fib saddr type != local drop\n");
    }
    buf.push_str("    ct state invalid drop\n");

    // A pre-existing Squid connection to a private segment must be cut when
    // proxy isolation changes; these denies precede established acceptance.
    if cfg.squid_proxy.enabled {
        if !lan.is_empty() {
            let _ = writeln!(buf, "    meta skuid squid oifname \"{}\" tcp sport {} accept", lan, cfg.squid_proxy.port);
            let _ = writeln!(buf, "    meta skuid squid oifname \"{}\" drop", lan);
        }
        if cfg.wireguard.enabled && !cfg.wireguard.interface.is_empty() {
            let _ = writeln!(buf, "    meta skuid squid oifname \"{}\" drop", cfg.wireguard.interface);
        }
        if cfg.wg_client.enabled {
            let _ = writeln!(buf, "    meta skuid squid oifname \"{}\" drop", wg_client_interface(cfg));
        }
        for extra in cfg.firewall.extra_lans.iter() {
            if extra.enabled && !extra.interface.is_empty() {
                let _ = writeln!(buf, "    meta skuid squid oifname \"{}\" drop", extra.interface);
            }
        }
    }

    buf.push_str("    ct state established,related accept\n");
    let _ = writeln!(buf, "    oifname \"{}\" udp sport {{ 53, 67 }} accept", lan);
    if cfg.system.management_access == "wireguard_only" {
        let _ = writeln!(buf, "    oifname \"{}\" tcp sport 53 accept", lan);
    } else {
        let _ = writeln!(buf, "    oifname \"{}\" tcp sport {{ 53, {} }} accept", lan, cfg.system.https_port);
    }
    if cfg.wireguard.enabled {
        let wg = &cfg.wireguard.interface;
        let _ = writeln!(buf, "    oifname \"{}\" udp sport {{ 53, {} }} accept", wg, cfg.wireguard.listen_port);
        let _ = writeln!(buf, "    oifname \"{}\" tcp sport {{ 53, {} }} accept", wg, cfg.system.https_port);
    }
    write_wg_client_output_rules(&mut buf, cfg);
    if !cfg.dhcp.dns_servers.is_empty() {
        let servers = cfg.dhcp.dns_servers.join(", ");
        let _ = writeln!(buf, "    ip daddr {{ {} }} udp dport 53 accept", servers);
        let _ = writeln!(buf, "    ip daddr {{ {} }} tcp dport 53 accept", servers);
    }
    buf.push_str("    udp dport 123 accept\n");
    buf.push_str("    ip protocol icmp accept\n\n");
    buf.push_str("    # The unprivileged management daemon may use HTTPS only through WAN paths\n");
    if !wan.is_empty() {
        let _ = writeln!(buf, "    meta skuid routerd oifname \"{}\" tcp dport 443 accept", wan);
    }
    buf.push_str("    meta skuid routerd oifname \"ppp*\" tcp dport 443 accept\n");
    if !lan.is_empty() {
        let broadcast = lan_broadcast_address(&cfg.lan.cidr);
        if !broadcast.is_empty() {
            buf.push_str("    # Wake-on-LAN magic packets may only leave via the local LAN segment\n");
            let _ = writeln!(buf, "    meta skuid routerd oifname \"{}\" ip daddr {} udp dport 9 accept", lan, broadcast);
        }
    }
    if cfg.cloudflare.ddns_enabled {
        buf.push_str("    # DDNS HTTPS is confined to WAN paths\n");
        if !wan.is_empty() {
            let _ = writeln!(buf, "    meta skuid root oifname \"{}\" tcp dport 443 accept", wan);
            let _ = writeln!(buf, "    meta skuid inadyn oifname \"{}\" tcp dport 443 accept", wan);
        }
        buf.push_str("    meta skuid root oifname \"ppp*\" tcp dport 443 accept\n");
        buf.push_str("    meta skuid inadyn oifname \"ppp*\" tcp dport 443 accept\n");
    }
    if cfg.squid_proxy.enabled {
        buf.push_str("    # Squid is an Internet-only egress proxy; no private/tunnel interface is a valid destination\n");
        if !wan.is_empty() {
            let _ = writeln!(buf, "    meta skuid squid oifname \"{}\" tcp dport {{ 80, 443 }} accept", wan);
        }
        buf.push_str("    meta skuid squid oifname \"ppp*\" tcp dport { 80, 443 } accept\n");
    }
    buf.push_str("    counter drop\n");
    buf.push_str("  }\n\n");

    write_tunnel_port_forwards(&mut buf, cfg);

    // Postrouting (masquerade NAT).
    buf.push_str("  chain postrouting {\n");
    buf.push_str("    type nat hook postrouting priority srcnat; policy accept;\n\n");
    buf.push_str("    # Masquerade outgoing LAN traffic on WAN interfaces (ppp0 / ethernet WAN)\n");
    if cfg.wan.enabled {
        buf.push_str("    oifname \"ppp*\" masquerade\n");
        let _ = writeln!(buf, "    oifname \"{}\" masquerade", wan);
    }
    write_wg_client_masquerade(&mut buf, cfg);
    buf.push_str("  }\n");
    buf.push_str("}\n");
    Ok(buf)
}

fn write_wan_antispoof(buf: &mut String, wan: &str) {
    let _ = writeln!(buf, "    iifname \"{}\" ip saddr {} drop", wan, RESERVED_WAN_SOURCES);
    let _ = writeln!(buf, "    iifname \"{}\" fib saddr . iif oif missing drop", wan);
    let _ = writeln!(buf, "    iifname \"{}\" meta nfproto ipv6 drop", wan);
    let _ = writeln!(buf, "    iifname \"ppp*\" ip saddr {} drop", RESERVED_WAN_SOURCES);
    buf.push_str("    iifname \"ppp*\" fib saddr . iif oif missing drop\n");
    buf.push_str("    iifname \"ppp*\" meta nfproto ipv6 drop\n\n");
}
