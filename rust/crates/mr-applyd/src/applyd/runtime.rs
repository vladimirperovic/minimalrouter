//! Generating, preflighting, installing, activating, verifying and rolling back
//! the runtime artifacts. Every external command is a fixed argument vector.

use std::collections::{BTreeMap, HashSet};
use std::time::{Duration, Instant};

use super::dnswire;
use super::state::{INTERFACE_NAME_PATTERN, clear_pending_confirmation};
use super::sys::*;
use crate::apply::{MAX_REQUEST_BYTES, OperationType, op};
use crate::config::{SystemConfig, WGClientConfig, WIFI_BRIDGE_INTERFACE, WireGuardConfig, WireGuardPeer};
use crate::errorf;
use crate::goish::errors::{Result, wrapf};
use crate::goish::net;
use crate::services;

pub(crate) const NFT_RUNTIME_PATH: &str = "/run/minimalrouter/nftables.nft";
pub(crate) const WIREGUARD_CONFIG_PATH: &str = "/run/minimalrouter/wg0.conf";
pub(crate) const WIREGUARD_RUNTIME_PATH: &str = "/run/minimalrouter/wg0.runtime.conf";
/// The outbound tunnel (wg1) peer config, activated without wg-quick.
pub(crate) const WIREGUARD_CLIENT_RUNTIME_PATH: &str = "/run/minimalrouter/wg1.runtime.conf";

#[derive(Clone, Debug)]
pub(crate) struct Artifact {
    pub path: &'static str,
    pub data: Vec<u8>,
    pub mode: u32,
}

pub(crate) type Artifacts = BTreeMap<&'static str, Artifact>;

#[derive(Clone, Debug)]
pub(crate) struct PreviousFile {
    pub path: String,
    pub data: Vec<u8>,
    pub mode: u32,
    pub existed: bool,
}

/// The canonical write order for both apply and startup reconcile, so a
/// reboot never restores a partial runtime.
pub(crate) const RESTORE_ARTIFACTS: [&str; 17] = [
    "pppoe",
    "chap",
    "pap",
    "dnsmasq",
    "adblock",
    "qos",
    "cf-ddns",
    "cf-tunnel",
    "doh-proxy",
    "hostapd",
    "wireguard",
    "wireguard-runtime",
    "wireguard-client-runtime",
    "squid",
    "squid-passwd",
    "resolv-conf",
    "nftables",
];

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) struct VerificationPlan {
    pub wan: bool,
    pub wireguard: bool,
    pub extra_lan: bool,
    pub wifi: bool,
    pub ddns: bool,
    pub squid: bool,
    pub wg_client: bool,
}

pub(crate) fn verification_plan(operation: &OperationType, previous: Option<&SystemConfig>, candidate: &SystemConfig) -> VerificationPlan {
    let mut plan = VerificationPlan { wireguard: candidate.wireguard.enabled && candidate.system.management_access == "wireguard_only", ..Default::default() };
    if *operation == op::reconcile() || *operation == op::commit_confirmed() || *operation == op::confirm() {
        return plan;
    }
    let Some(previous) = previous else {
        plan.wan = candidate.wan.enabled;
        plan.wireguard = candidate.wireguard.enabled;
        plan.extra_lan = !candidate.firewall.extra_lans.is_empty();
        plan.wifi = candidate.wifi.enabled;
        plan.ddns = candidate.cloudflare.ddns_enabled;
        plan.squid = candidate.squid_proxy.enabled;
        plan.wg_client = candidate.wg_client.enabled;
        return plan;
    };
    plan.wan = candidate.wan.enabled && candidate.wan != previous.wan;
    plan.wireguard = plan.wireguard || candidate.wireguard != previous.wireguard;
    plan.extra_lan = candidate.firewall.extra_lans != previous.firewall.extra_lans;
    plan.wifi = candidate.wifi != previous.wifi;
    plan.ddns = candidate.cloudflare != previous.cloudflare;
    plan.squid = candidate.squid_proxy != previous.squid_proxy;
    plan.wg_client = candidate.wg_client != previous.wg_client;
    plan
}

pub(crate) fn reject_unimplemented_features(cfg: &SystemConfig) -> Result<()> {
    if cfg.cloudflare.tunnel_enabled {
        return Err(errorf!("Cloudflare Tunnel is unavailable because WireGuard is the only allowed remote-entry path"));
    }
    if cfg.dhcp.dns_enabled {
        return Err(errorf!("DNS-over-HTTPS has no verified Alpine 3.22 runtime adapter"));
    }
    if !cfg.adguard.filter_devices.is_empty() {
        return Err(errorf!("per-device DNS filtering is unsupported because dnsmasq address rules are global"));
    }
    Ok(())
}

pub(crate) fn generate_artifacts(cfg: &SystemConfig) -> Result<Artifacts> {
    let nft = services::generate_nftables(cfg)?;
    let pppoe = services::generate_pppoe(cfg)?;
    let dnsmasq = services::generate_dnsmasq(cfg)?;
    let hostapd = services::generate_hostapd(cfg)?;
    let wireguard = services::generate_wireguard(&cfg.wireguard)?;
    let wireguard_runtime = services::generate_wireguard_runtime(&cfg.wireguard)?;
    let wireguard_client_runtime = if cfg.wg_client.enabled {
        services::generate_wireguard_client_runtime(&cfg.wg_client)?.into_bytes()
    } else {
        b"# WireGuard client disabled\n".to_vec()
    };
    let squid_config = services::generate_squid_config(cfg)?;
    let squid_password = if cfg.squid_proxy.enabled {
        let hash = bcrypt::hash_with_result(&cfg.squid_proxy.password, 12).map_err(|e| errorf!("{}", e))?.format_for_version(bcrypt::Version::TwoA);
        format!("{}:{}\n", cfg.squid_proxy.username, hash).into_bytes()
    } else {
        b"# Squid authentication is disabled\n".to_vec()
    };
    // Root never fetches network-controlled configuration: the hardened pilot
    // uses the reviewed built-in global list only.
    let adblock = if cfg.adguard.enabled {
        services::generate_ad_block_conf(cfg, &[]).map_err(|e| wrapf("adguard", e))?.into_bytes()
    } else {
        b"# AdGuard disabled\n".to_vec()
    };
    let qos = services::generate_qos(cfg).map_err(|e| wrapf("qos", e))?;
    let ddns = services::generate_cloudflare_ddns(cfg).map_err(|e| wrapf("cloudflare ddns", e))?;
    let tunnel = services::generate_cloudflare_tunnel(cfg).map_err(|e| wrapf("cloudflare tunnel", e))?;
    let doh = services::generate_doh_proxy(cfg).map_err(|e| wrapf("doh proxy", e))?;
    // The router resolves through its own dnsmasq, whose upstreams and the
    // nftables output allowlist both derive from dhcp.dns_servers.
    let resolv = b"nameserver 127.0.0.1\n".to_vec();
    let a = |path: &'static str, data: Vec<u8>, mode: u32| Artifact { path, data, mode };
    Ok(BTreeMap::from([
        ("nftables", a(NFT_RUNTIME_PATH, nft.into_bytes(), 0o600)),
        ("pppoe", a("/etc/ppp/peers/wan", pppoe.peer_config.into_bytes(), 0o600)),
        ("chap", a("/etc/ppp/chap-secrets", pppoe.chap_secrets.into_bytes(), 0o600)),
        ("pap", a("/etc/ppp/pap-secrets", pppoe.pap_secrets.into_bytes(), 0o600)),
        ("dnsmasq", a("/etc/dnsmasq.d/minimalrouter.conf", dnsmasq.into_bytes(), 0o640)),
        ("adblock", a("/etc/dnsmasq.d/adblock_hosts.conf", adblock, 0o640)),
        ("qos", a("/etc/minimalrouter/qos.plan", qos.into_bytes(), 0o600)),
        ("cf-ddns", a("/etc/inadyn/inadyn.conf", ddns.into_bytes(), 0o640)),
        ("cf-tunnel", a("/etc/cloudflared/config.yml", tunnel.into_bytes(), 0o600)),
        ("doh-proxy", a("/etc/cloudflared/doh-proxy.yml", doh.into_bytes(), 0o600)),
        ("hostapd", a("/etc/hostapd/hostapd.conf", hostapd.into_bytes(), 0o600)),
        ("wireguard", a(WIREGUARD_CONFIG_PATH, wireguard.into_bytes(), 0o600)),
        ("wireguard-runtime", a(WIREGUARD_RUNTIME_PATH, wireguard_runtime.into_bytes(), 0o600)),
        ("wireguard-client-runtime", a(WIREGUARD_CLIENT_RUNTIME_PATH, wireguard_client_runtime, 0o600)),
        ("squid", a("/etc/squid/squid.conf", squid_config.into_bytes(), 0o644)),
        ("squid-passwd", a("/etc/squid/passwd", squid_password, 0o640)),
        ("resolv-conf", a("/etc/resolv.conf", resolv, 0o644)),
    ]))
}

/// The artifacts routerd sent must equal what the helper generates itself.
pub(crate) fn compare_generated(req: &crate::apply::ApplyRequest, generated: &Artifacts) -> Result<()> {
    for (name, supplied) in [
        ("nftables", &req.nftables),
        ("pppoe", &req.pppoe_peer),
        ("chap", &req.pppoe_secret),
        ("dnsmasq", &req.dnsmasq),
        ("hostapd", &req.hostapd),
        ("wireguard", &req.wireguard),
    ] {
        if supplied.as_bytes() != generated[name].data.as_slice() {
            return Err(errorf!("{} differs", name));
        }
    }
    Ok(())
}

/// A private, auto-removed candidate directory under the socket directory.
pub(crate) struct CandidateDir(pub std::path::PathBuf);

impl CandidateDir {
    pub(crate) fn create(prefix: &str) -> Result<CandidateDir> {
        let dir = std::path::Path::new(SOCKET_DIR).join(format!("{}{}", prefix, crate::auth::generate_random_hex(6)?));
        std::fs::create_dir(&dir)?;
        let dir = CandidateDir(dir);
        crate::config::set_mode(&dir.0.to_string_lossy(), 0o700)?;
        Ok(dir)
    }
}

impl Drop for CandidateDir {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

pub(crate) fn write_candidates(dir: &std::path::Path, generated: &Artifacts) -> Result<BTreeMap<&'static str, String>> {
    let mut paths = BTreeMap::new();
    for (name, item) in generated {
        let path = dir.join(format!("{}.conf", name));
        std::fs::write(&path, &item.data)?;
        crate::config::set_mode(&path.to_string_lossy(), item.mode)?;
        paths.insert(*name, path.to_string_lossy().into_owned());
    }
    Ok(paths)
}

/// Mirrors packaging/alpine/minimalrouter.modules.
pub(crate) const ROUTER_KERNEL_MODULES: [&str; 9] = ["nf_conntrack", "ppp_generic", "pppox", "pppoe", "wireguard", "bridge", "sch_cake", "sch_fq_codel", "ifb"];

/// Loads the router's kernel modules before preflight; idempotent and best
/// effort (a missing module fails its own component preflight).
pub(crate) fn ensure_router_kernel_modules() {
    let mut args = vec!["-a"];
    args.extend(ROUTER_KERNEL_MODULES);
    if let Err(err) = run_fixed("/sbin/modprobe", &args) {
        log::warn!("kernel module preload incomplete: {}", err);
    }
}

pub(crate) fn preflight(cfg: &SystemConfig, candidates: &BTreeMap<&'static str, String>, plan: VerificationPlan) -> Result<()> {
    ensure_router_kernel_modules();
    run_nft_file(&candidates["nftables"], true).map_err(|e| wrapf("nftables", e))?;
    run_fixed("/usr/sbin/dnsmasq", &["--test", &format!("--conf-file={}", candidates["dnsmasq"])]).map_err(|e| wrapf("dnsmasq", e))?;
    if cfg.wan.enabled {
        run_fixed("/usr/sbin/pppd", &["dryrun", "file", &candidates["pppoe"]]).map_err(|e| wrapf("pppd", e))?;
    }
    let degraded = |required: bool, label: &str, what: &str, result: Result<()>| -> Result<()> {
        match result {
            Err(err) if required => Err(wrapf(label, err)),
            Err(err) => {
                log::warn!("{} preflight degraded (non-fatal for unrelated change): {}", what, err);
                Ok(())
            }
            Ok(()) => Ok(()),
        }
    };
    if cfg.wireguard.enabled {
        degraded(plan.wireguard, "WireGuard", "WireGuard", preflight_wireguard(&candidates["wireguard-runtime"], DEFAULT_COMMAND_TIMEOUT))?;
    }
    if cfg.wg_client.enabled {
        degraded(
            plan.wg_client,
            "WireGuard client",
            "WireGuard client",
            preflight_wireguard(&candidates["wireguard-client-runtime"], DEFAULT_COMMAND_TIMEOUT),
        )?;
    }
    if cfg.wifi.enabled {
        degraded(plan.wifi, "Wi-Fi", "Wi-Fi", preflight_wifi(&cfg.wifi.interface))?;
    }
    if cfg.cloudflare.ddns_enabled {
        degraded(plan.ddns, "Cloudflare DDNS", "DDNS", run_fixed("/usr/sbin/inadyn", &["--check-config", "-f", &candidates["cf-ddns"]]))?;
    }
    if cfg.squid_proxy.enabled {
        degraded(plan.squid, "Squid", "Squid", run_fixed("/usr/sbin/squid", &["-k", "parse", "-f", &candidates["squid"]]))?;
    }
    Ok(())
}

/// Validates runtime configuration against a throwaway interface; the timeout
/// bounds endpoint hostname resolution by wg(8).
pub(crate) fn preflight_wireguard(runtime_path: &str, timeout: Duration) -> Result<()> {
    let check = format!("mrwg{}", crate::auth::generate_random_hex(4).map_err(|_| errorf!("could not allocate validation interface name"))?);
    run_fixed("/sbin/ip", &["link", "add", "dev", &check, "type", "wireguard"]).map_err(|e| wrapf("create validation interface", e))?;
    let result = run_fixed_timeout(timeout, "/usr/bin/wg", &["setconf", &check, runtime_path]).map_err(|e| wrapf("validate configuration", e));
    let _ = run_fixed("/sbin/ip", &["link", "delete", "dev", &check]);
    result
}

pub(crate) fn preflight_wifi(interface: &str) -> Result<()> {
    run_fixed("/sbin/ip", &["link", "show", "dev", interface]).map_err(|e| wrapf("interface unavailable", e))?;
    run_fixed_output("/usr/sbin/iw", &["dev", interface, "info"]).map_err(|e| wrapf("interface is not a managed wireless radio", e))?;
    let capabilities = run_fixed_output("/usr/sbin/iw", &["list"]).map_err(|e| wrapf("read radio capabilities", e))?;
    if !capabilities.contains("Supported interface modes:") || !capabilities.contains("* AP") {
        return Err(errorf!("wireless radio does not advertise access-point mode"));
    }
    Ok(())
}

/// Installs every artifact, then restores service access to the files.
pub(crate) fn install_artifacts(cfg: &SystemConfig, generated: &Artifacts) -> Result<()> {
    for name in RESTORE_ARTIFACTS {
        let item = &generated[name];
        atomic_write(item.path, &item.data, item.mode).map_err(|e| wrapf(format!("install {}", name), e))?;
    }
    if cfg.cloudflare.ddns_enabled {
        secure_ddns_configuration().map_err(|e| wrapf("secure DDNS state", e))?;
    }
    if cfg.squid_proxy.enabled {
        secure_squid_password_file()?;
    }
    Ok(())
}

pub(crate) fn install_and_activate(cfg: &SystemConfig, generated: &Artifacts, previous: Option<&SystemConfig>, provisional: bool) -> Result<()> {
    let dnsmasq_changed = dnsmasq_artifacts_changed(generated, previous)?;
    prepare_dnsmasq_lease_state().map_err(|e| wrapf("prepare DHCP lease state", e))?;
    install_artifacts(cfg, generated)?;
    apply_kernel_hardening(cfg)?;
    // Unchanged subsystems are never torn down: an unrelated save must not
    // drop wg0/wg1 sessions or restart Squid/DDNS.
    let changed = |f: &dyn Fn(&SystemConfig) -> bool| previous.is_none_or(|p| f(p));
    let wifi_changed = changed(&|p| cfg.wifi != p.wifi);
    let wan_changed = changed(&|p| cfg.wan != p.wan);
    let wg_changed = changed(&|p| cfg.wireguard != p.wireguard);
    let wg_client_changed = changed(&|p| cfg.wg_client != p.wg_client);
    let ddns_changed = changed(&|p| cfg.cloudflare != p.cloudflare);
    let squid_changed = changed(&|p| cfg.squid_proxy != p.squid_proxy);
    let extra_lan_changed = changed(&|p| cfg.firewall.extra_lans != p.firewall.extra_lans);

    // A running AP owns the bridge membership; stop it before LAN changes.
    if wifi_changed {
        let _ = run_fixed("/sbin/rc-service", &["hostapd", "stop"]);
    }
    if provisional {
        configure_provisional_runtime_lan(cfg, previous.expect("provisional apply requires a previous configuration"))?;
    } else {
        configure_runtime_lan(cfg)?;
    }
    if extra_lan_changed {
        configure_extra_lans(cfg)?;
    }
    // A removed segment keeps its address and route unless explicitly flushed.
    if let Some(previous) = previous.filter(|_| extra_lan_changed) {
        clean_extra_lan_state(&extra_lan_interfaces(previous), &extra_lan_interfaces(cfg))?;
    }
    run_nft_file(NFT_RUNTIME_PATH, false).map_err(|e| wrapf("load nftables", e))?;
    // Only now, with the policy proven loaded, is routing switched on.
    enable_ip_forwarding()?;
    // Management tunnels come up before dnsmasq restarts.
    if wg_changed {
        sync_or_activate_wireguard(cfg, previous)?;
    }
    if wg_client_changed {
        sync_or_activate_wireguard_client(cfg, previous)?;
    }
    if dnsmasq_changed {
        run_fixed("/sbin/rc-service", &["dnsmasq", "restart"]).map_err(|e| wrapf("restart dnsmasq", e))?;
    }
    if cfg.wan.enabled {
        if wan_changed {
            run_fixed("/sbin/rc-service", &["pppoe-wan", "restart"]).map_err(|e| wrapf("restart PPPoE", e))?;
        }
    } else if wan_changed {
        let _ = run_fixed("/sbin/rc-service", &["pppoe-wan", "stop"]);
    }
    if cfg.wifi.enabled {
        if wifi_changed {
            run_fixed("/sbin/rc-service", &["hostapd", "restart"]).map_err(|e| wrapf("restart hostapd", e))?;
        }
    } else if wifi_changed {
        let _ = run_fixed("/sbin/rc-service", &["hostapd", "stop"]);
    }
    if ddns_changed {
        if cfg.cloudflare.ddns_enabled {
            run_fixed("/sbin/rc-service", &["inadyn", "restart"]).map_err(|e| wrapf("restart Cloudflare DDNS", e))?;
        } else {
            let _ = run_fixed("/sbin/rc-service", &["inadyn", "stop"]);
        }
    }
    // QoS attaches to ppp0, which pppd recreates, so it follows PPPoE. It is an
    // optimisation, never a reason to fail or roll back a valid configuration.
    if cfg.qos.enabled {
        if let Err(err) = apply_qos(cfg) {
            log::warn!("apply QoS not applied (non-fatal): {}", err);
        }
        let iface = if cfg.wan.enabled { "ppp0".to_string() } else { cfg.wan.interface.clone() };
        let output = run_fixed_output("/sbin/tc", &["qdisc", "show", "dev", &iface]);
        let active = output.as_ref().is_ok_and(|o| o.contains(" cake ") || o.contains(" fq_codel ") || o.contains(" htb "));
        if !active {
            log::warn!("QoS qdisc not active on {} (non-fatal, retried on next apply): {:?}", iface, output.err().map(|e| e.to_string()));
        }
    } else {
        clear_qos(cfg);
    }
    if squid_changed {
        if cfg.squid_proxy.enabled {
            run_fixed("/sbin/rc-service", &["squid", "restart"]).map_err(|e| wrapf("restart Squid", e))?;
        } else {
            stop_squid().map_err(|e| wrapf("stop Squid", e))?;
        }
    }
    Ok(())
}

fn address_prefix(cidr: &str) -> &str {
    cidr.split('/').next().unwrap_or(cidr)
}

fn has_inet(output: &str, address: &str) -> bool {
    output.contains(&format!("inet {}/", address))
}

pub(crate) fn verify_active(cfg: &SystemConfig, plan: VerificationPlan) -> Result<()> {
    run_fixed("/usr/sbin/nft", &["list", "table", "inet", "minimalrouter"]).map_err(|e| wrapf("nftables table unavailable", e))?;
    run_fixed("/sbin/rc-service", &["dnsmasq", "status"]).map_err(|e| wrapf("dnsmasq unhealthy", e))?;
    let output =
        run_fixed_output("/sbin/ip", &["-4", "addr", "show", "dev", cfg.runtime_lan_interface()]).map_err(|e| wrapf("LAN interface unavailable", e))?;
    if !has_inet(&output, address_prefix(&cfg.lan.cidr)) {
        return Err(errorf!("configured LAN address is not active"));
    }
    if cfg.wan.enabled && plan.wan {
        verify_wan()?;
    }
    if cfg.wireguard.enabled && plan.wireguard {
        let iface = wireguard_interface_name(&cfg.wireguard);
        run_fixed("/usr/bin/wg", &["show", &iface]).map_err(|e| wrapf("WireGuard interface unhealthy", e))?;
        let out = run_fixed_output("/sbin/ip", &["-4", "addr", "show", "dev", &iface]).map_err(|e| wrapf("WireGuard address unavailable", e))?;
        let expected = address_prefix(&cfg.wireguard.address);
        if expected.is_empty() || !has_inet(&out, expected) {
            return Err(errorf!("configured WireGuard address is not active"));
        }
    }
    if cfg.wg_client.enabled && plan.wg_client {
        let iface = wireguard_client_interface_name(&cfg.wg_client);
        run_fixed("/usr/bin/wg", &["show", &iface]).map_err(|e| wrapf("WireGuard client interface unhealthy", e))?;
        if !cfg.wg_client.address.is_empty() {
            let out = run_fixed_output("/sbin/ip", &["-4", "addr", "show", "dev", &iface]).map_err(|e| wrapf("WireGuard client address unavailable", e))?;
            if !has_inet(&out, address_prefix(&cfg.wg_client.address)) {
                return Err(errorf!("configured WireGuard client address is not active"));
            }
        }
    }
    if plan.extra_lan {
        for lan in cfg.firewall.extra_lans.iter() {
            if !lan.enabled || lan.interface.is_empty() || lan.router_address.is_empty() {
                continue;
            }
            let out = run_fixed_output("/sbin/ip", &["-4", "addr", "show", "dev", &lan.interface])
                .map_err(|e| wrapf(format!("extra LAN {} interface unavailable", lan.interface), e))?;
            if !has_inet(&out, address_prefix(&lan.router_address)) {
                return Err(errorf!("extra LAN {} router address is not active", lan.interface));
            }
        }
    }
    if cfg.wifi.enabled && plan.wifi {
        run_fixed("/sbin/rc-service", &["hostapd", "status"]).map_err(|e| wrapf("hostapd unhealthy", e))?;
        run_fixed("/sbin/ip", &["link", "show", "dev", &cfg.wifi.interface]).map_err(|e| wrapf("Wi-Fi interface unavailable", e))?;
        if !wifi_attached_to_bridge(&cfg.wifi.interface) {
            return Err(errorf!("Wi-Fi interface is not attached to the LAN bridge"));
        }
    }
    if cfg.cloudflare.ddns_enabled && plan.ddns {
        run_fixed("/sbin/rc-service", &["inadyn", "status"]).map_err(|e| wrapf("Cloudflare DDNS unhealthy", e))?;
    }
    if cfg.squid_proxy.enabled && plan.squid {
        let port = if cfg.squid_proxy.port == 0 { 3128 } else { cfg.squid_proxy.port };
        let address = net::join_host_port(&cfg.lan.ip_address, &port.to_string());
        let deadline = Instant::now() + Duration::from_secs(45);
        loop {
            let ok = address.parse::<std::net::SocketAddr>().ok().is_some_and(|sa| std::net::TcpStream::connect_timeout(&sa, Duration::from_secs(1)).is_ok());
            if ok {
                break;
            }
            if Instant::now() > deadline {
                return Err(errorf!("Squid did not become ready on the configured LAN endpoint"));
            }
            std::thread::sleep(Duration::from_millis(500));
        }
        run_fixed("/sbin/rc-service", &["squid", "status"]).map_err(|e| wrapf("Squid unhealthy after listener became ready", e))?;
    }
    Ok(())
}

pub(crate) fn wifi_attached_to_bridge(interface: &str) -> bool {
    std::fs::read_link(format!("/sys/class/net/{}/master", interface))
        .ok()
        .and_then(|p| p.file_name().map(|n| n.to_string_lossy().into_owned()))
        .is_some_and(|name| name == WIFI_BRIDGE_INTERFACE)
}

pub(crate) fn remove_wireguard(interface: &str) -> Result<()> {
    let _ = run_fixed("/sbin/ip", &["link", "delete", "dev", interface]);
    if run_fixed("/sbin/ip", &["link", "show", "dev", interface]).is_ok() {
        return Err(errorf!("WireGuard interface remained active"));
    }
    Ok(())
}

pub(crate) fn wireguard_interface_name(cfg: &WireGuardConfig) -> String {
    if cfg.interface.is_empty() { "wg0".into() } else { cfg.interface.clone() }
}

pub(crate) fn wireguard_client_interface_name(cfg: &WGClientConfig) -> String {
    if cfg.interface.is_empty() { "wg1".into() } else { cfg.interface.clone() }
}

pub(crate) fn wireguard_mtu(cfg: &SystemConfig) -> i64 {
    (cfg.wan.mtu - 80).clamp(576, 1420)
}

/// Address, interface, enabled state or MTU edits cannot go through syncconf.
fn wireguard_full_recreate(cfg: &SystemConfig, previous: &SystemConfig) -> bool {
    cfg.wireguard.interface != previous.wireguard.interface
        || cfg.wireguard.address != previous.wireguard.address
        || cfg.wireguard.enabled != previous.wireguard.enabled
        || wireguard_mtu(cfg) != wireguard_mtu(previous)
}

fn wireguard_client_full_recreate(cfg: &SystemConfig, previous: &SystemConfig) -> bool {
    cfg.wg_client.interface != previous.wg_client.interface
        || cfg.wg_client.address != previous.wg_client.address
        || cfg.wg_client.enabled != previous.wg_client.enabled
        || wireguard_mtu(cfg) != wireguard_mtu(previous)
}

/// Peer/key/endpoint edits go through `wg syncconf` (sessions survive);
/// topology changes rebuild the interface. Routes are reconciled afterwards.
pub(crate) fn sync_or_activate_wireguard(cfg: &SystemConfig, previous: Option<&SystemConfig>) -> Result<()> {
    let Some(previous) = previous.filter(|p| !wireguard_full_recreate(cfg, p)) else {
        if let Some(previous) = previous {
            let (old, new) = (wireguard_interface_name(&previous.wireguard), wireguard_interface_name(&cfg.wireguard));
            if old != new {
                remove_wireguard(&old).map_err(|e| wrapf("remove previous WireGuard interface", e))?;
            }
        }
        return activate_wireguard(cfg);
    };
    let iface = wireguard_interface_name(&cfg.wireguard);
    if !cfg.wireguard.enabled {
        return remove_wireguard(&iface);
    }
    if run_fixed_output("/sbin/ip", &["link", "show", "dev", &iface]).is_err() {
        return activate_wireguard(cfg);
    }
    run_fixed("/usr/bin/wg", &["syncconf", &iface, WIREGUARD_RUNTIME_PATH]).map_err(|e| wrapf("sync WireGuard configuration", e))?;
    clear_removed_wireguard_preshared_keys(&iface, &previous.wireguard.peers, &cfg.wireguard.peers)?;
    sync_routes(&peer_allowed_ips(&cfg.wireguard.peers), &peer_allowed_ips(&previous.wireguard.peers), &iface, "install WireGuard peer route")
}

pub(crate) fn sync_or_activate_wireguard_client(cfg: &SystemConfig, previous: Option<&SystemConfig>) -> Result<()> {
    let Some(previous) = previous.filter(|p| !wireguard_client_full_recreate(cfg, p)) else {
        if let Some(previous) = previous {
            let (old, new) = (wireguard_client_interface_name(&previous.wg_client), wireguard_client_interface_name(&cfg.wg_client));
            if old != new {
                remove_wireguard(&old).map_err(|e| wrapf("remove previous WireGuard client interface", e))?;
            }
        }
        return activate_wireguard_client(cfg, DEFAULT_COMMAND_TIMEOUT);
    };
    let iface = wireguard_client_interface_name(&cfg.wg_client);
    if !cfg.wg_client.enabled {
        return remove_wireguard(&iface);
    }
    if run_fixed_output("/sbin/ip", &["link", "show", "dev", &iface]).is_err() {
        return activate_wireguard_client(cfg, DEFAULT_COMMAND_TIMEOUT);
    }
    run_fixed("/usr/bin/wg", &["syncconf", &iface, WIREGUARD_CLIENT_RUNTIME_PATH]).map_err(|e| wrapf("sync WireGuard client configuration", e))?;
    if !previous.wg_client.preshared_key.is_empty() && cfg.wg_client.preshared_key.is_empty() && previous.wg_client.public_key == cfg.wg_client.public_key {
        clear_wireguard_preshared_key(&iface, &cfg.wg_client.public_key)?;
    }
    sync_routes(&cfg.wg_client.allowed_ips, &previous.wg_client.allowed_ips, &iface, "install WireGuard client route")
}

/// `wg syncconf` keeps a kernel PSK when the file omits it; clear it explicitly.
fn clear_removed_wireguard_preshared_keys(iface: &str, previous: &[WireGuardPeer], current: &[WireGuardPeer]) -> Result<()> {
    for old in previous.iter().filter(|p| !p.preshared_key.is_empty() && p.enabled) {
        if let Some(peer) = current
            .iter()
            .find(|peer| old.enabled && !old.preshared_key.is_empty() && peer.enabled && peer.public_key == old.public_key && peer.preshared_key.is_empty())
        {
            clear_wireguard_preshared_key(iface, &peer.public_key).map_err(|e| wrapf(format!("clear WireGuard preshared key for peer {}", peer.name), e))?;
        }
    }
    Ok(())
}

fn clear_wireguard_preshared_key(iface: &str, public_key: &str) -> Result<()> {
    run_fixed("/usr/bin/wg", &["set", iface, "peer", public_key, "preshared-key", "/dev/null"]).map_err(|e| wrapf("clear WireGuard preshared key", e))
}

fn sync_routes(routes: &[String], old_routes: &[String], iface: &str, label: &str) -> Result<()> {
    for route in routes {
        run_fixed("/sbin/ip", &["-4", "route", "replace", route, "dev", iface]).map_err(|e| wrapf(label, e))?;
    }
    for stale in removed_allowed_ips(old_routes, routes) {
        let _ = run_fixed("/sbin/ip", &["-4", "route", "del", &stale, "dev", iface]);
    }
    Ok(())
}

fn peer_allowed_ips(peers: &[WireGuardPeer]) -> Vec<String> {
    peers.iter().filter(|p| p.enabled).flat_map(|p| p.allowed_ips.iter().cloned()).collect()
}

/// Normalised routes in `old` but not in `new`: syncconf leaves them behind.
pub(crate) fn removed_allowed_ips(old: &[String], new: &[String]) -> Vec<String> {
    let keep: HashSet<String> = new.iter().map(|r| normalize_cidr(r)).collect();
    let mut seen = HashSet::new();
    old.iter().map(|r| normalize_cidr(r)).filter(|k| !keep.contains(k) && seen.insert(k.clone())).collect()
}

pub(crate) fn normalize_cidr(value: &str) -> String {
    let value = value.trim();
    if let Some(ip) = net::parse_ip(value) {
        let bits = if ip.to4().is_some() { 32 } else { 128 };
        return format!("{}/{}", ip, bits);
    }
    match net::parse_cidr(value) {
        Some((_, network)) => network.to_string(),
        None => value.to_string(),
    }
}

struct InterfaceCleanup<'a> {
    iface: &'a str,
    armed: bool,
}

impl Drop for InterfaceCleanup<'_> {
    fn drop(&mut self) {
        if self.armed {
            let _ = remove_wireguard(self.iface);
        }
    }
}

pub(crate) fn activate_wireguard(cfg: &SystemConfig) -> Result<()> {
    let iface = wireguard_interface_name(&cfg.wireguard);
    remove_wireguard(&iface).map_err(|e| wrapf("stop WireGuard", e))?;
    if !cfg.wireguard.enabled {
        return Ok(());
    }
    let mut cleanup = InterfaceCleanup { iface: &iface, armed: true };
    run_fixed("/sbin/ip", &["link", "add", "dev", &iface, "type", "wireguard"]).map_err(|e| wrapf("create WireGuard interface", e))?;
    run_fixed("/usr/bin/wg", &["setconf", &iface, WIREGUARD_RUNTIME_PATH]).map_err(|e| wrapf("load WireGuard configuration", e))?;
    run_fixed("/sbin/ip", &["-4", "address", "add", &cfg.wireguard.address, "dev", &iface]).map_err(|e| wrapf("assign WireGuard address", e))?;
    run_fixed("/sbin/ip", &["link", "set", "dev", &iface, "mtu", &wireguard_mtu(cfg).to_string(), "up"]).map_err(|e| wrapf("bring WireGuard up", e))?;
    for route in peer_allowed_ips(&cfg.wireguard.peers) {
        run_fixed("/sbin/ip", &["-4", "route", "replace", &route, "dev", &iface]).map_err(|e| wrapf("install WireGuard peer route", e))?;
    }
    cleanup.armed = false;
    Ok(())
}

/// The timeout bounds endpoint hostname resolution by wg(8) during startup.
pub(crate) fn activate_wireguard_client(cfg: &SystemConfig, timeout: Duration) -> Result<()> {
    let iface = wireguard_client_interface_name(&cfg.wg_client);
    remove_wireguard(&iface).map_err(|e| wrapf("stop WireGuard client", e))?;
    if !cfg.wg_client.enabled {
        return Ok(());
    }
    let mut cleanup = InterfaceCleanup { iface: &iface, armed: true };
    run_fixed("/sbin/ip", &["link", "add", "dev", &iface, "type", "wireguard"]).map_err(|e| wrapf("create WireGuard client interface", e))?;
    run_fixed_timeout(timeout, "/usr/bin/wg", &["setconf", &iface, WIREGUARD_CLIENT_RUNTIME_PATH])
        .map_err(|e| wrapf("load WireGuard client configuration", e))?;
    if !cfg.wg_client.address.is_empty() {
        run_fixed("/sbin/ip", &["-4", "address", "add", &cfg.wg_client.address, "dev", &iface]).map_err(|e| wrapf("assign WireGuard client address", e))?;
    }
    run_fixed("/sbin/ip", &["link", "set", "dev", &iface, "mtu", &wireguard_mtu(cfg).to_string(), "up"]).map_err(|e| wrapf("bring WireGuard client up", e))?;
    for route in cfg.wg_client.allowed_ips.iter() {
        run_fixed("/sbin/ip", &["-4", "route", "replace", route, "dev", &iface]).map_err(|e| wrapf("install WireGuard client route", e))?;
    }
    cleanup.armed = false;
    Ok(())
}

pub(crate) fn capture_previous(generated: &Artifacts) -> Result<Vec<PreviousFile>> {
    let mut result = Vec::with_capacity(generated.len());
    for item in generated.values() {
        match std::fs::metadata(item.path) {
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                result.push(PreviousFile { path: item.path.into(), data: Vec::new(), mode: 0, existed: false });
            }
            Err(e) => return Err(e.into()),
            Ok(meta) => {
                let data = std::fs::read(item.path)?;
                result.push(PreviousFile { path: item.path.into(), data, mode: file_mode(&meta), existed: true });
            }
        }
    }
    Ok(result)
}

#[cfg(unix)]
fn file_mode(meta: &std::fs::Metadata) -> u32 {
    use std::os::unix::fs::PermissionsExt;
    meta.permissions().mode() & 0o777
}

#[cfg(not(unix))]
fn file_mode(_meta: &std::fs::Metadata) -> u32 {
    0o600
}

pub(crate) fn rollback_wireguard_cleanup_interfaces(previous: Option<&SystemConfig>, candidate: Option<&SystemConfig>) -> Vec<String> {
    let Some(candidate) = candidate else { return Vec::new() };
    let mut keep = HashSet::new();
    if let Some(previous) = previous {
        if previous.wireguard.enabled {
            keep.insert(wireguard_interface_name(&previous.wireguard));
        }
        if previous.wg_client.enabled {
            keep.insert(wireguard_client_interface_name(&previous.wg_client));
        }
    }
    let mut candidates = Vec::new();
    if candidate.wireguard.enabled {
        candidates.push(wireguard_interface_name(&candidate.wireguard));
    }
    if candidate.wg_client.enabled {
        candidates.push(wireguard_client_interface_name(&candidate.wg_client));
    }
    let mut seen = HashSet::new();
    candidates.into_iter().filter(|i| !keep.contains(i) && seen.insert(i.clone())).collect()
}

pub(crate) fn rollback(previous: Option<&SystemConfig>, candidate: Option<&SystemConfig>, files: &[PreviousFile]) -> Result<()> {
    let mut errs: Vec<String> = Vec::new();
    let mut had_previous_nft = false;
    for file in files {
        if file.path == NFT_RUNTIME_PATH {
            had_previous_nft = file.existed;
        }
        if !file.existed {
            if let Err(e) = std::fs::remove_file(&file.path) {
                if e.kind() != std::io::ErrorKind::NotFound {
                    errs.push(sanitize_output(e.to_string().as_bytes()));
                }
            }
            continue;
        }
        if let Err(e) = atomic_write(&file.path, &file.data, file.mode) {
            errs.push(safe_error(&e));
        }
    }
    if let Some(prev) = previous {
        if prev.squid_proxy.enabled {
            if let Err(e) = secure_squid_password_file() {
                errs.push(safe_error(&e));
            }
        }
        if let Err(e) = apply_kernel_hardening(prev) {
            errs.push(safe_error(&e));
        }
        let _ = run_fixed("/sbin/rc-service", &["hostapd", "stop"]);
        if let Err(e) = configure_runtime_lan(prev) {
            errs.push(safe_error(&e));
        }
        if let Err(e) = configure_extra_lans(prev) {
            log::warn!("rollback optional ExtraLAN restore degraded: {}", e);
        }
        if let Some(cand) = candidate {
            if let Err(e) = clean_extra_lan_state(&extra_lan_interfaces(cand), &extra_lan_interfaces(prev)) {
                errs.push(safe_error(&e));
            }
        }
    }
    for iface in rollback_wireguard_cleanup_interfaces(previous, candidate) {
        if let Err(e) = remove_wireguard(&iface) {
            errs.push(format!("remove failed candidate WireGuard interface {}: {}", iface, safe_error(&e)));
        }
    }
    if had_previous_nft {
        match run_nft_file(NFT_RUNTIME_PATH, false) {
            Err(e) => errs.push(safe_error(&e)),
            Ok(()) => {
                if let Err(e) = enable_ip_forwarding() {
                    errs.push(safe_error(&e));
                }
            }
        }
    } else {
        // No previous policy: routing goes down before the table is removed,
        // so an unprotected forwarding window never exists.
        if let Err(e) = disable_ip_forwarding() {
            errs.push(safe_error(&e));
        }
        let _ = run_fixed("/usr/sbin/nft", &["delete", "table", "inet", "minimalrouter"]);
        if run_fixed("/usr/sbin/nft", &["list", "table", "inet", "minimalrouter"]).is_ok() {
            errs.push("candidate nftables table remained active".into());
        }
    }
    match previous {
        Some(prev) => {
            if let Err(e) = activate_wireguard(prev) {
                if prev.system.management_access == "wireguard_only" {
                    errs.push(safe_error(&e));
                } else {
                    log::warn!("rollback optional WireGuard restore degraded: {}", e);
                }
            }
            if let Err(e) = run_fixed("/sbin/rc-service", &["dnsmasq", "restart"]) {
                errs.push(safe_error(&e));
            }
        }
        None => {
            let _ = run_fixed("/sbin/rc-service", &["dnsmasq", "stop"]);
        }
    }
    if previous.is_some_and(|p| p.wan.enabled) {
        if let Err(e) = run_fixed("/sbin/rc-service", &["pppoe-wan", "restart"]) {
            log::warn!("rollback PPPoE unavailable (non-fatal to local rollback): {}", e);
        }
    } else {
        let _ = run_fixed("/sbin/rc-service", &["pppoe-wan", "stop"]);
    }
    match previous {
        Some(prev) => {
            if let Err(e) = activate_wireguard_client(prev, DEFAULT_COMMAND_TIMEOUT) {
                log::warn!("rollback optional WireGuard client restore degraded: {}", e);
            }
        }
        None => {
            let _ = remove_wireguard("wg0");
            let _ = remove_wireguard("wg1");
        }
    }
    if previous.is_some_and(|p| p.wifi.enabled) {
        if let Err(e) = run_fixed("/sbin/rc-service", &["hostapd", "restart"]) {
            log::warn!("rollback optional Wi-Fi restore degraded: {}", e);
        }
    } else {
        let _ = run_fixed("/sbin/rc-service", &["hostapd", "stop"]);
    }
    if previous.is_some_and(|p| p.cloudflare.ddns_enabled) {
        if let Err(e) = secure_ddns_configuration() {
            log::warn!("rollback optional DDNS permissions degraded: {}", e);
            let _ = run_fixed("/sbin/rc-service", &["inadyn", "stop"]);
        } else if let Err(e) = run_fixed("/sbin/rc-service", &["inadyn", "restart"]) {
            log::warn!("rollback optional DDNS restore degraded: {}", e);
        }
    } else {
        let _ = run_fixed("/sbin/rc-service", &["inadyn", "stop"]);
    }
    if let Some(prev) = previous {
        if prev.qos.enabled {
            if let Err(e) = apply_qos(prev) {
                log::warn!("rollback QoS not applied (non-fatal): {}", e);
            }
        } else {
            clear_qos(prev);
        }
    }
    if previous.is_some_and(|p| p.squid_proxy.enabled) {
        if let Err(e) = run_fixed("/sbin/rc-service", &["squid", "restart"]) {
            log::warn!("rollback optional Squid restore degraded: {}", e);
        }
    } else {
        stop_squid().map_err(|e| wrapf("stop Squid during rollback", e))?;
    }
    if !errs.is_empty() {
        return Err(errorf!("{}", errs.join("; ")));
    }
    match previous {
        // Structural verification only: an ISP or optional-service outage must
        // never turn a successful local rollback into RecoveryRequired.
        Some(prev) => verify_active(prev, verification_plan(&op::reconcile(), None, prev))?,
        // Failed first-run transactions return to the setup-only LAN.
        None => super::startup::restore_first_run_runtime()?,
    }
    clear_pending_confirmation().map_err(|e| wrapf("clear pending state after verified rollback", e))
}

pub(crate) fn extra_lan_interfaces(cfg: &SystemConfig) -> HashSet<String> {
    cfg.firewall.extra_lans.iter().filter(|l| l.enabled && !l.interface.is_empty()).map(|l| l.interface.clone()).collect()
}

/// Flushes and takes down interfaces in `stale` not owned by `keep`.
pub(crate) fn clean_extra_lan_state(stale: &HashSet<String>, keep: &HashSet<String>) -> Result<()> {
    let mut errs = Vec::new();
    for iface in stale.iter().filter(|i| !keep.contains(*i)) {
        if let Err(e) = run_fixed("/sbin/ip", &["-4", "addr", "flush", "dev", iface, "scope", "global"]) {
            errs.push(safe_error(&e));
        }
        if let Err(e) = run_fixed("/sbin/ip", &["link", "set", "dev", iface, "down"]) {
            errs.push(safe_error(&e));
        }
    }
    if errs.is_empty() { Ok(()) } else { Err(errorf!("{}", errs.join("; "))) }
}

pub(crate) fn apply_qos(cfg: &SystemConfig) -> Result<()> {
    let commands = services::qos_commands(cfg)?;
    if commands.is_empty() {
        return Ok(());
    }
    ensure_ifb_interface()?;
    clear_qos(cfg);
    run_fixed("/sbin/ip", &["link", "set", "dev", services::QOS_INTERFACE_NAME, "up"])?;
    for args in commands {
        let refs: Vec<&str> = args.iter().map(String::as_str).collect();
        if let Err(err) = run_fixed("/sbin/tc", &refs) {
            clear_qos(cfg);
            return Err(err);
        }
    }
    Ok(())
}

pub(crate) fn clear_qos(cfg: &SystemConfig) {
    let mut iface = cfg.wan.interface.clone();
    if cfg.wan.enabled {
        iface = "ppp0".into();
    }
    if iface.is_empty() {
        iface = "eth0".into();
    }
    let _ = run_fixed("/sbin/tc", &["qdisc", "del", "dev", &iface, "root"]);
    let _ = run_fixed("/sbin/tc", &["qdisc", "del", "dev", &iface, "ingress"]);
    if run_fixed_output("/sbin/ip", &["-j", "-d", "link", "show", "dev", services::QOS_INTERFACE_NAME]).is_ok_and(|o| is_ifb_interface(&o)) {
        let _ = run_fixed("/sbin/tc", &["qdisc", "del", "dev", services::QOS_INTERFACE_NAME, "root"]);
    }
}

pub(crate) fn is_ifb_interface(output: &str) -> bool {
    #[derive(serde::Deserialize, Default)]
    #[serde(default)]
    struct Info {
        info_kind: String,
    }
    #[derive(serde::Deserialize, Default)]
    #[serde(default)]
    struct Link {
        ifname: String,
        linkinfo: Info,
    }
    match crate::goish::json::unmarshal::<Vec<Link>>(output.as_bytes()) {
        Ok(links) if links.len() == 1 => links[0].ifname == services::QOS_INTERFACE_NAME && links[0].linkinfo.info_kind == "ifb",
        _ => false,
    }
}

fn ensure_ifb_interface() -> Result<()> {
    let inspect = || run_fixed_output("/sbin/ip", &["-j", "-d", "link", "show", "dev", services::QOS_INTERFACE_NAME]);
    if let Ok(out) = inspect() {
        if !is_ifb_interface(&out) {
            return Err(errorf!("ifb0 exists but is not an IFB device"));
        }
        return Ok(());
    }
    // Loading the IFB driver can create ifb0 first; EEXIST is success only
    // after verifying the device.
    let created = run_fixed_output("/sbin/ip", &["link", "add", services::QOS_INTERFACE_NAME, "type", "ifb"]);
    if inspect().is_ok_and(|o| is_ifb_interface(&o)) {
        return Ok(());
    }
    if let Err(e) = created {
        return Err(wrapf(format!("create {}", services::QOS_INTERFACE_NAME), e));
    }
    Err(errorf!("created IFB device could not be verified"))
}

fn configure_lan_interface(interface: &str, cidr: &str) -> Result<()> {
    run_fixed("/sbin/ip", &["link", "set", "dev", interface, "up"]).map_err(|e| wrapf("bring LAN up", e))?;
    run_fixed("/sbin/ip", &["-4", "addr", "flush", "dev", interface, "scope", "global"]).map_err(|e| wrapf("clear previous LAN addresses", e))?;
    run_fixed("/sbin/ip", &["-4", "addr", "add", cidr, "dev", interface]).map_err(|e| wrapf("configure LAN address", e))?;
    send_gratuitous_arp(interface, cidr);
    Ok(())
}

/// Loopback is part of the dataplane: the generic networking init path is
/// disabled, so 127.0.0.1 is restored here before dnsmasq and probes run.
fn configure_loopback() -> Result<()> {
    run_fixed("/sbin/ip", &["link", "set", "dev", "lo", "up"]).map_err(|e| wrapf("bring loopback up", e))?;
    run_fixed("/sbin/ip", &["-4", "addr", "replace", "127.0.0.1/8", "dev", "lo"]).map_err(|e| wrapf("configure loopback address", e))
}

fn remove_owned_lan_bridge() -> Result<()> {
    let bridge = WIFI_BRIDGE_INTERFACE;
    if run_fixed("/sbin/ip", &["link", "show", "dev", bridge]).is_err() {
        return Ok(());
    }
    if let Ok(members) = std::fs::read_dir(format!("/sys/class/net/{}/brif", bridge)) {
        for member in members.flatten() {
            let name = member.file_name().to_string_lossy().into_owned();
            if !INTERFACE_NAME_PATTERN.is_match(&name) {
                return Err(errorf!("LAN bridge has an invalid member name"));
            }
            let _ = run_fixed("/sbin/ip", &["link", "set", "dev", &name, "nomaster"]);
        }
    }
    let _ = run_fixed("/sbin/ip", &["-4", "addr", "flush", "dev", bridge, "scope", "global"]);
    run_fixed("/sbin/ip", &["link", "delete", "dev", bridge, "type", "bridge"]).map_err(|e| wrapf("remove LAN bridge", e))
}

pub(crate) fn configure_runtime_lan(cfg: &SystemConfig) -> Result<()> {
    configure_loopback()?;
    if !cfg.wifi.enabled {
        remove_owned_lan_bridge()?;
        return configure_lan_interface(&cfg.lan.interface, &cfg.lan.cidr);
    }
    let bridge = WIFI_BRIDGE_INTERFACE;
    let lan = cfg.lan.interface.as_str();
    if run_fixed("/sbin/ip", &["link", "show", "dev", bridge]).is_err() {
        run_fixed("/sbin/ip", &["link", "add", "name", bridge, "type", "bridge"]).map_err(|e| wrapf("create LAN bridge", e))?;
    }
    run_fixed("/sbin/ip", &["link", "set", "dev", lan, "up"]).map_err(|e| wrapf("bring wired LAN up", e))?;
    run_fixed("/sbin/ip", &["-4", "addr", "flush", "dev", lan, "scope", "global"]).map_err(|e| wrapf("move LAN address to bridge", e))?;
    run_fixed("/sbin/ip", &["link", "set", "dev", lan, "master", bridge]).map_err(|e| wrapf("attach wired LAN to bridge", e))?;
    run_fixed("/sbin/ip", &["link", "set", "dev", bridge, "up"]).map_err(|e| wrapf("bring LAN bridge up", e))?;
    run_fixed("/sbin/ip", &["-4", "addr", "flush", "dev", bridge, "scope", "global"]).map_err(|e| wrapf("clear previous bridge addresses", e))?;
    run_fixed("/sbin/ip", &["-4", "addr", "add", &cfg.lan.cidr, "dev", bridge]).map_err(|e| wrapf("configure LAN bridge address", e))?;
    send_gratuitous_arp(bridge, &cfg.lan.cidr);
    Ok(())
}

pub(crate) fn configure_provisional_runtime_lan(candidate: &SystemConfig, previous: &SystemConfig) -> Result<()> {
    if candidate.lan.interface != previous.lan.interface {
        return Err(errorf!("LAN interface changes cannot be provisionally applied"));
    }
    configure_runtime_lan(candidate)?;
    if previous.lan.cidr != candidate.lan.cidr {
        run_fixed("/sbin/ip", &["-4", "addr", "add", &previous.lan.cidr, "dev", candidate.runtime_lan_interface()])
            .map_err(|e| wrapf("retain previous LAN address", e))?;
    }
    Ok(())
}

fn send_gratuitous_arp(interface: &str, cidr: &str) {
    let ip = cidr.split('/').next().unwrap_or("");
    let _ = run_fixed("/usr/sbin/arping", &["-U", "-c", "3", "-I", interface, ip]);
}

/// Reconstructs every enabled isolated segment on the kernel.
pub(crate) fn configure_extra_lans(cfg: &SystemConfig) -> Result<()> {
    for lan in cfg.firewall.extra_lans.iter() {
        if !lan.enabled || lan.interface.is_empty() || lan.router_address.is_empty() {
            continue;
        }
        let iface = lan.interface.as_str();
        run_fixed("/sbin/ip", &["link", "set", "dev", iface, "up"]).map_err(|e| wrapf(format!("bring extra LAN {} up", iface), e))?;
        run_fixed("/sbin/ip", &["-4", "addr", "flush", "dev", iface, "scope", "global"])
            .map_err(|e| wrapf(format!("clear extra LAN {} addresses", iface), e))?;
        run_fixed("/sbin/ip", &["-4", "addr", "add", &lan.router_address, "dev", iface])
            .map_err(|e| wrapf(format!("configure extra LAN {} address", iface), e))?;
    }
    Ok(())
}

/// Routing is never switched on before the generated policy is loaded.
pub(crate) fn enable_ip_forwarding() -> Result<()> {
    run_fixed("/sbin/sysctl", &["-w", "net.ipv4.ip_forward=1"]).map_err(|e| wrapf("enable IPv4 forwarding", e))
}

pub(crate) fn disable_ip_forwarding() -> Result<()> {
    run_fixed("/sbin/sysctl", &["-w", "net.ipv4.ip_forward=0"]).map_err(|e| wrapf("disable IPv4 forwarding", e))
}

pub(crate) fn apply_kernel_hardening(cfg: &SystemConfig) -> Result<()> {
    const SETTINGS: [(&str, &str); 30] = [
        ("net.ipv4.tcp_syncookies", "1"),
        ("net.ipv4.tcp_rfc1337", "1"),
        ("net.ipv4.conf.all.accept_redirects", "0"),
        ("net.ipv4.conf.default.accept_redirects", "0"),
        ("net.ipv4.conf.all.send_redirects", "0"),
        ("net.ipv4.conf.default.send_redirects", "0"),
        ("net.ipv4.conf.all.accept_source_route", "0"),
        ("net.ipv4.conf.default.accept_source_route", "0"),
        ("net.ipv4.conf.all.secure_redirects", "0"),
        ("net.ipv4.conf.default.secure_redirects", "0"),
        ("net.ipv4.conf.all.proxy_arp", "0"),
        ("net.ipv4.conf.default.proxy_arp", "0"),
        ("net.ipv4.conf.all.route_localnet", "0"),
        ("net.ipv4.conf.default.route_localnet", "0"),
        ("net.ipv4.conf.all.log_martians", "1"),
        ("net.ipv4.icmp_echo_ignore_broadcasts", "1"),
        ("net.ipv4.icmp_ignore_bogus_error_responses", "1"),
        ("kernel.kptr_restrict", "2"),
        ("kernel.dmesg_restrict", "1"),
        ("kernel.unprivileged_bpf_disabled", "1"),
        ("net.core.bpf_jit_harden", "2"),
        ("fs.protected_hardlinks", "1"),
        ("fs.protected_symlinks", "1"),
        ("fs.protected_fifos", "2"),
        ("fs.protected_regular", "2"),
        ("net.ipv6.conf.all.disable_ipv6", "1"),
        ("net.ipv6.conf.default.disable_ipv6", "1"),
        // Reasserted so a runtime change cannot survive a configuration apply.
        ("net.ipv4.conf.all.rp_filter", "2"),
        ("net.ipv4.conf.default.rp_filter", "2"),
        ("net.netfilter.nf_conntrack_max", "131072"),
    ];
    for (key, value) in SETTINGS {
        run_fixed("/sbin/sysctl", &["-w", &format!("{}={}", key, value)]).map_err(|e| wrapf(format!("apply sysctl {}", key), e))?;
    }
    if !cfg.wan.interface.is_empty() {
        run_fixed("/sbin/sysctl", &["-w", &format!("net.ipv4.conf.{}.rp_filter=1", cfg.wan.interface)])
            .map_err(|e| wrapf("enable WAN reverse-path filtering", e))?;
    }
    Ok(())
}

/// Avoids the OpenRC stop wrapper (which can wait 120 s and still succeed):
/// a bounded graceful stop, then a forced one, and the PID must disappear.
pub(crate) fn stop_squid() -> Result<()> {
    let shutdown = run_fixed_timeout(Duration::from_secs(5), "/usr/sbin/squid", &["-k", "shutdown", "-f", "/etc/squid/squid.conf"]);
    if wait_for_squid_exit(Duration::from_secs(8)).is_ok() {
        return Ok(());
    }
    if let Err(e) = shutdown {
        log::warn!("graceful Squid shutdown degraded: {}", e);
    }
    run_fixed_timeout(Duration::from_secs(5), "/usr/sbin/squid", &["-k", "kill", "-f", "/etc/squid/squid.conf"]).map_err(|e| wrapf("force shutdown", e))?;
    wait_for_squid_exit(Duration::from_secs(8))
}

fn wait_for_squid_exit(timeout: Duration) -> Result<()> {
    let deadline = Instant::now() + timeout;
    while squid_master_running() {
        if Instant::now() > deadline {
            return Err(errorf!("Squid process did not stop"));
        }
        std::thread::sleep(Duration::from_millis(250));
    }
    Ok(())
}

fn squid_master_running() -> bool {
    std::fs::read_to_string("/var/run/squid.pid")
        .ok()
        .and_then(|s| crate::config::go_atoi(s.trim()))
        .filter(|pid| *pid > 1)
        .is_some_and(|pid| std::path::Path::new(&format!("/proc/{}", pid)).exists())
}

pub(crate) fn secure_squid_password_file() -> Result<()> {
    let gid = lookup_group("squid").map_err(|_| errorf!("Squid service group is unavailable"))?;
    chown("/etc/squid/passwd", 0, gid).map_err(|_| errorf!("could not secure Squid password file ownership"))?;
    crate::config::set_mode("/etc/squid/passwd", 0o640).map_err(|_| errorf!("could not secure Squid password file mode"))
}

pub(crate) fn secure_ddns_configuration() -> Result<()> {
    let gid = lookup_group("inadyn").map_err(|_| errorf!("DDNS service group is unavailable"))?;
    chown("/etc/inadyn/inadyn.conf", 0, gid).map_err(|_| errorf!("could not secure DDNS configuration ownership"))?;
    crate::config::set_mode("/etc/inadyn/inadyn.conf", 0o640).map_err(|_| errorf!("could not secure DDNS configuration mode"))?;
    let (uid, _) = lookup_user("inadyn").map_err(|_| errorf!("DDNS service account is unavailable"))?;
    prepare_ddns_cache_state_at("/var/cache/inadyn", uid, gid)
}

/// Repairs DDNS cache ownership without reading contents or following links.
pub(crate) fn prepare_ddns_cache_state_at(dir: &str, uid: u32, gid: u32) -> Result<()> {
    std::fs::create_dir_all(dir).map_err(|e| wrapf("create DDNS cache directory", e))?;
    let meta = std::fs::symlink_metadata(dir)?;
    if !meta.is_dir() {
        return Err(errorf!("DDNS cache directory is not a real directory"));
    }
    chown(dir, uid, gid).map_err(|e| wrapf("own DDNS cache directory", e))?;
    crate::config::set_mode(dir, 0o755).map_err(|e| wrapf("secure DDNS cache directory", e))?;
    for entry in std::fs::read_dir(dir)?.flatten() {
        let name = entry.file_name().to_string_lossy().into_owned();
        if !name.ends_with(".cache") {
            continue;
        }
        let path = entry.path();
        if !std::fs::symlink_metadata(&path)?.file_type().is_file() {
            return Err(errorf!("DDNS cache entry is not a regular file"));
        }
        let p = path.to_string_lossy();
        chown(&p, uid, gid).map_err(|e| wrapf("own DDNS cache file", e))?;
        crate::config::set_mode(&p, 0o600).map_err(|e| wrapf("secure DDNS cache file", e))?;
    }
    Ok(())
}

const DNSMASQ_LEASE_DIR: &str = "/var/lib/minimalrouter-dhcp";
const DNSMASQ_LEASE_FILE: &str = "/var/lib/minimalrouter-dhcp/dnsmasq.leases";

/// Makes the persistent DHCP state usable by the unprivileged dnsmasq.
pub(crate) fn prepare_dnsmasq_lease_state() -> Result<()> {
    let (uid, gid) = lookup_user("dnsmasq").map_err(|e| wrapf("lookup dnsmasq service account", e))?;
    prepare_dnsmasq_lease_state_at(DNSMASQ_LEASE_DIR, DNSMASQ_LEASE_FILE, uid, gid)
}

pub(crate) fn prepare_dnsmasq_lease_state_at(dir: &str, lease_file: &str, uid: u32, gid: u32) -> Result<()> {
    std::fs::create_dir_all(dir).map_err(|e| wrapf("create persistent DHCP lease directory", e))?;
    chown(dir, uid, gid).map_err(|e| wrapf("own persistent DHCP lease directory", e))?;
    crate::config::set_mode(dir, 0o750).map_err(|e| wrapf("secure persistent DHCP lease directory", e))?;
    // Never follow an unexpected symlink at this privileged path.
    for _ in 0..2 {
        match std::fs::symlink_metadata(lease_file) {
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => match std::fs::OpenOptions::new().write(true).create_new(true).open(lease_file) {
                Ok(_) => break,
                Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => continue,
                Err(e) => return Err(wrapf("create persistent DHCP lease file", e)),
            },
            Err(e) => return Err(wrapf("inspect persistent DHCP lease file", e)),
            Ok(meta) if meta.file_type().is_symlink() => return Err(errorf!("persistent DHCP lease file is a symlink")),
            Ok(meta) if !meta.file_type().is_file() => return Err(errorf!("persistent DHCP lease file is not regular")),
            Ok(_) => break,
        }
    }
    chown(lease_file, uid, gid).map_err(|e| wrapf("own persistent DHCP lease file", e))?;
    crate::config::set_mode(lease_file, 0o640).map_err(|e| wrapf("secure persistent DHCP lease file", e))
}

/// Compares the generated dnsmasq artifacts instead of a hand-written list of
/// config sections that happen to affect dnsmasq today.
pub(crate) fn dnsmasq_artifacts_changed(generated: &Artifacts, previous: Option<&SystemConfig>) -> Result<bool> {
    let Some(previous) = previous else { return Ok(true) };
    let previous_dnsmasq = services::generate_dnsmasq(previous).map_err(|e| wrapf("generate previous dnsmasq configuration", e))?;
    let previous_adblock = if previous.adguard.enabled {
        services::generate_ad_block_conf(previous, &[]).map_err(|e| wrapf("generate previous DNS filter configuration", e))?.into_bytes()
    } else {
        b"# AdGuard disabled\n".to_vec()
    };
    Ok(generated["dnsmasq"].data != previous_dnsmasq.as_bytes() || generated["adblock"].data != previous_adblock)
}

/// Only changes that can affect Internet/DNS reachability get the probe.
pub(crate) fn requires_functional_dns_verification(previous: Option<&SystemConfig>, candidate: &SystemConfig) -> bool {
    if !candidate.wan.enabled {
        return false;
    }
    let Some(previous) = previous else { return true };
    previous.wan != candidate.wan
        || previous.dhcp.dns_servers != candidate.dhcp.dns_servers
        || previous.adguard != candidate.adguard
        || previous.system.domain != candidate.system.domain
}

const FUNCTIONAL_DNS_READINESS: Duration = Duration::from_secs(15);
const FUNCTIONAL_DNS_RETRY_EVERY: Duration = Duration::from_millis(250);

/// The router's own dnsmasq must resolve public names; a large filter list
/// delays readiness after restart, so the probe polls until a deadline.
pub(crate) fn verify_functional_dns() -> Result<()> {
    verify_functional_dns_at("127.0.0.1:53", FUNCTIONAL_DNS_READINESS, FUNCTIONAL_DNS_RETRY_EVERY)
}

pub(crate) fn verify_functional_dns_at(address: &str, readiness: Duration, retry_every: Duration) -> Result<()> {
    let deadline = Instant::now() + readiness;
    loop {
        let err = match resolve_public_names(address) {
            Ok(()) => return Ok(()),
            Err(e) => e,
        };
        if Instant::now() + retry_every > deadline {
            return Err(wrapf(format!("local DNS could not resolve public names within {}", crate::goish::time::Duration::from_std(readiness)), err));
        }
        std::thread::sleep(retry_every);
    }
}

fn resolve_public_names(address: &str) -> Result<()> {
    let mut last = None;
    for hostname in ["example.com", "cloudflare.com"] {
        match dnswire::lookup_a(address, hostname, Duration::from_secs(2)) {
            Ok(addrs) if !addrs.is_empty() => return Ok(()),
            Ok(_) => {}
            Err(e) => last = Some(e),
        }
    }
    Err(last.unwrap_or_else(|| errorf!("local DNS returned no addresses")))
}

pub(crate) fn requires_ddns_verification(previous: Option<&SystemConfig>, candidate: &SystemConfig) -> bool {
    if !candidate.wan.enabled || !candidate.cloudflare.ddns_enabled {
        return false;
    }
    previous.is_none_or(|p| p.cloudflare != candidate.cloudflare)
}

/// A bounded one-shot provider update, only when DDNS itself changed.
pub(crate) fn verify_ddns_update() -> Result<()> {
    run_command_output(
        Duration::from_secs(45),
        "/usr/sbin/inadyn",
        &["--once", "--force", "--foreground", "--no-pidfile", "--drop-privs", "inadyn:inadyn", "--config", "/etc/inadyn/inadyn.conf", "--loglevel", "notice"],
    )
    .map(|_| ())
    .map_err(|e| wrapf("dynamic DNS provider update failed", e))
}

pub(crate) fn verify_wan() -> Result<()> {
    let deadline = Instant::now() + Duration::from_secs(20);
    loop {
        let address = run_fixed_output("/sbin/ip", &["-4", "addr", "show", "dev", "ppp0"]);
        let route = run_fixed_output("/sbin/ip", &["-4", "route", "show", "default", "dev", "ppp0"]);
        if address.as_ref().is_ok_and(|a| a.contains("inet ")) && route.as_ref().is_ok_and(|r| r.trim().starts_with("default ")) {
            return Ok(());
        }
        if Instant::now() >= deadline {
            return Err(errorf!("PPPoE interface lacks an assigned IPv4 address or default route"));
        }
        std::thread::sleep(Duration::from_millis(500));
    }
}

/// The fixed WAN action; a service-status success alone proves no PPP link.
pub(crate) fn recover_wan(cfg: &SystemConfig) -> Result<()> {
    if !cfg.wan.enabled {
        return Err(errorf!("PPPoE WAN is disabled"));
    }
    run_fixed("/sbin/rc-service", &["pppoe-wan", "restart"]).map_err(|e| wrapf("restart PPPoE WAN", e))?;
    run_fixed("/sbin/rc-service", &["pppoe-wan", "status"]).map_err(|e| wrapf("PPPoE WAN service unhealthy after restart", e))?;
    verify_wan().map_err(|e| wrapf("PPPoE link was not recovered", e))
}

const MAX_SERVICE_DESTINATIONS: usize = 16384;

/// Applies the runtime set size limit without changing the generator.
pub(crate) fn bound_service_sets(candidate: &[u8]) -> Vec<u8> {
    let mut text = String::from_utf8_lossy(candidate).into_owned();
    for service in crate::config::SUPPORTED_PROFILE_SERVICES {
        let old = format!("  set svc_{} {{ type ipv4_addr; flags timeout; timeout 4h; }}", service);
        let bounded = format!("  set svc_{} {{ type ipv4_addr; flags timeout; timeout 4h; size {}; }}", service, MAX_SERVICE_DESTINATIONS);
        text = text.replace(&old, &bounded);
    }
    text.into_bytes()
}

/// Only generated, allowlisted IPv4 service sets survive a table replacement,
/// as data inside the same atomic batch; no running rule text is replayed.
fn preserve_service_destinations(candidate: &[u8]) -> Result<Vec<u8>> {
    let text = String::from_utf8_lossy(candidate);
    let wanted: HashSet<String> = crate::config::SUPPORTED_PROFILE_SERVICES
        .iter()
        .map(|s| format!("svc_{}", s))
        .filter(|name| text.contains(&format!("  set {} {{ type ipv4_addr;", name)))
        .collect();
    if wanted.is_empty() {
        return Ok(Vec::new());
    }
    let started = Instant::now();
    let raw = run_command_output(Duration::from_secs(5), "/usr/sbin/nft", &["-j", "list", "table", "inet", "minimalrouter"])
        .map_err(|e| wrapf("read service destinations", e))?;
    if raw.len() > 8 << 20 {
        return Err(errorf!("nft state exceeds the 8 MiB safety limit"));
    }
    service_destination_batch(raw.as_bytes(), &wanted, started.elapsed())
}

pub(crate) fn service_destination_batch(raw: &[u8], wanted: &HashSet<String>, elapsed: Duration) -> Result<Vec<u8>> {
    if raw.len() > 8 << 20 {
        return Err(errorf!("service destination snapshot is too large"));
    }
    let envelope: serde_json::Value = serde_json::from_slice(raw)?;
    let mut out = String::new();
    for item in envelope.get("nftables").and_then(|v| v.as_array()).cloned().unwrap_or_default() {
        let Some(set) = item.get("set") else { continue };
        let name = set.get("name").and_then(|v| v.as_str()).unwrap_or("");
        if !wanted.contains(name) {
            continue;
        }
        if !crate::config::SUPPORTED_PROFILE_SERVICES.contains(&name.trim_start_matches("svc_")) {
            return Err(errorf!("unknown service set"));
        }
        let field = |k: &str| set.get(k).and_then(|v| v.as_str()).unwrap_or("");
        let elems = set.get("elem").and_then(|v| v.as_array()).cloned().unwrap_or_default();
        if field("family") != "inet" || field("table") != "minimalrouter" || field("type") != "ipv4_addr" || elems.len() > MAX_SERVICE_DESTINATIONS {
            return Err(errorf!("invalid service destination set"));
        }
        let mut values = Vec::new();
        for entry in elems {
            let elem = entry.get("elem").ok_or_else(|| errorf!("invalid service destination address"))?;
            let val = elem.get("val").and_then(|v| v.as_str()).unwrap_or("");
            let addr: std::net::Ipv4Addr = val.parse().map_err(|_| errorf!("invalid service destination address"))?;
            if addr.is_unspecified() || addr.is_multicast() {
                return Err(errorf!("invalid service destination address"));
            }
            // nft's JSON exports whole seconds.
            let expires = elem.get("expires").and_then(|v| v.as_i64()).unwrap_or(0);
            if expires <= 0 || expires > 4 * 3600 {
                continue;
            }
            let remaining = Duration::from_secs(expires as u64).saturating_sub(elapsed);
            // A second of headroom; entries never gain a fresh four hours.
            let seconds = remaining.as_secs() as i64 - 1;
            if seconds > 0 {
                values.push(format!("{} timeout {}s", addr, seconds));
            }
        }
        if !values.is_empty() {
            values.sort();
            out.push_str(&format!("add element inet minimalrouter {} {{ {} }}\n", name, values.join(", ")));
        }
    }
    Ok(out.into_bytes())
}

/// Replaces the helper-owned table in one atomic nft batch: delete and create
/// share a netlink transaction, so there is no fail-open interval.
pub(crate) fn run_nft_file(path: &str, check_only: bool) -> Result<()> {
    let config = std::fs::read(path)?;
    if config.len() > MAX_REQUEST_BYTES {
        return Err(errorf!("nftables candidate is too large"));
    }
    let config = bound_service_sets(&config);
    let mut batch = config.clone();
    if run_fixed("/usr/sbin/nft", &["list", "table", "inet", "minimalrouter"]).is_ok() {
        batch = [b"delete table inet minimalrouter\n".as_slice(), &config].concat();
        if !check_only {
            let preserved = preserve_service_destinations(&config)?;
            batch.push(b'\n');
            batch.extend(preserved);
        }
    }
    let tmp = std::path::Path::new(SOCKET_DIR).join(format!("nft-batch-{}", crate::auth::generate_random_hex(6)?));
    let tmp_str = tmp.to_string_lossy().into_owned();
    let result = (|| {
        std::fs::write(&tmp, &batch)?;
        crate::config::set_mode(&tmp_str, 0o600)?;
        if check_only { run_fixed("/usr/sbin/nft", &["-c", "-f", &tmp_str]) } else { run_fixed("/usr/sbin/nft", &["-f", &tmp_str]) }
    })();
    let _ = std::fs::remove_file(&tmp);
    result
}

/// Interface replacement can leave dnsmasq without its tunnel bindings;
/// reconcile DNS after restarting tunnels, never masking a tunnel error.
pub(crate) fn restart_wireguard_services(cfg: &SystemConfig) -> Result<()> {
    let tunnel = (|| -> Result<()> {
        activate_wireguard(cfg).map_err(|e| wrapf("restart WireGuard server", e))?;
        activate_wireguard_client(cfg, DEFAULT_COMMAND_TIMEOUT).map_err(|e| wrapf("restart WireGuard client", e))?;
        if cfg.wireguard.enabled {
            verify_restarted_wireguard_address(&wireguard_interface_name(&cfg.wireguard), &cfg.wireguard.address)
                .map_err(|e| wrapf("WireGuard server unavailable after restart", e))?;
        }
        if cfg.wg_client.enabled {
            verify_restarted_wireguard_address(&wireguard_client_interface_name(&cfg.wg_client), &cfg.wg_client.address)
                .map_err(|e| wrapf("WireGuard client unavailable after restart", e))?;
        }
        Ok(())
    })();
    let dns = (|| -> Result<()> {
        run_fixed("/sbin/rc-service", &["dnsmasq", "restart"]).map_err(|e| wrapf("restart DNS after WireGuard", e))?;
        run_fixed("/sbin/rc-service", &["dnsmasq", "status"]).map_err(|e| wrapf("DNS unhealthy after WireGuard restart", e))?;
        let mut address = "127.0.0.1".to_string();
        if cfg.wireguard.enabled {
            match net::parse_cidr(&cfg.wireguard.address) {
                Some((ip, _)) if ip.to4().is_some() => address = ip.to_string(),
                _ => return Err(errorf!("invalid trusted WireGuard DNS address")),
            }
        }
        verify_wireguard_dns_listener(&address).map_err(|e| wrapf(format!("DNS listener unavailable at {} after WireGuard restart", address), e))
    })();
    let errs: Vec<String> = [tunnel.err(), dns.err()].into_iter().flatten().map(|e| e.to_string()).collect();
    if errs.is_empty() { Ok(()) } else { Err(errorf!("{}", errs.join("\n"))) }
}

fn verify_restarted_wireguard_address(iface: &str, address: &str) -> Result<()> {
    let text = run_fixed_output("/sbin/ip", &["-o", "-4", "address", "show", "dev", iface])?;
    if address.is_empty() {
        return Ok(());
    }
    let want = match net::parse_cidr(address) {
        Some((ip, n)) if ip.to4().is_some() => (ip.to_string(), n.mask.size().0),
        _ => return Err(errorf!("invalid trusted WireGuard interface address")),
    };
    let fields: Vec<&str> = text.split_whitespace().collect();
    for pair in fields.windows(2) {
        if pair[0] == "inet" {
            if let Some((ip, n)) = net::parse_cidr(pair[1]) {
                if (ip.to_string(), n.mask.size().0) == want {
                    return Ok(());
                }
            }
        }
    }
    Err(errorf!("{} lacks configured address {}", iface, address))
}

/// A random single-label A question answered locally (domain-needed) proves
/// a DNS response at this exact address without upstream availability.
fn verify_wireguard_dns_listener(address: &str) -> Result<()> {
    let deadline = Instant::now() + Duration::from_secs(4);
    let target = net::join_host_port(address, "53");
    loop {
        let err = match probe_wireguard_dns(&target) {
            Ok(()) => return Ok(()),
            Err(e) => e,
        };
        if Instant::now() >= deadline {
            return Err(err);
        }
        std::thread::sleep(Duration::from_millis(150));
    }
}

fn probe_wireguard_dns(target: &str) -> Result<()> {
    let nonce = crate::goish::rand::bytes(10).map_err(|e| errorf!("{}", e))?;
    let id = u16::from_be_bytes([nonce[0], nonce[1]]);
    let name = format!("mr-ready-{}", hex::encode(&nonce[2..]));
    let query = dnswire::build_query(id, &name, dnswire::TYPE_A, true)?;
    let reply = dnswire::exchange(target, &query, Duration::from_millis(500))?;
    if reply.len() < query.len()
        || reply[..2] != query[..2]
        || reply[2] & 0xfa != 0x80
        || u16::from_be_bytes([reply[4], reply[5]]) != 1
        || reply[12..query.len()] != query[12..]
    {
        return Err(errorf!("invalid DNS readiness response"));
    }
    let code = reply[3] & 15;
    if code != 0 && code != 3 {
        return Err(errorf!("DNS readiness response code {}", code));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn verification_plan_scopes_optional_checks() {
        let base = crate::config::default_config();
        let mut candidate = base.clone();
        candidate.system.hostname = "edge".into();
        assert_eq!(verification_plan(&op::apply_all(), Some(&base), &candidate), VerificationPlan::default());
        candidate.squid_proxy.port = 3129;
        assert!(verification_plan(&op::apply_all(), Some(&base), &candidate).squid);
        assert!(!verification_plan(&op::reconcile(), Some(&base), &candidate).squid);
    }

    #[test]
    fn stale_routes_are_normalised() {
        let old = vec!["10.8.0.2/32".to_string(), "10.8.0.3".to_string(), "10.8.0.3/32".to_string()];
        let new = vec!["10.8.0.2/32".to_string()];
        assert_eq!(removed_allowed_ips(&old, &new), vec!["10.8.0.3/32".to_string()]);
    }

    #[test]
    fn service_destinations_render_remaining_lifetimes() {
        let raw = br#"{"nftables":[{"set":{"family":"inet","table":"minimalrouter","name":"svc_youtube","type":"ipv4_addr","elem":[{"elem":{"val":"142.250.1.1","expires":120}},{"elem":{"val":"142.250.1.2","expires":99999}}]}}]}"#;
        let wanted: HashSet<String> = ["svc_youtube".to_string()].into();
        let batch = service_destination_batch(raw, &wanted, Duration::from_secs(10)).unwrap();
        assert_eq!(String::from_utf8(batch).unwrap(), "add element inet minimalrouter svc_youtube { 142.250.1.1 timeout 109s }\n");
    }

    #[test]
    fn ifb_detection() {
        assert!(is_ifb_interface(r#"[{"ifname":"ifb0","linkinfo":{"info_kind":"ifb"}}]"#));
        assert!(!is_ifb_interface(r#"[{"ifname":"ifb0","linkinfo":{"info_kind":"dummy"}}]"#));
    }
}
