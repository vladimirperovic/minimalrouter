//! Lab-only fault-injection hooks for crash-consistency testing. A hook runs
//! only when MINIMALROUTER_FAULT_HOOK_DIR names a directory owned by root or
//! the current user that is not group/other writable.

use std::path::Path;
use std::process::Command;
use std::time::Duration;

pub const HOOK_DIR_ENV: &str = "MINIMALROUTER_FAULT_HOOK_DIR";

pub const PRE_PRIVILEGED_APPLY: &str = "pre-privileged-apply";
pub const POST_PROVISIONAL_APPLY: &str = "post-provisional-apply";
pub const PRE_SQLITE_COMMIT: &str = "pre-sqlite-commit";
pub const POST_SQLITE_COMMIT: &str = "post-sqlite-commit";
pub const PRE_CANONICAL_ACK: &str = "pre-canonical-ack";
pub const DURING_FINAL_RECONCILE: &str = "during-final-reconcile";

const HOOK_TIMEOUT: Duration = Duration::from_secs(600);

pub fn run(phase: &str) {
    let Ok(dir) = std::env::var(HOOK_DIR_ENV) else { return };
    if dir.is_empty() {
        return;
    }
    let path = Path::new(&dir).join(phase);
    let Ok(info) = std::fs::symlink_metadata(&path) else { return };
    if let Err(err) = hook_source_trusted(Path::new(&dir), &path, &info) {
        log::warn!("faultinject: hook {:?} ignored: {}", phase, err);
        return;
    }
    let Ok(data) = std::fs::read_to_string(&path) else { return };
    let command = data.trim();
    if command.is_empty() {
        return;
    }
    log::info!("faultinject: hook {:?} armed: {}", phase, command);
    let output = crate::proc::execute(
        Command::new("/bin/sh").arg("-c").arg(command),
        crate::proc::Options { capture: false, ..crate::proc::Options::timeout(HOOK_TIMEOUT) },
    );
    if output.spawn_error.is_some() {
        log::warn!("faultinject: hook {:?} failed to start (continuing)", phase);
    } else if let Some(status) = output.status.filter(|s| !s.success()) {
        log::warn!("faultinject: hook {:?} failed (continuing): {}", phase, status);
    }
}

fn hook_source_trusted(dir: &Path, path: &Path, info: &std::fs::Metadata) -> Result<(), String> {
    if !info.file_type().is_file() {
        return Err(format!("{} is not a regular file", path.display()));
    }
    let dir_info = std::fs::symlink_metadata(dir).map_err(|e| e.to_string())?;
    if !dir_info.is_dir() {
        return Err(format!("{} is not a directory", dir.display()));
    }
    for (item, meta) in [(dir, &dir_info), (path, info)] {
        owned_by_root_or_self_and_private(item, meta)?;
    }
    Ok(())
}

#[cfg(unix)]
fn owned_by_root_or_self_and_private(path: &Path, meta: &std::fs::Metadata) -> Result<(), String> {
    use std::os::unix::fs::MetadataExt;
    if meta.mode() & 0o022 != 0 {
        return Err(format!("{} is writable by group or others", path.display()));
    }
    let euid = crate::sys::effective_uid();
    if meta.uid() != 0 && meta.uid() != euid {
        return Err(format!("{} is owned by uid {}", path.display(), meta.uid()));
    }
    Ok(())
}

/// Lab hooks exist only for the Linux appliance; elsewhere ownership cannot be
/// proven, so a hook is never run.
#[cfg(not(unix))]
fn owned_by_root_or_self_and_private(path: &Path, _meta: &std::fs::Metadata) -> Result<(), String> {
    Err(format!("{} ownership cannot be verified on this platform", path.display()))
}
