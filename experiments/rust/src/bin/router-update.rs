//! router-update: the root-only bootstrap updater. It stages only signed
//! releases, activates them with the router service pair restarted from one
//! slot, rolls back automatically on a failed start, and gates the full
//! installer's trust and layout decisions.

use std::io::Write;
use std::path::Path;
use std::time::Duration as StdDuration;

use minimalrouter::firmware::{self, FirmwareManifest, SlotManager};
use minimalrouter::goish::errors::{wrapf, Result};
use minimalrouter::goish::flag::{geteuid, ErrorHandling, FlagError, FlagSet};
use minimalrouter::{errorf, runtimebudget};

const DEFAULT_UPDATE_ROOT: &str = "/var/lib/minimalrouter-update";
const DEFAULT_PUBLIC_KEY: &str = "/etc/minimalrouter/firmware-signing.pub";

fn env_or(name: &str, fallback: &str) -> String {
    std::env::var(name).ok().filter(|v| !v.is_empty()).unwrap_or_else(|| fallback.into())
}

fn usage(w: &mut dyn Write) {
    let _ = writeln!(
        w,
        "Usage: router-update <command>\n  stage --dir PATH --manifest PATH\n  activate --version VERSION --confirm ACTIVATE-UPDATE\n  rollback --confirm ROLLBACK-UPDATE\n  status\n  startup-budget\n  install-preflight --dir PATH\n  install-begin --dir PATH\n  install-baseline --dir PATH\n  help"
    );
}

struct LayoutFile {
    slot_path: String,
    system_path: String,
    mode: u32,
}

/// Only compiled role-table paths are ever compared with the root
/// filesystem; non-bootstrap paths are architecture-independent.
fn layout_files(bootstrap: bool, arch: &str) -> Result<Vec<LayoutFile>> {
    Ok(firmware::appliance_file_roles(arch)?
        .into_iter()
        .filter(|r| !r.system_path.is_empty() && r.bootstrap == bootstrap)
        .map(|r| LayoutFile { slot_path: r.path, system_path: r.system_path, mode: r.mode })
        .collect())
}

const DISPATCHER_LINKS: [(&str, &str); 4] = [
    ("/usr/bin/routerd", "/usr/libexec/minimalrouter/slot-exec"),
    ("/usr/sbin/router-applyd", "/usr/libexec/minimalrouter/slot-exec"),
    ("/usr/sbin/router-recovery", "/usr/libexec/minimalrouter/slot-exec"),
    ("/usr/sbin/router-update", "/usr/libexec/minimalrouter/slot-exec"),
];

fn rooted_path(root: &str, absolute: &str) -> String {
    if root.is_empty() || root == "/" {
        return absolute.into();
    }
    Path::new(root).join(absolute.trim_start_matches('/')).to_string_lossy().into_owned()
}

/// Exact permission bits; platforms without them fail closed.
fn mode_matches(meta: &std::fs::Metadata, mode: u32) -> bool {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        meta.permissions().mode() & 0o777 == mode
    }
    #[cfg(not(unix))]
    {
        let _ = (meta, mode);
        false
    }
}

fn verify_layout_files(slot_root: &str, system_root: &str, files: &[LayoutFile]) -> Result<()> {
    for item in files {
        let candidate = std::fs::read(Path::new(slot_root).join(&item.slot_path)).map_err(|e| errorf!("candidate runtime layout missing {}: {}", item.slot_path, e))?;
        let installed_path = rooted_path(system_root, &item.system_path);
        let installed = std::fs::read(&installed_path).map_err(|_| errorf!("installed runtime layout missing {}; run the full distribution installer before A/B activation", item.system_path))?;
        if candidate != installed {
            return Err(errorf!("installed runtime layout differs at {}; run the full distribution installer before A/B activation", item.system_path));
        }
        if !std::fs::metadata(&installed_path).is_ok_and(|m| m.is_file() && mode_matches(&m, item.mode)) {
            return Err(errorf!("installed runtime layout has unsafe mode at {}; run the full distribution installer", item.system_path));
        }
    }
    Ok(())
}

/// Repairs only the fixed command entry points that select the active slot;
/// the dispatcher itself is content- and mode-verified first.
fn ensure_runtime_dispatcher_links(system_root: &str) -> Result<()> {
    for (system_path, target) in DISPATCHER_LINKS {
        let path = rooted_path(system_root, system_path);
        if std::fs::read_link(&path).is_ok_and(|current| current.to_string_lossy() == target) {
            continue;
        }
        if let Some(parent) = Path::new(&path).parent() {
            std::fs::create_dir_all(parent).map_err(|e| errorf!("prepare dispatcher directory for {}: {}", system_path, e))?;
        }
        let temp = format!("{}.minimalrouter-new", path);
        let _ = std::fs::remove_file(&temp);
        #[cfg(unix)]
        let linked = std::os::unix::fs::symlink(target, &temp);
        #[cfg(not(unix))]
        let linked: std::io::Result<()> = Err(std::io::Error::other("symlinks require a Unix filesystem"));
        linked.map_err(|e| errorf!("create dispatcher link for {}: {}", system_path, e))?;
        if let Err(err) = std::fs::rename(&temp, &path) {
            let _ = std::fs::remove_file(&temp);
            return Err(errorf!("install dispatcher link for {}: {}", system_path, err));
        }
    }
    Ok(())
}

/// A/B activation never mutates OS integration files: the installed layer
/// must be an exact content/mode match for the candidate, including the
/// bootstrap-stable updater and recovery binaries.
fn verify_runtime_layout_compatibility(update_root: &str, version: &str, system_root: &str) -> Result<()> {
    let slot_root = Path::new(update_root).join("slots").join(version).to_string_lossy().into_owned();
    verify_layout_files(&slot_root, system_root, &layout_files(false, "amd64")?)?;
    verify_layout_files(&slot_root, system_root, &layout_files(true, firmware::host_arch())?)?;
    let lease_dir = rooted_path(system_root, "/var/lib/minimalrouter-dhcp");
    if !std::fs::metadata(&lease_dir).is_ok_and(|m| m.is_dir() && mode_matches(&m, 0o750)) {
        return Err(errorf!("persistent DHCP runtime state is not compatible; run the full distribution installer before A/B activation"));
    }
    Ok(())
}

/// rc-service in its own process group, bounded by the same startup window
/// routerd's start_post allows, so a slow legitimate start is not mistaken
/// for a failed activation.
fn service_command(args: &[&str]) -> Result<()> {
    let timeout = if args.len() == 2 && (args[1] == "stop" || args[1] == "status") { StdDuration::from_secs(30) } else { runtimebudget::SERVICE_COMMAND.to_std() };
    let output = minimalrouter::goish::exec::run_group("/sbin/rc-service", args, timeout, StdDuration::from_secs(2));
    if output.timed_out {
        return Err(errorf!("rc-service {} timed out after {}", args.join(" "), minimalrouter::goish::time::Duration::from_std(timeout)));
    }
    if let Some(err) = output.error() {
        let mut text = String::from_utf8_lossy(&output.combined()).trim().to_string();
        text.truncate(300);
        if text.is_empty() {
            return Err(errorf!("rc-service {}: {}", args.join(" "), err));
        }
        return Err(errorf!("rc-service {}: {}: {}", args.join(" "), err, text));
    }
    Ok(())
}

/// Stop the management plane, restart the helper from the new slot, then
/// start routerd: no mixed-slot RPC window.
fn restart_runtime_pair() -> Result<()> {
    service_command(&["routerd", "stop"])?;
    service_command(&["router-applyd", "restart"])?;
    service_command(&["routerd", "start"])?;
    service_command(&["router-applyd", "status"])?;
    service_command(&["routerd", "status"])
}

fn activate_and_restart(manager: &SlotManager, version: &str, system_root: &str) -> Result<()> {
    let state = manager.state().map_err(|e| wrapf("read update state", e))?;
    if state.current.is_empty() {
        return Err(errorf!("no rollback baseline slot is registered; rerun the full distribution installer before the first A/B activation"));
    }
    verify_runtime_layout_compatibility(&manager.root, version, system_root)?;
    verify_runtime_layout_compatibility(&manager.root, &state.current, system_root).map_err(|e| wrapf("rollback baseline is incompatible", e))?;
    ensure_runtime_dispatcher_links(system_root)?;
    manager.activate(version)?;
    let Err(activation) = restart_runtime_pair() else { return Ok(()) };
    match manager.rollback().and_then(|_| restart_runtime_pair()) {
        Ok(()) => Err(errorf!("new slot failed to start and was automatically rolled back: {}", activation)),
        Err(rollback) => Err(errorf!("new slot failed to start: {}\nautomatic rollback failed: {}", activation, rollback)),
    }
}

fn rollback_and_restart(manager: &SlotManager, system_root: &str) -> Result<()> {
    let state = manager.state()?;
    if state.previous.is_empty() {
        return Err(errorf!("no previous release slot is available"));
    }
    verify_runtime_layout_compatibility(&manager.root, &state.previous, system_root).map_err(|e| wrapf("rollback runtime is incompatible", e))?;
    manager.rollback()?;
    restart_runtime_pair().map_err(|e| wrapf("rollback pointer changed but services did not restart cleanly", e))
}

/// The full installer is an explicit root trust decision, separate from
/// signed staging; this read-only preflight runs before any write.
fn inspect_installation(manager: &SlotManager, source: &str, system_root: &str) -> Result<(FirmwareManifest, String)> {
    let inventory = firmware::inspect_local_payload(source)?;
    firmware::validate_appliance_architecture(Some(&inventory), firmware::host_arch())?;
    let state = manager.state()?;
    let floor_result = state.upgrade_floor();
    let pub_path = Path::new(source).join("firmware-signing.pub");
    match std::fs::metadata(&pub_path) {
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => {
            if !matches!(std::fs::metadata(Path::new(source).join("release-manifest.json")), Err(ref e) if e.kind() == std::io::ErrorKind::NotFound) {
                return Err(errorf!("manifest without a distribution trust key"));
            }
            // A development tree may replace runtime files but cannot invent,
            // lower or erase an authenticated version floor.
            let floor = floor_result.unwrap_or_else(|_| state.minimum_version.clone());
            return Ok((inventory, floor));
        }
        Err(err) => return Err(err.into()),
        Ok(_) => {}
    }
    let mut key = firmware::load_trusted_public_key(&pub_path.to_string_lossy())?;
    let installed_key = rooted_path(system_root, DEFAULT_PUBLIC_KEY);
    match std::fs::metadata(&installed_key) {
        Ok(_) => {
            let pinned = firmware::load_trusted_public_key(&installed_key)?;
            if key != pinned {
                return Err(errorf!("refusing to replace the installed firmware trust anchor"));
            }
            key = pinned;
        }
        Err(err) if err.kind() != std::io::ErrorKind::NotFound => return Err(err.into()),
        Err(_) => {}
    }
    let manifest = firmware::load_manifest(&Path::new(source).join("release-manifest.json").to_string_lossy()).map_err(|e| wrapf("signed full installation requires release-manifest.json", e))?;
    firmware::validate_release_candidate(source, Some(&manifest), &key)?;
    // The whole tree is executed or copied: unsigned extra files are refused.
    for (path, hash) in inventory.files() {
        if path != "release-manifest.json" && manifest.files().get(path) != Some(hash) {
            return Err(errorf!("full installation contains unsigned file: {}", path));
        }
    }
    let floor = match floor_result {
        Ok(floor) => floor,
        Err(floor_err) => {
            // Golden installs predating signed baselines: their VERSION is
            // only an additional conservative lower bound.
            let Ok(data) = std::fs::read_to_string(rooted_path(system_root, "/etc/minimalrouter/VERSION")) else { return Err(floor_err) };
            let floor = data.trim().to_string();
            if firmware::compare_release_versions(&floor, &floor).is_err() {
                return Err(floor_err);
            }
            floor
        }
    };
    // A same-version full reinstall repairs; stage/activate stay forward-only.
    if !floor.is_empty() && firmware::compare_release_versions(&manifest.version, &floor)? < 0 {
        return Err(errorf!("refusing full-install downgrade below the installed version floor"));
    }
    let version = manifest.version.clone();
    Ok((inventory, version))
}

/// Rechecks and fsyncs each integration file and its ancestors before the
/// slot manager may publish a baseline and remove the install fence.
fn sync_installed_integration(source: &str, system_root: &str) -> Result<()> {
    let system_root = std::path::absolute(if system_root.is_empty() { "/" } else { system_root })?.to_string_lossy().into_owned();
    let mut files = layout_files(false, "amd64")?;
    files.extend(layout_files(true, firmware::host_arch())?);
    verify_layout_files(source, &system_root, &files)?;
    for item in &files {
        let path = rooted_path(&system_root, &item.system_path);
        std::fs::File::open(&path)?.sync_all()?;
        let mut parent = Path::new(&path).parent().map(Path::to_path_buf);
        while let Some(dir) = parent {
            #[cfg(unix)]
            std::fs::File::open(&dir)?.sync_all()?;
            if dir == Path::new(&system_root) || dir.parent().is_none() {
                break;
            }
            parent = dir.parent().map(Path::to_path_buf);
        }
    }
    Ok(())
}

/// Parse errors and usage go to the caller's stderr, like
/// `fs.SetOutput(stderr)`.
struct Captured(std::sync::Arc<std::sync::Mutex<Vec<u8>>>);

impl Write for Captured {
    fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
        self.0.lock().unwrap_or_else(|e| e.into_inner()).extend_from_slice(buf);
        Ok(buf.len())
    }
    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

fn parse_flags(fs: &mut FlagSet, args: &[String], stderr: &mut dyn Write) -> std::result::Result<(), FlagError> {
    let buffer = std::sync::Arc::new(std::sync::Mutex::new(Vec::new()));
    fs.set_output(Box::new(Captured(buffer.clone())));
    let result = fs.parse(args);
    let _ = stderr.write_all(&buffer.lock().unwrap_or_else(|e| e.into_inner()));
    result
}

fn run(args: &[String], euid: i64, stdout: &mut dyn Write, stderr: &mut dyn Write) -> i32 {
    let Some(command) = args.first() else {
        usage(stderr);
        return 2;
    };
    if matches!(command.as_str(), "help" | "--help" | "-h") {
        usage(stdout);
        return 0;
    }
    let root = env_or("MINIMALROUTER_UPDATE_ROOT", DEFAULT_UPDATE_ROOT);
    let system_root = env_or("MINIMALROUTER_SYSTEM_ROOT", "/");
    let mut manager = SlotManager { root, trusted_key: Vec::new() };
    let require_root = |stderr: &mut dyn Write| {
        if euid == 0 {
            return true;
        }
        let _ = writeln!(stderr, "ERROR: router-update must run as root for this command");
        false
    };
    let rest = &args[1..];
    match command.as_str() {
        "startup-budget" => {
            if !rest.is_empty() {
                return 2;
            }
            let _ = writeln!(stdout, "{}", runtimebudget::STARTUP.0 / 1_000_000_000);
            0
        }
        "install-preflight" | "install-begin" | "install-baseline" => {
            if !require_root(stderr) {
                return 1;
            }
            let mut fs = FlagSet::new(command, ErrorHandling::Continue);
            fs.string("dir", "", "root-trusted full distribution directory");
            if parse_flags(&mut fs, rest, stderr).is_err() {
                return 2;
            }
            let directory = fs.get("dir");
            if directory.is_empty() || fs.narg() != 0 {
                let _ = writeln!(stderr, "ERROR: full installer requires --dir");
                return 2;
            }
            let result = inspect_installation(&manager, &directory, &system_root).and_then(|(inventory, minimum)| {
                match command.as_str() {
                    "install-begin" => manager.begin_installation(&minimum)?,
                    "install-baseline" => {
                        sync_installed_integration(&directory, &system_root)?;
                        manager.install_baseline(&directory, &inventory, &minimum)?;
                    }
                    _ => {}
                }
                Ok(minimum)
            });
            match result {
                Ok(minimum) => {
                    let _ = writeln!(stdout, "Full installer trust/layout preflight passed; minimum version: {}", minimum);
                    0
                }
                Err(err) => {
                    let _ = writeln!(stderr, "ERROR: full installer: {}", err);
                    1
                }
            }
        }
        "stage" => {
            if !require_root(stderr) {
                return 1;
            }
            let mut fs = FlagSet::new("stage", ErrorHandling::Continue);
            fs.string("dir", "", "already-extracted release directory");
            fs.string("manifest", "", "signed release manifest");
            match parse_flags(&mut fs, rest, stderr) {
                Err(FlagError::Help) => return 0,
                Err(_) => return 2,
                Ok(()) => {}
            }
            let (directory, manifest_path) = (fs.get("dir"), fs.get("manifest"));
            if directory.is_empty() || manifest_path.is_empty() {
                let _ = writeln!(stderr, "ERROR: stage requires --dir and --manifest");
                return 2;
            }
            let key_path = env_or("MINIMALROUTER_FIRMWARE_PUBLIC_KEY", DEFAULT_PUBLIC_KEY);
            match firmware::load_trusted_public_key(&key_path) {
                Ok(key) => manager.trusted_key = key,
                Err(err) => {
                    let _ = writeln!(stderr, "ERROR: load pinned firmware key: {}", err);
                    return 1;
                }
            }
            let staged = firmware::load_manifest(&manifest_path).and_then(|manifest| manager.stage(&directory, Some(&manifest)).map(|_| manifest.version));
            match staged {
                Ok(version) => {
                    let _ = writeln!(stdout, "Release {} verified and staged. Run activate explicitly after review.", version);
                    0
                }
                Err(err) => {
                    let _ = writeln!(stderr, "ERROR: {}", err);
                    1
                }
            }
        }
        "activate" => {
            if !require_root(stderr) {
                return 1;
            }
            let mut fs = FlagSet::new("activate", ErrorHandling::Continue);
            fs.string("version", "", "staged version");
            fs.string("confirm", "", "must equal ACTIVATE-UPDATE");
            match parse_flags(&mut fs, rest, stderr) {
                Err(FlagError::Help) => return 0,
                Err(_) => return 2,
                Ok(()) => {}
            }
            if fs.get("version").is_empty() {
                let _ = writeln!(stderr, "ERROR: activate requires --version");
                return 2;
            }
            if fs.get("confirm") != "ACTIVATE-UPDATE" {
                let _ = writeln!(stderr, "ERROR: activation requires --confirm ACTIVATE-UPDATE");
                return 2;
            }
            if let Err(err) = activate_and_restart(&manager, &fs.get("version"), &system_root) {
                let _ = writeln!(stderr, "ERROR: {}", err);
                return 1;
            }
            let _ = writeln!(stdout, "Update slot activated; router-applyd and routerd restarted from the same slot and passed service health checks.");
            0
        }
        "rollback" => {
            if !require_root(stderr) {
                return 1;
            }
            let mut fs = FlagSet::new("rollback", ErrorHandling::Continue);
            fs.string("confirm", "", "must equal ROLLBACK-UPDATE");
            match parse_flags(&mut fs, rest, stderr) {
                Err(FlagError::Help) => return 0,
                Err(_) => return 2,
                Ok(()) => {}
            }
            if fs.get("confirm") != "ROLLBACK-UPDATE" {
                let _ = writeln!(stderr, "ERROR: rollback requires --confirm ROLLBACK-UPDATE");
                return 2;
            }
            if let Err(err) = rollback_and_restart(&manager, &system_root) {
                let _ = writeln!(stderr, "ERROR: {}", err);
                return 1;
            }
            let _ = writeln!(stdout, "Previous verified update slot restored and both router services restarted from it.");
            0
        }
        "status" => {
            let mut fs = FlagSet::new("status", ErrorHandling::Continue);
            match parse_flags(&mut fs, rest, stderr) {
                Err(FlagError::Help) => return 0,
                Err(_) => return 2,
                Ok(()) => {}
            }
            match manager.state().and_then(|state| Ok(minimalrouter::goish::json::marshal_indent(&state, "  ")?)) {
                Ok(data) => {
                    let _ = writeln!(stdout, "{}", String::from_utf8_lossy(&data));
                    0
                }
                Err(err) => {
                    let _ = writeln!(stderr, "ERROR: {}", err);
                    1
                }
            }
        }
        _ => {
            usage(stderr);
            2
        }
    }
}

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let code = run(&args, geteuid(), &mut std::io::stdout(), &mut std::io::stderr());
    std::process::exit(code);
}

#[cfg(test)]
mod tests {
    use super::*;

    fn invoke(args: &[&str], euid: i64) -> (i32, String, String) {
        let args: Vec<String> = args.iter().map(|a| a.to_string()).collect();
        let (mut out, mut err) = (Vec::new(), Vec::new());
        let code = run(&args, euid, &mut out, &mut err);
        (code, String::from_utf8(out).unwrap(), String::from_utf8(err).unwrap())
    }

    #[test]
    fn command_line_contract() {
        assert_eq!(invoke(&[], 0).0, 2);
        assert_eq!(invoke(&["help"], 1000).0, 0);
        let (code, out, _) = invoke(&["startup-budget"], 1000);
        assert_eq!((code, out.trim()), (0, "300"));
        let (code, _, err) = invoke(&["stage", "--dir", "/x"], 1000);
        assert_eq!((code, err.trim()), (1, "ERROR: router-update must run as root for this command"));
        let (code, _, err) = invoke(&["activate", "--version", "0.4.0"], 0);
        assert_eq!((code, err.trim()), (2, "ERROR: activation requires --confirm ACTIVATE-UPDATE"));
        assert_eq!(rooted_path("/mnt/target", "/etc/minimalrouter/VERSION").replace('\\', "/"), "/mnt/target/etc/minimalrouter/VERSION");
        assert_eq!(layout_files(true, "arm64").unwrap().len(), 3);
    }
}
