//! router-recovery: the root-only local console for lockout recovery. It
//! edits canonical state directly (interfaces, credentials, snapshots,
//! factory reset, offline migration) and builds a sanitized support bundle.

#![forbid(unsafe_code)]

use std::io::{BufRead, Write};
use std::path::Path;
use std::sync::Arc;

use minimalrouter::config::SQLiteStore;
use minimalrouter::goish::flag::{ErrorHandling, FlagSet, geteuid};
use minimalrouter::goish::time::Time;
use minimalrouter::recovery::{self, Manager};
use minimalrouter::{errorf, network};

const DEFAULT_DATA_DIR: &str = "/var/lib/minimalrouter";

fn fatal(message: impl std::fmt::Display) -> ! {
    eprintln!("ERROR: {}", message);
    std::process::exit(1);
}

fn data_dir() -> String {
    std::env::var("MINIMALROUTER_DATA_DIR").ok().filter(|d| !d.is_empty()).unwrap_or_else(|| DEFAULT_DATA_DIR.into())
}

fn open_manager() -> Manager {
    let store = SQLiteStore::new(&data_dir()).unwrap_or_else(|e| fatal(e));
    Manager { store: Some(Arc::new(store)) }
}

/// Passwords only through stdin, never argv or shell history.
fn read_password(from_stdin: bool) -> String {
    if !from_stdin {
        fatal("password must be supplied through --password-stdin to avoid shell history exposure");
    }
    let mut line = String::new();
    if std::io::stdin().lock().read_line(&mut line).is_err() && line.is_empty() {
        fatal("EOF");
    }
    let password = line.trim_end_matches(['\r', '\n']).to_string();
    if password.is_empty() {
        fatal("password cannot be empty");
    }
    password
}

fn usage(w: &mut dyn Write) {
    let _ = writeln!(
        w,
        "Usage: router-recovery [command] [options]\nNo command opens the interactive local recovery console.\nCommands:\n  interfaces\n  reset-auth --password-stdin [--disable-totp]\n  set-wan --interface NAME\n  set-lan --interface NAME --cidr ADDRESS/PREFIX\n  snapshots\n  support-bundle [--output PATH]\n  restore-last-good\n  restore-snapshot --id SNAPSHOT --confirm RESTORE-SNAPSHOT\n  migrate-config --file CONFIG.json --confirm MIGRATE-CONFIG\n  factory-reset [--wan NAME --lan NAME] --password-stdin --confirm FACTORY-RESET\n  help"
    );
}

fn flags(name: &str) -> FlagSet {
    FlagSet::new(name, ErrorHandling::Exit)
}

fn show_interfaces() {
    match network::discover() {
        Err(err) => println!("ERROR: {}", err),
        Ok(rec) => {
            println!("Recommended WAN: {}\nRecommended LAN: {}", rec.wan, rec.lan);
            for item in rec.interfaces.iter() {
                println!(
                    "- {:<15} physical={} carrier={} up={} default-route={} mac={}",
                    item.name, item.physical, item.carrier, item.up, item.default_route, item.mac_address
                );
            }
            for warning in rec.warnings.iter() {
                println!("WARNING: {}", warning);
            }
        }
    }
}

fn list_snapshots() {
    match open_manager().list_snapshots() {
        Err(err) => println!("ERROR: {}", err),
        Ok(snapshots) => {
            for s in snapshots {
                println!("{} revision={} created={} checksum={}", s.id, s.revision, s.created_at, s.checksum);
            }
        }
    }
}

fn factory_reset(args: &[String]) {
    let mut fs = flags("factory-reset");
    fs.string("wan", "", "WAN interface");
    fs.string("lan", "", "LAN interface");
    fs.string("confirm", "", "must equal FACTORY-RESET");
    fs.bool("password-stdin", false, "read password from stdin");
    let _ = fs.parse(args);
    if fs.get("confirm") != "FACTORY-RESET" {
        fatal("factory reset requires --confirm FACTORY-RESET");
    }
    let (mut wan, mut lan) = (fs.get("wan"), fs.get("lan"));
    if wan.is_empty() || lan.is_empty() {
        let rec = network::discover().unwrap_or_else(|e| fatal(e));
        if wan.is_empty() {
            wan = rec.wan;
        }
        if lan.is_empty() {
            lan = rec.lan;
        }
    }
    let manager = open_manager();
    let snapshot = manager.factory_reset(&wan, &lan, &read_password(fs.get_bool("password-stdin"))).unwrap_or_else(|e| fatal(e));
    println!("Factory defaults stored. Recovery snapshot: {}", snapshot.id);
}

fn migrate_config_command(args: &[String]) -> minimalrouter::goish::errors::Result<()> {
    let mut fs = FlagSet::new("migrate-config", ErrorHandling::Continue);
    fs.string("file", "", "complete unredacted replacement configuration JSON");
    fs.string("confirm", "", "must equal MIGRATE-CONFIG");
    fs.parse(args)?;
    if fs.get("file").is_empty() || fs.get("confirm") != "MIGRATE-CONFIG" || fs.narg() != 0 {
        return Err(errorf!("offline migration requires --file CONFIG.json --confirm MIGRATE-CONFIG"));
    }
    let backup = recovery::migrate_config(&data_dir(), &fs.get("file"))
        .map_err(|e| errorf!("offline migration failed (do not remove its journal; retry with the same file): {}", e))?;
    println!("Offline migration committed. Private original-state backup: {}\nStart router-applyd, then routerd, and verify local connectivity.", backup);
    Ok(())
}

fn run_command(command: &str, args: &[String]) {
    match command {
        "migrate-config" => {
            if let Err(err) = migrate_config_command(args) {
                fatal(err);
            }
        }
        "interfaces" => show_interfaces(),
        "reset-auth" => {
            let mut fs = flags("reset-auth");
            fs.bool("disable-totp", false, "remove configured TOTP secret");
            fs.bool("password-stdin", false, "read new password from stdin");
            let _ = fs.parse(args);
            let manager = open_manager();
            if let Err(err) = manager.reset_authentication(&read_password(fs.get_bool("password-stdin")), fs.get_bool("disable-totp")) {
                fatal(err);
            }
            println!("Administrator credentials reset and all sessions revoked.");
        }
        "set-lan" => {
            let mut fs = flags("set-lan");
            fs.string("interface", "", "Linux LAN interface name");
            fs.string("cidr", "", "LAN host address/prefix");
            let _ = fs.parse(args);
            let snapshot = open_manager().set_lan(&fs.get("interface"), &fs.get("cidr")).unwrap_or_else(|e| fatal(e));
            println!("LAN stored. Undo snapshot: {}. Restart router services to reconcile.", snapshot.id);
        }
        "set-wan" => {
            let mut fs = flags("set-wan");
            fs.string("interface", "", "Linux WAN interface name");
            let _ = fs.parse(args);
            let snapshot = open_manager().set_wan(&fs.get("interface")).unwrap_or_else(|e| fatal(e));
            println!("WAN stored. Undo snapshot: {}. Restart router services to reconcile.", snapshot.id);
        }
        "snapshots" => list_snapshots(),
        "support-bundle" => {
            let mut fs = flags("support-bundle");
            fs.string("output", "", "output .tar.gz path (default: /tmp/minimalrouter-support-<timestamp>.tar.gz)");
            let _ = fs.parse(args);
            let path = create_support_bundle(&fs.get("output")).unwrap_or_else(|e| fatal(e));
            println!("Sanitized support bundle created: {}", path);
        }
        "restore-snapshot" => {
            let mut fs = flags("restore-snapshot");
            fs.string("id", "", "snapshot identifier");
            fs.string("confirm", "", "must equal RESTORE-SNAPSHOT");
            let _ = fs.parse(args);
            if fs.get("confirm") != "RESTORE-SNAPSHOT" {
                fatal("snapshot restore requires --confirm RESTORE-SNAPSHOT");
            }
            let undo = open_manager().restore_snapshot(&fs.get("id")).unwrap_or_else(|e| fatal(e));
            println!("Snapshot restored. Undo snapshot: {}. Restart router services.", undo.id);
        }
        "restore-last-good" => {
            let (undo, id) = open_manager().restore_latest_snapshot().unwrap_or_else(|e| fatal(e));
            println!("Restored latest verified snapshot {}. Undo snapshot: {}. Restart router services.", id, undo.id);
        }
        "factory-reset" => factory_reset(args),
        _ => {
            usage(&mut std::io::stderr());
            std::process::exit(2);
        }
    }
}

fn run_visible(program: &str, args: &[&str]) {
    match std::process::Command::new(program).args(args).status() {
        Ok(status) if status.success() => {}
        Ok(status) => println!("ERROR: {}", status.code().map(|c| format!("exit status {}", c)).unwrap_or_else(|| "signal: killed".into())),
        Err(err) => println!("ERROR: {}", err),
    }
}

fn with_manager(f: impl FnOnce(&Manager) -> minimalrouter::goish::errors::Result<()>) {
    if let Err(err) = f(&open_manager()) {
        println!("ERROR: {}", err);
    }
}

fn interactive_menu() {
    let stdin = std::io::stdin();
    let read_line = |prompt: &str| -> Option<String> {
        print!("{}", prompt);
        let _ = std::io::stdout().flush();
        let mut line = String::new();
        match stdin.lock().read_line(&mut line) {
            Ok(0) | Err(_) if line.is_empty() => None,
            _ => Some(line.trim().to_string()),
        }
    };
    let ask_confirm = |prompt: &str| -> bool {
        print!("{} (type YES): ", prompt);
        read_line("").is_some_and(|v| v == "YES")
    };
    loop {
        let Some(choice) = read_line(
            "\nminimalrouter recovery\n======================\n1) Show interfaces / status\n2) Assign WAN interface\n3) Assign LAN interface + IP\n4) Restore last-known-good configuration\n5) List / restore snapshot\n6) Factory reset\n7) Reset admin password / TOTP\n8) Restart router services\n9) Reboot\ns) Create sanitized support bundle\n0) Shell\nq) Quit\n\nSelect: ",
        ) else {
            return;
        };
        match choice.as_str() {
            "" => continue,
            "1" => show_interfaces(),
            "2" => {
                if let Some(iface) = read_line("WAN interface: ") {
                    with_manager(|m| m.set_wan(&iface).map(|s| println!("Saved; undo snapshot: {}", s.id)));
                }
            }
            "3" => {
                let Some(iface) = read_line("LAN interface: ") else { continue };
                if let Some(cidr) = read_line("LAN CIDR (e.g. 192.168.1.1/24): ") {
                    with_manager(|m| m.set_lan(&iface, &cidr).map(|s| println!("Saved; undo snapshot: {}", s.id)));
                }
            }
            "4" => {
                if ask_confirm("Restore latest verified snapshot?") {
                    with_manager(|m| m.restore_latest_snapshot().map(|(undo, id)| println!("Restored {}; undo snapshot {}", id, undo.id)));
                }
            }
            "5" => {
                list_snapshots();
                if let Some(id) = read_line("Snapshot ID to restore (blank cancels): ").filter(|id| !id.is_empty()) {
                    if ask_confirm("Restore this snapshot?") {
                        with_manager(|m| m.restore_snapshot(&id).map(|undo| println!("Restored; undo snapshot: {}", undo.id)));
                    }
                }
            }
            "6" => println!("Factory reset remains confirmation-protected. Run: router-recovery factory-reset --password-stdin --confirm FACTORY-RESET"),
            "7" => println!("Run: router-recovery reset-auth --password-stdin --disable-totp"),
            "8" => {
                if ask_confirm("Restart router-applyd and routerd?") {
                    run_visible("rc-service", &["router-applyd", "restart"]);
                    run_visible("rc-service", &["routerd", "restart"]);
                }
            }
            "9" => {
                if ask_confirm("Reboot this router VM now?") {
                    run_visible("reboot", &[]);
                }
            }
            "s" | "S" => match create_support_bundle("") {
                Err(err) => println!("ERROR: {}", err),
                Ok(path) => {
                    println!("Sanitized support bundle created: {}", path);
                    println!("It excludes passwords, private keys, configuration DB, process environments and shell history.");
                }
            },
            "0" => {
                println!("Type 'exit' to return to recovery console.");
                let _ = std::process::Command::new("/bin/sh").status();
            }
            "q" | "Q" => return,
            _ => println!("Unknown selection."),
        }
    }
}

/// A deliberately narrow archive: never the SQLite state, PPPoE secrets,
/// WireGuard or TLS keys, process environments or shell history. Every
/// command and file is chosen explicitly.
fn create_support_bundle(output: &str) -> minimalrouter::goish::errors::Result<String> {
    let stamp = Time::now_utc().format("%Y%m%dT%H%M%SZ");
    let output = if output.is_empty() {
        // Entropy in the name: a planted symlink cannot predict it.
        let mut suffix = [0u8; 4];
        minimalrouter::goish::rand::fill(&mut suffix).map_err(|e| errorf!("generate support bundle name: {}", e))?;
        format!("/tmp/minimalrouter-support-{}-{}.tar.gz", stamp, hex::encode(suffix))
    } else {
        output.to_string()
    };
    let command = |program: &str, args: &[&str]| -> String {
        match std::process::Command::new(program).args(args).output() {
            Ok(out) if out.status.success() => String::from_utf8_lossy(&[out.stdout, out.stderr].concat()).into_owned(),
            Ok(out) => format!(
                "command: {} {}\nerror: {}\n\n{}",
                program,
                args.join(" "),
                out.status.code().map(|c| format!("exit status {}", c)).unwrap_or_else(|| "signal: killed".into()),
                String::from_utf8_lossy(&[out.stdout, out.stderr].concat())
            ),
            Err(err) => format!("command: {} {}\nerror: {}\n\n", program, args.join(" "), err),
        }
    };
    let read_safe = |path: &str| std::fs::read_to_string(path).unwrap_or_else(|e| format!("unavailable: {}\n", e));
    let sysctl = |key: &str| command("sysctl", &["-n", key]).trim().to_string();
    let entries: Vec<(&str, String)> = vec![
        (
            "README.txt",
            format!(
                "minimalrouter sanitized support bundle\ncreated_utc={}\n\nSECURITY NOTE\nThis archive intentionally excludes credentials, private keys, the configuration database, process environments, and shell history.\n",
                Time::now_utc().rfc3339()
            ),
        ),
        (
            "version.txt",
            format!(
                "minimalrouter_version:\n{}\ninstalled_marker:\n{}\nalpine_release:\n{}",
                read_safe("/etc/minimalrouter/VERSION"),
                read_safe("/etc/minimalrouter/installed"),
                read_safe("/etc/alpine-release")
            ),
        ),
        ("kernel.txt", command("uname", &["-a"])),
        ("memory.txt", read_safe("/proc/meminfo")),
        ("cpu.txt", read_safe("/proc/cpuinfo")),
        ("interfaces.txt", command("ip", &["-details", "-brief", "link"])),
        ("addresses.txt", command("ip", &["-brief", "address"])),
        ("routes-ipv4.txt", command("ip", &["-4", "route", "show"])),
        ("routes-ipv6.txt", command("ip", &["-6", "route", "show"])),
        ("neighbors.txt", command("ip", &["neighbor", "show"])),
        ("disks.txt", command("lsblk", &["-o", "NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS,MODEL,SERIAL"])),
        ("mounts.txt", command("findmnt", &["-r"])),
        ("openrc.txt", command("rc-status", &["-a"])),
        ("routerd-status.txt", command("rc-service", &["routerd", "status"])),
        ("applyd-status.txt", command("rc-service", &["router-applyd", "status"])),
        ("sshd-status.txt", command("rc-service", &["sshd", "status"])),
        ("listeners.txt", command("ss", &["-lntu"])),
        ("nftables.txt", command("nft", &["list", "ruleset"])),
        ("modules.txt", command("lsmod", &[])),
        (
            "sysctl-routing.txt",
            format!(
                "net.ipv4.ip_forward={}\nnet.ipv6.conf.all.forwarding={}\nnet.ipv4.conf.all.rp_filter={}\n",
                sysctl("net.ipv4.ip_forward"),
                sysctl("net.ipv6.conf.all.forwarding"),
                sysctl("net.ipv4.conf.all.rp_filter")
            ),
        ),
    ];
    write_bundle(&output, &entries)?;
    Ok(output)
}

/// O_EXCL refuses an existing path, including a planted symlink in /tmp.
fn write_bundle(output: &str, entries: &[(&str, String)]) -> minimalrouter::goish::errors::Result<()> {
    let mut options = std::fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let file = options.open(output).map_err(|e| errorf!("create support bundle at {}: {}", output, e))?;
    let mut archive = tar::Builder::new(flate2::write::GzEncoder::new(file, flate2::Compression::default()));
    let mut sorted: Vec<&(&str, String)> = entries.iter().collect();
    sorted.sort_by_key(|(name, _)| *name);
    let now = Time::now().unix().max(0) as u64;
    for (name, content) in sorted {
        let mut header = tar::Header::new_ustar();
        header.set_size(content.len() as u64);
        header.set_mode(0o600);
        header.set_mtime(now);
        header.set_entry_type(tar::EntryType::Regular);
        archive.append_data(&mut header, Path::new(name), content.as_bytes())?;
    }
    archive.into_inner()?.finish()?.sync_all()?;
    Ok(())
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    // This pipe protocol cannot elevate its caller: the worker refuses root
    // and verifies permanently dropped appliance credentials.
    if args.len() == 2 && args[1] == recovery::MIGRATION_DB_WORKER_ARGUMENT {
        if recovery::run_migration_db_worker().is_err() {
            std::process::exit(1);
        }
        return;
    }
    if args.len() > 1 && matches!(args[1].as_str(), "help" | "--help" | "-h") {
        usage(&mut std::io::stdout());
        return;
    }
    if geteuid() != 0 {
        fatal("router-recovery must be run as root on the local console");
    }
    if args.len() < 2 {
        interactive_menu();
        return;
    }
    run_command(&args[1], &args[2..]);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn support_bundle_refuses_existing_paths_and_is_private() {
        let path = std::env::temp_dir().join(format!("mr-support-{}.tar.gz", std::process::id()));
        let _ = std::fs::remove_file(&path);
        let path = path.to_string_lossy().into_owned();
        write_bundle(&path, &[("b.txt", "two".into()), ("a.txt", "one".into())]).unwrap();
        assert!(write_bundle(&path, &[]).is_err(), "existing destination is refused");
        let mut archive = tar::Archive::new(flate2::read::GzDecoder::new(std::fs::File::open(&path).unwrap()));
        let names: Vec<String> = archive.entries().unwrap().map(|e| e.unwrap().path().unwrap().to_string_lossy().into_owned()).collect();
        assert_eq!(names, ["a.txt", "b.txt"]);
        std::fs::remove_file(path).ok();
    }
}
