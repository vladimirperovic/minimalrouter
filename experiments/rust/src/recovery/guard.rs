//! The offline lock shared by both daemons and the migration fence check.

use crate::errorf;
use crate::goish::errors::Result;

pub(crate) const OFFLINE_LOCK_PATH: &str = "/run/minimalrouter-offline.lock";
pub(crate) const MIGRATION_DIRECTORY: &str = "/var/lib/minimalrouter-migration";

/// A held offline lock; the lock is released when this is dropped.
pub struct Guard {
    #[allow(dead_code)]
    file: Option<std::fs::File>,
}

/// Holds a shared lock for the daemon lifetime. Call it before ANY runtime or
/// store initialisation and keep the guard until exit. Non-Linux developer
/// builds need no host guard.
pub fn acquire_daemon_guard() -> Result<Guard> {
    #[cfg(target_os = "linux")]
    {
        let file = acquire_offline_lock(OFFLINE_LOCK_PATH, false, 0)?;
        check_migration_fence(MIGRATION_DIRECTORY, 0)?;
        Ok(Guard { file: Some(file) })
    }
    #[cfg(not(target_os = "linux"))]
    {
        Ok(Guard { file: None })
    }
}

#[cfg(unix)]
pub(crate) fn check_migration_fence(dir: &str, uid: u32) -> Result<()> {
    if let Err(err) = secure_directory(dir, uid, false) {
        if err.kind() == std::io::ErrorKind::NotFound {
            return Ok(());
        }
        return Err(errorf!("unsafe offline migration fence directory: {}", err));
    }
    match std::fs::symlink_metadata(std::path::Path::new(dir).join("pending.json")) {
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(e) => Err(errorf!("cannot inspect offline migration fence: {}", e)),
        Ok(_) => Err(errorf!(
            "offline configuration migration is incomplete; resume router-recovery migrate-config before starting services"
        )),
    }
}

/// Checks the parent and the opened inode, never following a lock symlink.
#[cfg(unix)]
pub(crate) fn acquire_offline_lock(path: &str, exclusive: bool, uid: u32) -> Result<std::fs::File> {
    use std::os::unix::fs::{MetadataExt, PermissionsExt};
    use std::os::unix::io::AsRawFd;
    let parent = std::path::Path::new(path).parent().map(|p| p.to_string_lossy().into_owned()).unwrap_or_else(|| "/".into());
    secure_directory(&parent, uid, false).map_err(|e| errorf!("{}", e))?;
    use std::os::unix::io::FromRawFd;
    // SAFETY: geteuid has no preconditions.
    let euid = unsafe { libc::geteuid() };
    let c_path = std::ffi::CString::new(path).map_err(|e| errorf!("{}", e))?;
    let mut flags = libc::O_RDONLY | libc::O_CLOEXEC | libc::O_NOFOLLOW;
    if euid == uid {
        flags |= libc::O_CREAT;
    }
    // SAFETY: c_path is NUL-terminated; the returned descriptor is owned below.
    let fd = unsafe { libc::open(c_path.as_ptr(), flags, 0o644 as libc::c_uint) };
    if fd < 0 {
        return Err(errorf!("open offline guard: open {}: {}", path, std::io::Error::last_os_error()));
    }
    // SAFETY: fd is a freshly opened descriptor we exclusively own.
    let file = unsafe { std::fs::File::from_raw_fd(fd) };
    let meta = file.metadata().map_err(|e| errorf!("{}", e))?;
    if meta.uid() != uid || !meta.file_type().is_file() || meta.mode() & 0o022 != 0 || meta.nlink() != 1 {
        return Err(errorf!("offline guard must be an exclusively owned, non-writable regular file"));
    }
    if euid == uid {
        file.set_permissions(std::fs::Permissions::from_mode(0o644)).map_err(|e| errorf!("{}", e))?;
    }
    let op = if exclusive { libc::LOCK_EX } else { libc::LOCK_SH };
    // SAFETY: the descriptor is valid for the lifetime of `file`.
    if unsafe { libc::flock(file.as_raw_fd(), op | libc::LOCK_NB) } != 0 {
        let err = std::io::Error::last_os_error();
        return Err(errorf!("offline guard busy; stop both routerd and router-applyd before migration: {}", err));
    }
    Ok(file)
}

#[cfg(unix)]
pub(crate) fn secure_directory(path: &str, uid: u32, private: bool) -> std::io::Result<()> {
    use std::os::unix::fs::MetadataExt;
    let meta = std::fs::symlink_metadata(path)?;
    let mask = if private { 0o077 } else { 0o022 };
    if !meta.file_type().is_dir() || meta.uid() != uid || meta.mode() & mask != 0 {
        return Err(std::io::Error::other(format!("unsafe ownership or permissions on directory {}", path)));
    }
    Ok(())
}

/// Old releases did not take the guard; reject their processes too.
#[cfg(unix)]
pub(crate) fn require_daemons_stopped(proc: &str) -> Result<()> {
    for entry in std::fs::read_dir(proc)?.flatten() {
        let name = entry.file_name().to_string_lossy().into_owned();
        if name.parse::<u64>().is_err() {
            continue;
        }
        match std::fs::read_to_string(entry.path().join("comm")) {
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => continue,
            Err(e) => return Err(errorf!("inspect daemon processes: {}", e)),
            Ok(comm) => {
                let comm = comm.trim();
                if comm == "routerd" || comm == "router-applyd" {
                    return Err(errorf!("{} is still running; stop both daemons", comm));
                }
            }
        }
    }
    Ok(())
}
