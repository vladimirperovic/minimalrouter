//! DNS Filter exemptions: reserved devices that skip the network DNS
//! categories. dnsmasq blocks globally, so the router redirects their lookups
//! to the first upstream resolver and leaves them out of the DNS enforcement
//! presets.

use minimalrouter::config::{DNSFilterExemption, DeviceProfile, FirewallRule, StaticLease, SystemConfig, default_config};
use minimalrouter::goish::json::List;
use minimalrouter::services::{self, FirewallFlow};

const LAPTOP_IP: &str = "192.168.1.14";

fn lease(id: &str, name: &str, ip: &str) -> StaticLease {
    StaticLease {
        id: id.into(),
        hostname: name.into(),
        mac: format!("02:00:00:00:00:{:02x}", ip.rsplit('.').next().unwrap().parse::<u8>().unwrap()),
        ip_address: ip.into(),
    }
}

fn exempt(ip: &str, name: &str) -> DNSFilterExemption {
    DNSFilterExemption { ip_address: ip.into(), hostname: name.into() }
}

fn preset(id: &str, protocol: &str, port: i64) -> FirewallRule {
    FirewallRule {
        id: format!("preset-{id}-{protocol}"),
        preset_id: id.into(),
        name: format!("Block {id} (preset)"),
        action: "deny".into(),
        direction: "forward".into(),
        protocol: protocol.into(),
        dst_port: port,
        enabled: true,
        scope: "wan".into(),
        ..Default::default()
    }
}

/// A router with a laptop reservation, WAN up and both DNS enforcement presets.
fn enforced() -> SystemConfig {
    let mut cfg = default_config();
    cfg.wan.enabled = true;
    cfg.dhcp.static_leases = List::from(vec![lease("laptop", "Laptop", LAPTOP_IP), lease("pc", "Desktop", "192.168.1.13")]);
    cfg.firewall.custom_rules = List::from(vec![
        preset("dns", "udp", 53),
        preset("dns", "tcp", 53),
        preset("dot", "tcp", 853),
        preset("dot", "udp", 853),
        FirewallRule {
            id: "rdp".into(),
            name: "Block RDP".into(),
            action: "deny".into(),
            direction: "forward".into(),
            protocol: "tcp".into(),
            dst_port: 3389,
            enabled: true,
            scope: "wan".into(),
            ..Default::default()
        },
    ]);
    cfg
}

/// Whether any custom rule carries an exempt-device source exclusion (the LAN
/// anti-spoofing rule uses the same operator but has no counter).
fn skips_exempt_devices(text: &str) -> bool {
    text.lines().any(|line| line.contains("ip saddr != {") && line.contains("counter name"))
}

fn nft(cfg: &SystemConfig) -> String {
    services::generate_nftables(cfg).expect("nftables generates")
}

#[test]
fn no_exemption_leaves_the_ruleset_unchanged() {
    let text = nft(&enforced());
    assert!(!text.contains("DNS exemption"), "{text}");
    assert!(!text.contains("chain prerouting"), "no NAT chain without port forwards or exemptions");
    assert!(!skips_exempt_devices(&text), "{text}");
    assert!(text.contains("iifname \"eth1\" udp dport 53 oifname { eth0, ppp* } counter name"), "{text}");
}

#[test]
fn exempt_device_is_redirected_to_the_first_upstream() {
    let mut cfg = enforced();
    cfg.adguard.exempt_devices = List::from(vec![exempt(LAPTOP_IP, "Laptop")]);
    let text = nft(&cfg);
    assert!(text.contains("chain prerouting {\n    type nat hook prerouting priority dstnat; policy accept;"), "{text}");
    assert!(text.contains("iifname \"eth1\" ip saddr { 192.168.1.14 } ip daddr 192.168.1.1 udp dport 53 dnat ip to 1.1.1.1:53"), "{text}");
    assert!(text.contains("iifname \"eth1\" ip saddr { 192.168.1.14 } ip daddr 192.168.1.1 tcp dport 53 dnat ip to 1.1.1.1:53"), "{text}");
    // Only the router's own address is redirected, never other destinations.
    assert_eq!(text.matches("dnat ip to").count(), 2, "{text}");
}

#[test]
fn enforcement_presets_skip_the_exempt_device_but_other_rules_do_not() {
    let mut cfg = enforced();
    cfg.adguard.exempt_devices = List::from(vec![exempt("192.168.1.14", "Laptop"), exempt("192.168.1.13", "Desktop")]);
    let text = nft(&cfg);
    // Sorted, deterministic address list on all four presets.
    let skip = "ip saddr != { 192.168.1.13, 192.168.1.14 }";
    assert_eq!(text.matches(skip).count(), 4, "{text}");
    for (protocol, port) in [("udp", 53), ("tcp", 53), ("tcp", 853), ("udp", 853)] {
        assert!(text.contains(&format!("iifname \"eth1\" {skip} {protocol} dport {port} oifname {{ eth0, ppp* }} counter name")), "{protocol}/{port}\n{text}");
    }
    // A deny that is not a DNS enforcement rule stays authoritative for everyone.
    assert!(text.contains("iifname \"eth1\" tcp dport 3389 oifname { eth0, ppp* } counter name"), "{text}");
}

#[test]
fn a_per_device_deny_rule_for_dns_is_not_widened() {
    let mut cfg = enforced();
    let mut targeted = preset("dns", "udp", 53);
    targeted.id = "kid-dns".into();
    targeted.preset_id.clear();
    targeted.src_ip = "192.168.1.14".into();
    cfg.firewall.custom_rules.push(targeted);
    cfg.adguard.exempt_devices = List::from(vec![exempt(LAPTOP_IP, "Laptop")]);
    let text = nft(&cfg);
    assert!(text.contains("iifname \"eth1\" ip saddr 192.168.1.14 udp dport 53 oifname { eth0, ppp* } counter name"), "{text}");
}

#[test]
fn tunnel_port_forwards_and_the_exemption_share_one_prerouting_chain() {
    let mut cfg = enforced();
    cfg.wireguard.enabled = true;
    cfg.firewall.port_forwards = List::from(vec![minimalrouter::config::PortForwardRule {
        id: "pf".into(),
        name: "web".into(),
        protocol: "tcp".into(),
        external_port: 8080,
        internal_ip: "192.168.1.7".into(),
        internal_port: 80,
        enabled: true,
        ..Default::default()
    }]);
    cfg.adguard.exempt_devices = List::from(vec![exempt(LAPTOP_IP, "Laptop")]);
    let text = nft(&cfg);
    assert_eq!(text.matches("chain prerouting").count(), 1, "{text}");
    assert!(text.contains("dnat ip to 1.1.1.1:53") && text.contains("iifname \"wg0\" tcp dport 8080 dnat ip to 192.168.1.7:80"), "{text}");
}

#[test]
fn nothing_is_exempted_without_a_usable_upstream() {
    let mut cfg = enforced();
    cfg.adguard.exempt_devices = List::from(vec![exempt(LAPTOP_IP, "Laptop")]);
    cfg.dhcp.dns_servers = List::from(Vec::<String>::new());
    assert!(services::dns_exempt_addresses(&cfg).is_empty());
    let text = nft(&cfg);
    assert!(!text.contains("dnat ip to"), "{text}");
    assert!(!skips_exempt_devices(&text), "the filter stays authoritative: {text}");
}

#[test]
fn exemptions_are_validated() {
    let mut base = default_config();
    base.dhcp.static_leases = List::from(vec![lease("laptop", "Laptop", LAPTOP_IP), lease("pc", "Desktop", "192.168.1.13")]);
    assert!(base.validate().is_ok(), "baseline must be valid: {:?}", base.validate());
    let errors = |devices: Vec<DNSFilterExemption>| {
        let mut cfg = base.clone();
        cfg.adguard.exempt_devices = List::from(devices);
        cfg.validate().err().map(|e| e.to_string()).unwrap_or_default()
    };

    assert_eq!(errors(vec![exempt(LAPTOP_IP, "Laptop")]), "");
    assert_eq!(errors(vec![exempt("192.168.1.13", ""), exempt(LAPTOP_IP, "Laptop")]), "");
    assert!(errors(vec![exempt("192.168.1.99", "Guest")]).contains("needs a DHCP reservation"));
    assert!(errors(vec![exempt("192.168.1.1", "Router")]).contains("cannot be the router itself"));
    assert!(errors(vec![exempt("10.0.0.5", "Elsewhere")]).contains("valid address in the LAN subnet"));
    assert!(errors(vec![exempt("not-an-ip", "")]).contains("valid address in the LAN subnet"));
    assert!(errors(vec![exempt(LAPTOP_IP, "Laptop"), exempt(LAPTOP_IP, "Laptop again")]).contains("already exempt"));
    assert!(errors(vec![exempt(LAPTOP_IP, "bad\nname")]).contains("unsupported characters"));
}

#[test]
fn a_device_cannot_be_both_exempt_and_on_a_schedule() {
    let mut cfg = default_config();
    cfg.dhcp.static_leases = List::from(vec![lease("laptop", "Laptop", LAPTOP_IP)]);
    cfg.adguard.exempt_devices = List::from(vec![exempt(LAPTOP_IP, "Laptop")]);
    cfg.adguard.device_profiles = List::from(vec![DeviceProfile {
        id: "kids".into(),
        name: "Kids".into(),
        ip_addresses: List::from(vec![LAPTOP_IP.to_string()]),
        services: List::from(vec!["youtube".to_string()]),
        enabled: true,
        ..Default::default()
    }]);
    let message = cfg.validate().err().map(|e| e.to_string()).unwrap_or_default();
    assert!(message.contains("belongs to a device profile"), "{message}");
}

#[test]
fn at_most_32_devices() {
    let mut cfg = default_config();
    let leases: Vec<StaticLease> = (100..134).map(|n| lease(&format!("l{n}"), &format!("host{n}"), &format!("192.168.1.{n}"))).collect();
    cfg.adguard.exempt_devices = List::from(leases.iter().map(|l| exempt(&l.ip_address, &l.hostname)).collect::<Vec<_>>());
    cfg.dhcp.static_leases = List::from(leases);
    let message = cfg.validate().err().map(|e| e.to_string()).unwrap_or_default();
    assert!(message.contains("cannot contain more than 32 devices"), "{message}");
}

#[test]
fn the_default_configuration_does_not_serialize_the_field() {
    let json = serde_json::to_string(&default_config()).unwrap();
    assert!(!json.contains("exempt_devices"), "empty exemptions must not change saved configuration");
    let mut cfg = default_config();
    cfg.adguard.exempt_devices = List::from(vec![exempt(LAPTOP_IP, "Laptop")]);
    let round: SystemConfig = serde_json::from_str(&serde_json::to_string(&cfg).unwrap()).unwrap();
    assert_eq!(round.adguard.exempt_devices, cfg.adguard.exempt_devices);
}

#[test]
fn the_flow_explainer_matches_the_ruleset() {
    let mut cfg = enforced();
    let direct = |source: &str| FirewallFlow { source_ip: source.into(), destination_ip: "8.8.8.8".into(), protocol: "udp".into(), port: 53 };
    let verdict = |cfg: &SystemConfig, source: &str| services::explain_firewall_flow(cfg, &direct(source)).map_err(|(_, e)| e).expect("explains");

    // Without an exemption, direct DNS is denied by the preset for everyone.
    assert_eq!(verdict(&cfg, LAPTOP_IP).verdict, "deny");
    cfg.adguard.exempt_devices = List::from(vec![exempt(LAPTOP_IP, "Laptop")]);
    let exempted = verdict(&cfg, LAPTOP_IP);
    assert_eq!(exempted.verdict, "allow", "{}", exempted.reason);
    // Everyone else is still subject to the preset.
    let other = verdict(&cfg, "192.168.1.13");
    assert_eq!(other.verdict, "deny", "{}", other.reason);

    let to_router = FirewallFlow { source_ip: LAPTOP_IP.into(), destination_ip: "192.168.1.1".into(), protocol: "udp".into(), port: 53 };
    let router = services::explain_firewall_flow(&cfg, &to_router).map_err(|(_, e)| e).expect("explains");
    assert_eq!(router.verdict, "allow");
    assert!(router.reason.contains("redirected to the first upstream resolver"), "{}", router.reason);
}

#[test]
fn changes_are_described_for_review() {
    let before = enforced();
    let mut after = before.clone();
    after.adguard.exempt_devices = List::from(vec![exempt(LAPTOP_IP, "Laptop")]);
    let added = services::describe_network_changes(&before, &after);
    assert!(added.iter().any(|c| c == "DNS protection exemption added: Laptop · 192.168.1.14"), "{added:?}");
    let removed = services::describe_network_changes(&after, &before);
    assert!(removed.iter().any(|c| c == "DNS protection exemption removed: Laptop · 192.168.1.14"), "{removed:?}");
    assert!(services::describe_network_changes(&before, &before).is_empty());
}
