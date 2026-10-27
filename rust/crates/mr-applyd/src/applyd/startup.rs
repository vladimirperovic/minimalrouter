//! Boot-time reconciliation: restore the confirmed runtime from last-good, or
//! the setup-only first-run LAN, and fail closed when the core cannot start.

use std::time::Duration;

use super::runtime::*;
use super::state::{clear_pending_confirmation, load_last_good, pending_confirmation_exists};
use super::sys::*;
use crate::config::{SystemConfig, default_config};
use crate::errorf;
use crate::goish::errors::{Result, wrapf};
use crate::goish::net;

pub(crate) const STARTUP_RECONCILE_ENV: &str = "MINIMALROUTER_APPLYD_STARTUP_RECONCILE";

const EMERGENCY_NFTABLES: &str = "table inet minimalrouter {
    chain input {
        type filter hook input priority filter; policy drop;
        iifname \"lo\" accept
        ct state established,related accept
    }
    chain forward {
        type filter hook forward priority filter; policy drop;
    }
    chain output {
        type filter hook output priority filter; policy accept;
    }
}
";

/// Hooks keep reconciliation testable without touching host networking.
pub(crate) struct StartupHooks<'a> {
    pub load_last_good: &'a dyn Fn() -> Result<SystemConfig>,
    pub pending_exists: &'a dyn Fn() -> Result<bool>,
    pub restore_runtime: &'a dyn Fn(&SystemConfig) -> Result<()>,
    pub restore_first_run: Option<&'a dyn Fn() -> Result<()>>,
    pub clear_pending: &'a dyn Fn() -> Result<()>,
}

/// Runs only for the installed OpenRC service (opt-in environment variable).
pub(crate) fn run_if_requested() {
    if std::env::var(STARTUP_RECONCILE_ENV).as_deref() != Ok("1") {
        return;
    }
    if let Err(err) = super::acquire_daemon_guard() {
        // Migration owns the stopped runtime; not even the emergency firewall
        // is rewritten while its durable marker is present.
        log::error!("applyd offline migration admission failed: {}", err);
        std::process::exit(1);
    }
    if let Err(err) = harden_process() {
        fail_closed_startup(&SystemConfig::default());
        log::error!("applyd startup hardening failed closed: {}", err);
        std::process::exit(1);
    }
    let hooks = StartupHooks {
        load_last_good: &load_last_good,
        pending_exists: &pending_confirmation_exists,
        restore_runtime: &restore_last_good_runtime,
        restore_first_run: Some(&restore_first_run_runtime),
        clear_pending: &clear_pending_confirmation,
    };
    if let Err(err) = reconcile_startup(&hooks) {
        // Only security/core dataplane failures reach this point.
        fail_closed_startup(&SystemConfig::default());
        log::error!("applyd startup reconciliation failed closed: {}", err);
        std::process::exit(1);
    }
}

pub(crate) fn reconcile_startup(h: &StartupHooks<'_>) -> Result<()> {
    let cfg = match (h.load_last_good)() {
        Err(err) if is_not_found(&err) => {
            let pending = (h.pending_exists)().map_err(|e| wrapf("inspect pending confirmation", e))?;
            // Missing last-good is the first-run state and the recovery target
            // after a power loss between provisional setup and the SQLite commit.
            if let Some(restore_first_run) = h.restore_first_run {
                restore_first_run().map_err(|e| wrapf("restore first-run runtime", e))?;
                if pending {
                    log::info!("discarding provisional setup state without canonical last-good; first-run runtime restored");
                    match (h.clear_pending)() {
                        Err(e) if !is_not_found(&e) => return Err(wrapf("clear provisional first-run state", e)),
                        _ => {}
                    }
                }
                return Ok(());
            }
            if pending {
                return Err(errorf!("pending configuration exists without a recoverable last-good configuration"));
            }
            return Ok(());
        }
        Err(err) => return Err(wrapf("load last-good configuration", err)),
        Ok(cfg) => cfg,
    };
    // Load-bearing: generators do not reject a structurally broken config.
    cfg.validate().map_err(|e| wrapf("last-good configuration is invalid", e))?;
    cfg.validate_scenario_safety().map_err(|e| wrapf("last-good scenario safety is invalid", e))?;
    (h.restore_runtime)(&cfg).map_err(|e| wrapf("restore confirmed runtime", e))?;
    match (h.clear_pending)() {
        Err(e) if !is_not_found(&e) => Err(wrapf("clear stale pending confirmation", e)),
        _ => Ok(()),
    }
}

struct FailClosed<'a> {
    cfg: &'a SystemConfig,
    armed: bool,
}

impl Drop for FailClosed<'_> {
    fn drop(&mut self) {
        if self.armed {
            fail_closed_startup(self.cfg);
        }
    }
}

/// Exposes only the local setup plane: forwarding stays off and WAN/optional
/// services stay stopped until the wizard commits configuration and credential.
pub(crate) fn restore_first_run_runtime() -> Result<()> {
    let mut cfg = default_config();
    cfg.wan.enabled = false;
    cfg.wireguard.enabled = false;
    cfg.wg_client.enabled = false;
    cfg.wifi.enabled = false;
    cfg.cloudflare.ddns_enabled = false;
    cfg.cloudflare.tunnel_enabled = false;
    cfg.squid_proxy.enabled = false;
    cfg.qos.enabled = false;
    cfg.firewall.extra_lans = Default::default();
    std::fs::create_dir_all(SOCKET_DIR).map_err(|e| wrapf("create first-run runtime directory", e))?;
    let generated = generate_artifacts(&cfg).map_err(|e| wrapf("generate first-run artifacts", e))?;
    let dir = CandidateDir::create("first-run-candidate-").map_err(|e| wrapf("create first-run candidate directory", e))?;
    let candidates = write_candidates(&dir.0, &generated)?;
    run_nft_file(&candidates["nftables"], true).map_err(|e| wrapf("first-run nftables preflight", e))?;
    run_fixed("/usr/sbin/dnsmasq", &["--test", &format!("--conf-file={}", candidates["dnsmasq"])]).map_err(|e| wrapf("first-run dnsmasq preflight", e))?;
    let mut guard = FailClosed { cfg: &cfg, armed: true };
    // Optional credentials are not materialised before the administrator opts in.
    for name in ["dnsmasq", "adblock", "resolv-conf", "nftables"] {
        let item = &generated[name];
        atomic_write(item.path, &item.data, item.mode).map_err(|e| wrapf(format!("install first-run {}", name), e))?;
    }
    prepare_dnsmasq_lease_state().map_err(|e| wrapf("prepare first-run DHCP lease state", e))?;
    apply_kernel_hardening(&cfg)?;
    run_fixed("/sbin/sysctl", &["-w", "net.ipv4.ip_forward=0"]).map_err(|e| wrapf("disable first-run forwarding", e))?;
    let _ = run_fixed("/sbin/rc-service", &["pppoe-wan", "stop"]);
    let _ = remove_wireguard("wg0");
    let _ = remove_wireguard("wg1");
    for service in ["hostapd", "inadyn", "squid"] {
        let _ = run_fixed("/sbin/rc-service", &[service, "stop"]);
    }
    configure_runtime_lan(&cfg).map_err(|e| wrapf("configure first-run LAN", e))?;
    run_nft_file(NFT_RUNTIME_PATH, false).map_err(|e| wrapf("load first-run nftables", e))?;
    run_fixed("/sbin/rc-service", &["dnsmasq", "restart"]).map_err(|e| wrapf("start first-run dnsmasq", e))?;
    verify_first_run_runtime(&cfg)?;
    guard.armed = false;
    Ok(())
}

fn verify_first_run_runtime(cfg: &SystemConfig) -> Result<()> {
    let forwarding = run_fixed_output("/sbin/sysctl", &["-n", "net.ipv4.ip_forward"]);
    if forwarding.map(|f| f.trim() != "0").unwrap_or(true) {
        return Err(errorf!("first-run IPv4 forwarding is not disabled"));
    }
    run_fixed("/usr/sbin/nft", &["list", "table", "inet", "minimalrouter"]).map_err(|e| wrapf("first-run nftables table unavailable", e))?;
    run_fixed("/sbin/rc-service", &["dnsmasq", "status"]).map_err(|e| wrapf("first-run dnsmasq unhealthy", e))?;
    let output = run_fixed_output("/sbin/ip", &["-4", "addr", "show", "dev", cfg.runtime_lan_interface()]);
    if !output.is_ok_and(|o| o.contains(&format!("inet {}/", cfg.lan.ip_address))) {
        return Err(errorf!("first-run LAN address is not active"));
    }
    Ok(())
}

/// Only security/core configuration must be proven before routing starts.
fn preflight_startup(cfg: &SystemConfig, candidates: &std::collections::BTreeMap<&'static str, String>) -> Result<()> {
    ensure_router_kernel_modules();
    run_nft_file(&candidates["nftables"], true).map_err(|e| wrapf("nftables", e))?;
    run_fixed("/usr/sbin/dnsmasq", &["--test", &format!("--conf-file={}", candidates["dnsmasq"])]).map_err(|e| wrapf("dnsmasq", e))?;
    if cfg.wan.enabled {
        run_fixed("/usr/sbin/pppd", &["dryrun", "file", &candidates["pppoe"]]).map_err(|e| wrapf("pppd", e))?;
    }
    if cfg.wireguard.enabled {
        if let Err(err) = preflight_wireguard(&candidates["wireguard-runtime"], DEFAULT_COMMAND_TIMEOUT) {
            if cfg.system.management_access == "wireguard_only" {
                return Err(wrapf("management WireGuard", err));
            }
            log::warn!("startup WireGuard server preflight degraded (LAN management remains available): {}", err);
        }
    }
    Ok(())
}

pub(crate) fn restore_last_good_runtime(cfg: &SystemConfig) -> Result<()> {
    std::fs::create_dir_all(SOCKET_DIR).map_err(|e| wrapf("create runtime directory", e))?;
    let generated = generate_artifacts(cfg).map_err(|e| wrapf("generate last-good artifacts", e))?;
    let dir = CandidateDir::create("startup-candidate-").map_err(|e| wrapf("create startup candidate directory", e))?;
    let candidates = write_candidates(&dir.0, &generated).map_err(|e| wrapf("write startup candidates", e))?;
    preflight_startup(cfg, &candidates).map_err(|e| wrapf("preflight last-good runtime", e))?;
    let mut guard = FailClosed { cfg, armed: true };
    prepare_dnsmasq_lease_state().map_err(|e| wrapf("prepare startup DHCP lease state", e))?;
    for name in RESTORE_ARTIFACTS {
        let item = &generated[name];
        atomic_write(item.path, &item.data, item.mode).map_err(|e| wrapf(format!("install startup {}", name), e))?;
    }
    apply_kernel_hardening(cfg)?;
    let _ = run_fixed("/sbin/rc-service", &["hostapd", "stop"]);
    configure_runtime_lan(cfg)?;
    // ExtraLAN is a service segment, not a prerequisite for the core LAN.
    for lan in cfg.firewall.extra_lans.iter().filter(|l| l.enabled) {
        let mut single = cfg.clone();
        single.firewall.extra_lans = vec![lan.clone()].into();
        if let Err(err) = configure_extra_lans(&single) {
            log::warn!("startup extra LAN {} degraded (non-fatal): {}", lan.interface, err);
        }
    }
    run_nft_file(NFT_RUNTIME_PATH, false).map_err(|e| wrapf("load startup nftables", e))?;
    // Forwarding only once the policy is loaded.
    enable_ip_forwarding()?;
    if let Err(err) = activate_wireguard(cfg) {
        if cfg.system.management_access == "wireguard_only" {
            return Err(wrapf("restore startup management WireGuard", err));
        }
        log::warn!("startup WireGuard server degraded (LAN management remains online): {}", err);
    }
    run_fixed("/sbin/rc-service", &["dnsmasq", "restart"]).map_err(|e| wrapf("restart startup dnsmasq", e))?;
    // PPPoE is the primary WAN dependency and starts before optional services.
    if cfg.wan.enabled {
        if let Err(err) = run_fixed("/sbin/rc-service", &["pppoe-wan", "restart"]) {
            log::warn!("startup PPPoE unavailable (LAN management remains online): {}", err);
        }
    } else {
        let _ = run_fixed("/sbin/rc-service", &["pppoe-wan", "stop"]);
    }
    if let Err(err) = activate_wireguard_client(cfg, Duration::from_secs(15)) {
        log::warn!("startup WireGuard client not brought up (non-fatal): {}", err);
    }
    if cfg.qos.enabled {
        if let Err(err) = apply_qos(cfg) {
            log::warn!("startup QoS not applied (non-fatal): {}", err);
        }
    } else {
        clear_qos(cfg);
    }
    if cfg.wifi.enabled {
        if let Err(err) = preflight_wifi(&cfg.wifi.interface) {
            log::warn!("startup Wi-Fi preflight degraded (wired LAN remains available): {}", err);
        } else if let Err(err) = run_fixed("/sbin/rc-service", &["hostapd", "restart"]) {
            log::warn!("startup Wi-Fi unavailable (wired LAN remains online): {}", err);
        }
    } else {
        let _ = run_fixed("/sbin/rc-service", &["hostapd", "stop"]);
    }
    if cfg.cloudflare.ddns_enabled {
        if let Err(err) = run_fixed("/usr/sbin/inadyn", &["--check-config", "-f", &candidates["cf-ddns"]]) {
            log::warn!("startup DDNS preflight degraded (non-fatal): {}", err);
            let _ = run_fixed("/sbin/rc-service", &["inadyn", "stop"]);
        } else if let Err(err) = secure_ddns_configuration() {
            log::warn!("startup DDNS unavailable: {}", err);
            let _ = run_fixed("/sbin/rc-service", &["inadyn", "stop"]);
        } else if let Err(err) = run_fixed("/sbin/rc-service", &["inadyn", "restart"]) {
            log::warn!("startup DDNS unavailable (non-fatal): {}", err);
        }
    } else {
        let _ = run_fixed("/sbin/rc-service", &["inadyn", "stop"]);
    }
    if cfg.squid_proxy.enabled {
        if let Err(err) = run_fixed("/usr/sbin/squid", &["-k", "parse", "-f", &candidates["squid"]]) {
            log::warn!("startup Squid preflight degraded (non-fatal): {}", err);
            let _ = run_fixed("/sbin/rc-service", &["squid", "stop"]);
        } else if let Err(err) = lookup_group("squid") {
            log::warn!("startup Squid unavailable: service group missing: {}", err);
            let _ = run_fixed("/sbin/rc-service", &["squid", "stop"]);
        } else if lookup_group("squid").map(|gid| chown("/etc/squid/passwd", 0, gid).is_err()).unwrap_or(true) {
            log::warn!("startup Squid unavailable: could not secure password-file ownership");
            let _ = run_fixed("/sbin/rc-service", &["squid", "stop"]);
        } else if let Err(err) = run_fixed("/sbin/rc-service", &["squid", "restart"]) {
            log::warn!("startup Squid unavailable (non-fatal): {}", err);
        }
    } else if let Err(err) = stop_squid() {
        log::warn!("startup Squid stop failed: {}", err);
    }
    verify_startup_local(cfg)?;
    guard.armed = false;
    Ok(())
}

/// Proves only security/core invariants; optional components are logged.
fn verify_startup_local(cfg: &SystemConfig) -> Result<()> {
    let forwarding = run_fixed_output("/sbin/sysctl", &["-n", "net.ipv4.ip_forward"]);
    if forwarding.map(|f| f.trim() != "1").unwrap_or(true) {
        return Err(errorf!("startup IPv4 forwarding is not enabled"));
    }
    run_fixed("/usr/sbin/nft", &["list", "table", "inet", "minimalrouter"]).map_err(|e| wrapf("startup nftables table unavailable", e))?;
    run_fixed("/sbin/rc-service", &["dnsmasq", "status"]).map_err(|e| wrapf("startup dnsmasq unhealthy", e))?;
    let output =
        run_fixed_output("/sbin/ip", &["-4", "addr", "show", "dev", cfg.runtime_lan_interface()]).map_err(|e| wrapf("startup LAN interface unavailable", e))?;
    let prefix = cfg.lan.cidr.split('/').next().unwrap_or("");
    if !output.contains(&format!("inet {}/", prefix)) {
        return Err(errorf!("startup LAN address is not active"));
    }
    if cfg.wireguard.enabled {
        let iface = wireguard_interface_name(&cfg.wireguard);
        let wg = run_fixed("/usr/bin/wg", &["show", &iface]);
        let addr = run_fixed("/sbin/ip", &["-4", "addr", "show", "dev", &iface]);
        if wg.is_err() || addr.is_err() {
            if cfg.system.management_access == "wireguard_only" {
                return Err(errorf!("startup management WireGuard is unavailable"));
            }
            log::warn!(
                "startup WireGuard server degraded (non-fatal): wg={:?} address={:?}",
                wg.err().map(|e| e.to_string()),
                addr.err().map(|e| e.to_string())
            );
        }
    }
    if cfg.wg_client.enabled {
        let iface = wireguard_client_interface_name(&cfg.wg_client);
        if let Err(err) = run_fixed("/usr/bin/wg", &["show", &iface]) {
            log::warn!("startup WireGuard client interface not available (non-fatal): {}", err);
        } else if !cfg.wg_client.address.is_empty() {
            if let Err(err) = run_fixed("/sbin/ip", &["-4", "addr", "show", "dev", &iface]) {
                log::warn!("startup WireGuard client address unavailable (non-fatal): {}", err);
            }
        }
    }
    for lan in cfg.firewall.extra_lans.iter() {
        if !lan.enabled || lan.interface.is_empty() || lan.router_address.is_empty() {
            continue;
        }
        match run_fixed_output("/sbin/ip", &["-4", "addr", "show", "dev", &lan.interface]) {
            Err(err) => log::warn!("startup extra LAN {} unavailable (non-fatal): {}", lan.interface, err),
            Ok(out) => {
                if !out.contains(&format!("inet {}/", lan.router_address.split('/').next().unwrap_or(""))) {
                    log::warn!("startup extra LAN {} router address unavailable (non-fatal)", lan.interface);
                }
            }
        }
    }
    if cfg.wifi.enabled {
        if let Err(err) = run_fixed("/sbin/rc-service", &["hostapd", "status"]) {
            log::warn!("startup hostapd unhealthy (wired LAN remains available): {}", err);
        } else if !wifi_attached_to_bridge(&cfg.wifi.interface) {
            log::warn!("startup Wi-Fi interface is not attached to LAN bridge (non-fatal)");
        }
    }
    if cfg.squid_proxy.enabled {
        let port = if cfg.squid_proxy.port == 0 { 3128 } else { cfg.squid_proxy.port };
        let address = net::join_host_port(&cfg.lan.ip_address, &port.to_string());
        let ok = address.parse::<std::net::SocketAddr>().ok().is_some_and(|sa| std::net::TcpStream::connect_timeout(&sa, Duration::from_secs(2)).is_ok());
        if !ok {
            log::warn!("startup Squid listener unavailable (non-fatal)");
        }
        if let Err(err) = run_fixed("/sbin/rc-service", &["squid", "status"]) {
            log::warn!("startup Squid service unhealthy (non-fatal): {}", err);
        }
    }
    Ok(())
}

/// Never deletes the last known firewall while failing closed: if the
/// emergency batch cannot install, the previous table is safer than ACCEPT.
fn install_emergency_firewall() -> Result<()> {
    std::fs::create_dir_all(SOCKET_DIR)?;
    let path = format!("{}/fail-closed.nft", SOCKET_DIR);
    atomic_write(&path, EMERGENCY_NFTABLES.as_bytes(), 0o600)?;
    run_nft_file(&path, false)
}

pub(crate) fn fail_closed_startup(cfg: &SystemConfig) {
    let _ = run_fixed("/sbin/sysctl", &["-w", "net.ipv4.ip_forward=0"]);
    if let Err(err) = install_emergency_firewall() {
        log::error!("CRITICAL: could not install emergency fail-closed firewall; preserving the existing nftables state: {}", err);
    }
    // The management API must not stay alive behind uncertain network state.
    let _ = run_fixed("/sbin/rc-service", &["routerd", "stop"]);
    let _ = remove_wireguard(&wireguard_interface_name(&cfg.wireguard));
    let _ = remove_wireguard(&wireguard_client_interface_name(&cfg.wg_client));
    for service in ["dnsmasq", "pppoe-wan", "hostapd", "inadyn", "squid"] {
        let _ = run_fixed("/sbin/rc-service", &[service, "stop"]);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::cell::RefCell;

    #[test]
    fn rejects_invalid_last_good_and_restores_first_run() {
        let mut broken = default_config();
        broken.lan.cidr = "invalid".into();
        let restored = RefCell::new(false);
        let hooks = StartupHooks {
            load_last_good: &|| Ok(broken.clone()),
            pending_exists: &|| Ok(false),
            restore_runtime: &|_| {
                *restored.borrow_mut() = true;
                Ok(())
            },
            restore_first_run: None,
            clear_pending: &|| Ok(()),
        };
        assert!(reconcile_startup(&hooks).unwrap_err().to_string().starts_with("last-good configuration is invalid"));
        assert!(!*restored.borrow());

        let first_run = RefCell::new(0);
        let missing = || Err(crate::goish::errors::Error::new(std::io::Error::from(std::io::ErrorKind::NotFound)));
        let hooks = StartupHooks {
            load_last_good: &missing,
            pending_exists: &|| Ok(true),
            restore_runtime: &|_| Ok(()),
            restore_first_run: Some(&|| {
                *first_run.borrow_mut() += 1;
                Ok(())
            }),
            clear_pending: &|| Ok(()),
        };
        reconcile_startup(&hooks).unwrap();
        assert_eq!(*first_run.borrow(), 1);
    }
}
