//! router-setup: the root-only first-run console. `collect` interviews the
//! operator, `apply` commits the reviewed configuration (only after a real
//! PPPoE session when credentials were given), `verify` proves provisioning.

#![forbid(unsafe_code)]

use std::io::{BufRead, IsTerminal, Write};
use std::path::Path;
use std::sync::Arc;
use std::time::{Duration as StdDuration, Instant};

use serde::{Deserialize, Serialize};

use minimalrouter::apply::Engine;
use minimalrouter::config::{self, SQLiteStore, SystemConfig};
use minimalrouter::goish::errors::{is, Result};
use minimalrouter::goish::flag::{geteuid, ErrorHandling, FlagSet};
use minimalrouter::goish::time::Time;
use minimalrouter::network::{self, InterfaceInfo, RoleRecommendation};
use minimalrouter::{auth, errorf};

const DEFAULT_LAN_IP: &str = "192.168.1.1";

#[derive(Serialize, Deserialize, Default)]
#[serde(default)]
struct Provision {
    wan_interface: String,
    lan_interface: String,
    pppoe_username: String,
    pppoe_password: String,
    admin_password: String,
    lan_ip_address: String,
}

struct ConsoleUI {
    color: bool,
}

const BANNER: &str = r"           _       _                 _                 _
 _ __ ___ (_)_ __ (_)_ __ ___   __ _| |_ __ ___  _   _| |_ ___ _ __
| '_ ` _ \| | '_ \| | '_ ` _ \ / _` | | '__/ _ \| | | | __/ _ \ '__|
| | | | | | | | | | | | | | | | (_| | | | | (_) | |_| | ||  __/ |
|_| |_| |_|_|_| |_|_|_| |_| |_|\__,_|_|_|  \___/ \__,_|\__\___|_|";

fn print_banner() {
    for line in BANNER.lines() {
        println!("{}", line);
    }
    println!("  first-run console setup");
    println!("  -----------------------");
}

fn fatal(message: impl std::fmt::Display) -> ! {
    eprintln!("ERROR: {}", message);
    std::process::exit(1);
}

fn usage() -> ! {
    eprintln!("Usage: router-setup collect --output <file> [--data-dir <dir>] | router-setup apply --input <file> [--data-dir <dir>] | router-setup verify [--data-dir <dir>]");
    std::process::exit(2);
}

fn terminal_color() -> bool {
    std::io::stdout().is_terminal() && std::env::var("TERM").as_deref() != Ok("dumb") && std::env::var("NO_COLOR").unwrap_or_default().is_empty()
}

impl ConsoleUI {
    fn mark(&self, colored: &'static str, plain: &'static str) -> &'static str {
        if self.color { colored } else { plain }
    }

    fn ok(&self, message: &str) {
        println!("{} {}", self.mark("\x1b[32m●\x1b[0m", "[OK]"), message);
    }

    fn warn(&self, message: &str) {
        println!("{} {}", self.mark("\x1b[33m●\x1b[0m", "[WARN]"), message);
    }

    fn fail(&self, message: &str) {
        println!("{} {}", self.mark("\x1b[31m●\x1b[0m", "[FAIL]"), message);
    }

    fn read_line(&self, prompt: &str) -> Result<String> {
        print!("{}", prompt);
        let _ = std::io::stdout().flush();
        let mut line = String::new();
        let n = std::io::stdin().lock().read_line(&mut line)?;
        if n == 0 {
            return Err(errorf!("EOF"));
        }
        Ok(line.trim_end_matches(['\r', '\n']).to_string())
    }

    /// Echo is disabled with stty only on a real terminal.
    fn read_secret(&self, prompt: &str) -> Result<String> {
        print!("{}", prompt);
        let _ = std::io::stdout().flush();
        let hidden = std::io::stdin().is_terminal() && run_quiet_with_stdin("stty", &["-echo"]);
        let mut line = String::new();
        let read = std::io::stdin().lock().read_line(&mut line);
        if hidden {
            run_quiet_with_stdin("stty", &["echo"]);
        }
        println!();
        if read? == 0 {
            return Err(errorf!("EOF"));
        }
        Ok(line.trim_end_matches(['\r', '\n']).to_string())
    }

    fn confirm(&self, prompt: &str, default_yes: bool) -> Result<bool> {
        let suffix = if default_yes { " [Y/n]: " } else { " [y/N]: " };
        loop {
            match self.read_line(&format!("{}{}", prompt, suffix))?.trim().to_lowercase().as_str() {
                "" => return Ok(default_yes),
                "y" | "yes" => return Ok(true),
                "n" | "no" => return Ok(false),
                _ => self.warn("Please answer y or n."),
            }
        }
    }

    fn select_interface(&self, label: &str, items: &[InterfaceInfo], excluded: &str) -> Result<String> {
        println!();
        println!("{}:", label);
        let choices: Vec<&InterfaceInfo> = items.iter().filter(|i| i.name != excluded).collect();
        for (index, item) in choices.iter().enumerate() {
            println!("  {}) {:<10} MAC {:<17} link={}", index + 1, item.name, fallback(&item.mac_address, "unknown"), if item.carrier { "yes" } else { "no" });
        }
        if choices.is_empty() {
            return Err(errorf!("no selectable interface remains"));
        }
        loop {
            let value = self.read_line("Choice: ")?;
            if let Some(index) = config::go_atoi(&value).filter(|i| *i >= 1 && *i as usize <= choices.len()) {
                return Ok(choices[index as usize - 1].name.clone());
            }
            self.fail("Enter one of the listed numbers.");
        }
    }
}

fn fallback(value: &str, other: &str) -> String {
    if value.trim().is_empty() { other.into() } else { value.into() }
}

fn run_quiet(program: &str, args: &[&str]) -> bool {
    std::process::Command::new(program).args(args).stdout(std::process::Stdio::null()).stderr(std::process::Stdio::null()).status().is_ok_and(|s| s.success())
}

fn run_quiet_with_stdin(program: &str, args: &[&str]) -> bool {
    std::process::Command::new(program).args(args).stdin(std::process::Stdio::inherit()).stdout(std::process::Stdio::null()).stderr(std::process::Stdio::null()).status().is_ok_and(|s| s.success())
}

/// exec.LookPath.
fn look_path(program: &str) -> bool {
    std::env::var_os("PATH").is_some_and(|paths| std::env::split_paths(&paths).any(|dir| dir.join(program).is_file()))
}

/// A PADO answer only proves a concentrator replied; no session is started.
fn discover_pppoe(iface: &str) -> (bool, String) {
    if !look_path("pppoe-discovery") {
        return (false, "pppoe-discovery unavailable".into());
    }
    let output = minimalrouter::proc::run("pppoe-discovery", &["-I", iface], StdDuration::from_secs(5));
    let text = String::from_utf8_lossy(&output.combined()).into_owned();
    let lower = text.to_lowercase();
    if !(lower.contains("access-concentrator") || lower.contains("ac-ethernet-address") || lower.contains("pado")) {
        return (false, String::new());
    }
    for line in text.lines() {
        let trimmed = line.trim();
        if trimmed.to_lowercase().starts_with("access-concentrator:") {
            if let Some((_, value)) = trimmed.split_once(':') {
                return (true, value.trim().to_string());
            }
        }
    }
    (true, String::new())
}

fn recommend_roles(rec: &RoleRecommendation, probes: &std::collections::HashMap<String, bool>) -> (String, String, String) {
    let found: Vec<&str> = rec.interfaces.iter().filter(|i| probes.get(&i.name).copied().unwrap_or(false)).map(|i| i.name.as_str()).collect();
    let mut wan = rec.wan.clone();
    let reason = match found.len() {
        1 => {
            wan = found[0].to_string();
            "only this interface answered PPPoE discovery"
        }
        0 => "no PPPoE discovery response; falling back to link/default-route hardware scoring",
        _ => "PPPoE answered on multiple interfaces; manual confirmation is required",
    };
    let mut lan = rec.lan.clone();
    if lan == wan {
        lan = rec.interfaces.iter().find(|i| i.name != wan && i.physical).or_else(|| rec.interfaces.iter().find(|i| i.name != wan)).map(|i| i.name.clone()).unwrap_or_default();
    }
    (wan, lan, reason.into())
}

fn write_provision(path: &str, provision: &Provision) -> Result<()> {
    if let Some(parent) = Path::new(path).parent() {
        std::fs::create_dir_all(parent)?;
    }
    let data = minimalrouter::goish::json::marshal(provision)?;
    let temp = format!("{}.tmp-{}", path, std::process::id());
    let result = (|| -> Result<()> {
        let mut options = std::fs::OpenOptions::new();
        options.write(true).create(true).truncate(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        options.open(&temp)?.write_all(&data)?;
        config::set_mode(&temp, 0o600)?;
        std::fs::rename(&temp, path)?;
        Ok(())
    })();
    if result.is_err() {
        let _ = std::fs::remove_file(&temp);
    }
    result
}

fn data_dir_flag(fs: &mut FlagSet) {
    fs.string("data-dir", "/var/lib/minimalrouter", "canonical routerd data directory");
}

fn admin_configured(store: &SQLiteStore) -> Result<bool> {
    match store.get_admin_hash() {
        Ok(_) => Ok(true),
        Err(err) if is::<config::ErrNoRows>(&err) => Ok(false),
        Err(err) => Err(err),
    }
}

fn collect(args: &[String]) -> Result<()> {
    let mut fs = FlagSet::new("collect", ErrorHandling::Continue);
    fs.discard_output();
    fs.string("output", "/run/minimalrouter-console-setup.json", "root-only provisioning file");
    data_dir_flag(&mut fs);
    fs.parse(args)?;
    let data_dir = fs.get("data-dir");
    if Path::new(&data_dir).join("minimalrouter.db").exists() {
        let store = SQLiteStore::new_file_store(&data_dir).map_err(|e| errorf!("inspect existing configuration store: {}", e))?;
        if admin_configured(&store).map_err(|e| errorf!("inspect administrator state: {}", e))? {
            println!("Minimal Router OS is already configured; console first-run setup is skipped.");
            return Ok(());
        }
    }
    let ui = ConsoleUI { color: terminal_color() };
    println!();
    if std::env::var("MINIMALROUTER_WELCOME_SHOWN").as_deref() != Ok("1") {
        print_banner();
        println!();
    }
    println!("Setup asks only for what it needs:");
    println!("  1. WAN/LAN — the installer suggests likely ports and explains why");
    println!("  2. PPPoE — optional; press Enter to skip it and configure it later");
    println!("  3. Dashboard password — required, minimum 12 characters");
    println!("  4. A final review before the configuration is saved");
    println!();
    println!("Tip: [Y/n] means Yes is the default; just press Enter to accept it.");
    println!();
    let recommendation = network::discover().map_err(|e| errorf!("discover interfaces: {}", e))?;
    println!();
    println!("Testing network interfaces for a PPPoE access concentrator...");
    let physical: Vec<InterfaceInfo> = recommendation.interfaces.iter().filter(|i| i.physical).cloned().collect();
    let candidates: Vec<InterfaceInfo> = if physical.len() < 2 { recommendation.interfaces.to_vec() } else { physical };
    let mut probes = std::collections::HashMap::new();
    for item in &candidates {
        if !item.up {
            run_quiet("ip", &["link", "set", "dev", &item.name, "up"]);
        }
        let (found, detail) = discover_pppoe(&item.name);
        probes.insert(item.name.clone(), found);
        if found {
            let detail = if detail.trim().is_empty() { String::new() } else { format!(" ({})", detail.trim()) };
            ui.ok(&format!("{}  PPPoE concentrator found{}", item.name, detail));
        } else if item.carrier {
            ui.warn(&format!("{}  link detected, no PPPoE concentrator response", item.name));
        } else {
            ui.fail(&format!("{}  no carrier / no PPPoE response", item.name));
        }
    }
    let (mut wan, mut lan, reason) = recommend_roles(&recommendation, &probes);
    let (mut wan_mac, mut lan_mac, mut strong) = ("unknown".to_string(), "unknown".to_string(), false);
    for item in recommendation.interfaces.iter() {
        if item.name == wan {
            wan_mac = fallback(&item.mac_address, "unknown");
            strong = strong || item.default_route || item.carrier;
        }
        if item.name == lan {
            lan_mac = fallback(&item.mac_address, "unknown");
            strong = strong || item.carrier;
        }
    }
    println!();
    println!("Suggested WAN: {}  (MAC {})", wan, wan_mac);
    println!("Suggested LAN: {}  (MAC {})", lan, lan_mac);
    println!("Reason: {}", reason);
    let pppoe_based = reason == "only this interface answered PPPoE discovery" || reason.starts_with("PPPoE answered on multiple interfaces");
    let low_confidence = !pppoe_based && !strong;
    if pppoe_based {
        println!();
        ui.warn("PPPoE discovery only detects that a service answered; it does not start a PPPoE session.");
        ui.warn("An existing router or shared upstream can make PPPoE visible during migration.");
        println!("Please explicitly confirm the WAN/LAN roles; pressing Enter alone will NOT accept this PPPoE-based suggestion.");
    }
    if low_confidence {
        println!();
        ui.warn("The installer does not have enough link evidence to identify WAN/LAN with high confidence.");
        ui.warn("Check the MAC addresses against the two Proxmox NICs before confirming.");
        println!("Please explicitly confirm the roles; pressing Enter alone will NOT accept this low-confidence suggestion.");
    }
    if !ui.confirm("Use these WAN/LAN roles?", !pppoe_based && !low_confidence)? {
        wan = ui.select_interface("Select WAN", &candidates, "")?;
        if candidates.len() == 2 {
            lan = candidates.iter().find(|c| c.name != wan).map(|c| c.name.clone()).unwrap_or_default();
            println!("LAN: {} (the only other interface)", lan);
        } else {
            lan = ui.select_interface("Select LAN", &candidates, &wan)?;
        }
    }
    if wan == lan || wan.is_empty() || lan.is_empty() {
        return Err(errorf!("WAN and LAN must be two different interfaces"));
    }
    println!();
    println!("WAN is {}; LAN is {}.", wan, lan);
    println!("PPPoE credentials are optional during installation. Leave the username empty to configure them later in the Dashboard.");
    let pppoe_user = ui.read_line("PPPoE username [skip]: ")?;
    let mut pppoe_pass = String::new();
    if !pppoe_user.is_empty() {
        pppoe_pass = ui.read_secret("PPPoE password: ")?;
        if pppoe_pass.is_empty() {
            return Err(errorf!("PPPoE password cannot be empty when a username is supplied"));
        }
    }
    let admin_pass = loop {
        let candidate = ui.read_secret("Dashboard administrator password (minimum 12 characters): ")?;
        if candidate.chars().count() < 12 {
            ui.fail("Password is too short; use at least 12 characters.");
            continue;
        }
        if candidate != ui.read_secret("Confirm dashboard password: ")? {
            ui.fail("Passwords do not match.");
            continue;
        }
        break candidate;
    };
    println!();
    println!("Review");
    println!("------");
    println!("WAN:       {}", wan);
    println!("LAN:       {}", lan);
    println!("LAN IP:    {}/24", DEFAULT_LAN_IP);
    if pppoe_user.is_empty() {
        println!("PPPoE:     skipped");
    } else {
        println!("PPPoE:     {} (password hidden)", pppoe_user);
    }
    println!("Dashboard: https://192.168.1.1:8443");
    println!("Admin:     password set (hidden)");
    println!();
    if !ui.confirm("Install and apply this configuration?", true)? {
        return Err(errorf!("installation cancelled by operator"));
    }
    write_provision(&fs.get("output"), &Provision { wan_interface: wan, lan_interface: lan, pppoe_username: pppoe_user, pppoe_password: pppoe_pass, admin_password: admin_pass, lan_ip_address: DEFAULT_LAN_IP.into() })?;
    ui.ok("Console choices saved in volatile root-only memory; installing appliance files now.");
    Ok(())
}

/// The first `ppp*` interface with a non-loopback IPv4 address.
#[cfg(unix)]
fn ppp_ipv4() -> Option<(String, String)> {
    minimalrouter::sys::interface_ipv4_addresses()
        .ok()?
        .into_iter()
        .find(|(name, ip)| name.to_lowercase().starts_with("ppp") && !ip.is_loopback())
        .map(|(name, ip)| (name, ip.to_string()))
}

#[cfg(not(unix))]
fn ppp_ipv4() -> Option<(String, String)> {
    None
}

fn wait_for_ppp_session(timeout: StdDuration) -> Option<(String, String)> {
    let deadline = Instant::now() + timeout;
    while Instant::now() < deadline {
        if let Some(found) = ppp_ipv4() {
            return Some(found);
        }
        std::thread::sleep(StdDuration::from_millis(500));
    }
    None
}

fn apply(args: &[String]) -> Result<()> {
    let mut fs = FlagSet::new("apply", ErrorHandling::Continue);
    fs.discard_output();
    fs.string("input", "/run/minimalrouter-console-setup.json", "root-only provisioning file");
    data_dir_flag(&mut fs);
    fs.bool("offline", false, "write the configuration without applying the network; the first disk boot reconciles it");
    fs.parse(args)?;
    let data = std::fs::read(fs.get("input")).map_err(|e| errorf!("read provisioning file: {}", e))?;
    let requested: Provision = minimalrouter::goish::json::unmarshal(&data).map_err(|e| errorf!("decode provisioning file: {}", e))?;
    if requested.wan_interface.is_empty() || requested.lan_interface.is_empty() || requested.wan_interface == requested.lan_interface {
        return Err(errorf!("provisioning file contains invalid WAN/LAN roles"));
    }
    if requested.admin_password.chars().count() < 12 {
        return Err(errorf!("provisioning file contains an invalid administrator password"));
    }
    if requested.pppoe_username.is_empty() != requested.pppoe_password.is_empty() {
        return Err(errorf!("provisioning file contains incomplete PPPoE credentials"));
    }
    let store = Arc::new(SQLiteStore::new_file_store(&fs.get("data-dir")).map_err(|e| errorf!("open canonical configuration store: {}", e))?);
    if admin_configured(&store).map_err(|e| errorf!("read administrator state: {}", e))? {
        return Err(errorf!("system is already configured; refusing to rerun first-run console setup"));
    }
    let initial = store.get_latest_config().map_err(|e| errorf!("read initial configuration: {}", e))?;
    let hashed = auth::hash_password(&requested.admin_password).map_err(|e| errorf!("hash administrator password: {}", e))?;
    let mut cfg = config::default_config();
    cfg.revision = initial.revision;
    cfg.cloudflare.ddns_enabled = false;
    cfg.cloudflare.tunnel_enabled = false;
    cfg.wifi.enabled = false;
    cfg.wan.interface = requested.wan_interface.clone();
    cfg.wan.username = requested.pppoe_username.clone();
    cfg.wan.password = requested.pppoe_password.clone();
    cfg.wan.enabled = !requested.pppoe_username.is_empty();
    cfg.lan.interface = requested.lan_interface.clone();
    if !requested.lan_ip_address.is_empty() && requested.lan_ip_address != cfg.lan.ip_address {
        return Err(errorf!("first-run LAN address must remain {}", cfg.lan.ip_address));
    }
    if fs.get_bool("offline") {
        // The live installer never runs the router stack: the first boot of
        // the installed disk reconciles this configuration natively.
        cfg.revision = initial.revision + 1;
        return store.commit_initial_setup(cfg, &hashed).map_err(|e| errorf!("store initial configuration for first boot: {}", e));
    }
    let engine = Engine::new(initial, Some(store.clone()));
    let session = std::sync::Mutex::new(None);
    let commit = |applied: &SystemConfig| -> Result<()> {
        // No credential is committed until the production PPPoE path has an
        // IPv4 session; an error restores the previous configuration.
        if !requested.pppoe_username.is_empty() {
            let found = wait_for_ppp_session(StdDuration::from_secs(60)).ok_or_else(|| errorf!("PPPoE authentication did not produce an IPv4 session within 60 seconds"))?;
            *session.lock().unwrap_or_else(|e| e.into_inner()) = Some(found);
        }
        store.commit_initial_setup(applied.clone(), &hashed)
    };
    let (tx, result) = engine.process_initial_setup(&format!("console-setup-{}", Time::now().unix_nano()), cfg, &commit);
    if let Err(err) = result {
        return Err(errorf!("initial setup failed in state {}: {}", tx.current_state.as_str(), err));
    }
    let ui = ConsoleUI { color: terminal_color() };
    ui.ok("Configuration applied and verified by router-applyd.");
    if let Some((iface, address)) = session.into_inner().unwrap_or_else(|e| e.into_inner()) {
        ui.ok(&format!("PPPoE connected on {} ({}).", iface, address));
    }
    ui.ok("Initial setup committed. The dashboard can now start at https://192.168.1.1:8443");
    Ok(())
}

/// Proves provisioning exists without creating a default database or
/// prompting again after a later firstboot step was interrupted.
fn verify_setup(args: &[String]) -> Result<()> {
    let mut fs = FlagSet::new("verify", ErrorHandling::Continue);
    fs.discard_output();
    fs.string("data-dir", "/var/lib/minimalrouter", "canonical data directory");
    fs.parse(args)?;
    let dir = fs.get("data-dir");
    let meta = std::fs::metadata(Path::new(&dir).join("minimalrouter.db"))?;
    if !meta.is_file() || meta.len() == 0 {
        return Err(errorf!("canonical database is missing"));
    }
    let store = SQLiteStore::new_file_store(&dir)?;
    store.get_latest_config()?;
    let hash = store.get_admin_hash()?;
    if !hash.starts_with("$argon2id$") {
        return Err(errorf!("administrator credentials are not configured"));
    }
    // Validates the stored encoding; a non-matching probe is expected.
    auth::verify_password("", &hash)?;
    Ok(())
}

fn main() {
    if geteuid() != 0 {
        fatal("router-setup must run as root");
    }
    let args: Vec<String> = std::env::args().collect();
    if args.len() < 2 {
        usage();
    }
    let result = match args[1].as_str() {
        "collect" => collect(&args[2..]),
        "apply" => apply(&args[2..]),
        "verify" => verify_setup(&args[2..]),
        _ => usage(),
    };
    if let Err(err) = result {
        fatal(err);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn roles_prefer_the_single_pppoe_answer() {
        let iface = |name: &str| InterfaceInfo { name: name.into(), physical: true, ..Default::default() };
        let rec = RoleRecommendation { wan: "eth0".into(), lan: "eth1".into(), interfaces: vec![iface("eth0"), iface("eth1")].into(), ..Default::default() };
        let probes = [("eth1".to_string(), true)].into();
        assert_eq!(recommend_roles(&rec, &probes), ("eth1".into(), "eth0".into(), "only this interface answered PPPoE discovery".into()));
        let none = std::collections::HashMap::new();
        assert_eq!(recommend_roles(&rec, &none).2, "no PPPoE discovery response; falling back to link/default-route hardware scoring");
        assert!(BANNER.lines().count() == 5 && BANNER.contains("| '_ ` _ \\|"));
    }
}
