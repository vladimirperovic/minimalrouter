//! Process, file and account primitives for the privileged helper.

use std::io::{Read, Write};
use std::path::Path;
use std::process::Command;
use std::time::{Duration, Instant};

use crate::errorf;
use crate::goish::errors::Result;

pub(crate) const SOCKET_DIR: &str = "/run/minimalrouter";
pub(crate) const DEFAULT_COMMAND_TIMEOUT: Duration = Duration::from_secs(30);
/// How long an idle peer may hold a connection before sending its request.
pub(crate) const REQUEST_READ_TIMEOUT: Duration = Duration::from_secs(15);
/// Bounds only the delivery of a finished outcome, armed at write time.
pub(crate) const RESPONSE_WRITE_TIMEOUT: Duration = Duration::from_secs(15);

fn base_name(path: &str) -> &str {
    path.rsplit('/').next().unwrap_or(path)
}

pub(crate) fn sanitize_output(output: &[u8]) -> String {
    let text = String::from_utf8_lossy(output);
    let mut text = text.trim().to_string();
    if text.len() > 512 {
        let mut cut = 512;
        while !text.is_char_boundary(cut) {
            cut -= 1;
        }
        text.truncate(cut);
    }
    let text = text.replace(['\r', '\n'], " ");
    if text.is_empty() { "no diagnostic output".into() } else { text }
}

pub(crate) fn safe_error(err: &crate::goish::errors::Error) -> String {
    sanitize_output(err.to_string().as_bytes())
}

/// Gives every privileged child process a hard upper bound: the helper is
/// serialised, so one wedged command must never hold the apply lock forever.
pub(crate) fn run_command_output(timeout: Duration, binary: &str, args: &[&str]) -> Result<String> {
    let timeout = if timeout.is_zero() { DEFAULT_COMMAND_TIMEOUT } else { timeout };
    if std::fs::metadata(binary).is_err() {
        return Err(errorf!("required binary unavailable: {}", base_name(binary)));
    }
    let mut command = Command::new(binary);
    command.args(args).env_clear().env("PATH", "/sbin:/usr/sbin:/bin:/usr/bin").env("LANG", "C").env("LC_ALL", "C");
    let output = crate::proc::run_command(&mut command, timeout, None);
    if let Some(err) = output.spawn_error {
        return Err(errorf!("{} failed: {}", base_name(binary), err));
    }
    if output.timed_out {
        return Err(errorf!("{} timed out after {}", base_name(binary), crate::goish::time::Duration::from_std(timeout)));
    }
    let combined = output.combined();
    if !output.success() {
        return Err(errorf!("{} failed: {}", base_name(binary), sanitize_output(&combined)));
    }
    Ok(String::from_utf8_lossy(&combined).into_owned())
}

pub(crate) fn run_fixed(binary: &str, args: &[&str]) -> Result<()> {
    run_command_output(DEFAULT_COMMAND_TIMEOUT, binary, args).map(|_| ())
}

pub(crate) fn run_fixed_timeout(timeout: Duration, binary: &str, args: &[&str]) -> Result<()> {
    run_command_output(timeout, binary, args).map(|_| ())
}

pub(crate) fn run_fixed_output(binary: &str, args: &[&str]) -> Result<String> {
    run_command_output(DEFAULT_COMMAND_TIMEOUT, binary, args)
}

fn random_suffix() -> String {
    crate::auth::generate_random_hex(6).unwrap_or_else(|_| format!("{}", std::process::id()))
}

pub(crate) fn sync_dir(dir: &Path) -> std::io::Result<()> {
    std::fs::File::open(dir)?.sync_all()
}

/// Write-to-temp, fsync, rename, fsync-directory.
pub(crate) fn atomic_write(path: &str, data: &[u8], mode: u32) -> Result<()> {
    let target = Path::new(path);
    let dir = target.parent().unwrap_or(Path::new("/"));
    std::fs::create_dir_all(dir)?;
    let tmp = dir.join(format!(".minimalrouter-{}", random_suffix()));
    let result = (|| -> std::io::Result<()> {
        let mut options = std::fs::OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(mode);
        }
        let mut file = options.open(&tmp)?;
        crate::config::set_mode(&tmp.to_string_lossy(), mode)?;
        file.write_all(data)?;
        file.sync_all()?;
        drop(file);
        std::fs::rename(&tmp, target)?;
        sync_dir(dir)
    })();
    let _ = std::fs::remove_file(&tmp);
    result.map_err(Into::into)
}

/// Removes a file (missing is fine) and syncs its directory, so the removal
/// itself is durable before success is reported.
pub(crate) fn clear_file_durably(path: &str) -> std::io::Result<()> {
    match std::fs::remove_file(path) {
        Err(e) if e.kind() != std::io::ErrorKind::NotFound => return Err(e),
        _ => {}
    }
    sync_dir(Path::new(path).parent().unwrap_or(Path::new("/")))
}

pub(crate) fn is_not_found(err: &crate::goish::errors::Error) -> bool {
    err.chain().any(|e| e.downcast_ref::<std::io::Error>().is_some_and(|io| io.kind() == std::io::ErrorKind::NotFound))
}

/// `fmt.Sscanf(value, "%d", &id)` with id >= 0.
pub(crate) fn parse_numeric_id(label: &str, value: &str) -> Result<u32> {
    let digits: String = value.trim_start().chars().take_while(|c| c.is_ascii_digit()).collect();
    digits.parse::<u32>().map_err(|_| errorf!("{} is invalid: {}", label, crate::goish::strings::quote(value)))
}

/// `user.Lookup(name)` from /etc/passwd: (uid, gid).
pub(crate) fn lookup_user(name: &str) -> Result<(u32, u32)> {
    let passwd = std::fs::read_to_string("/etc/passwd")?;
    for line in passwd.lines() {
        let f: Vec<&str> = line.split(':').collect();
        if f.len() >= 4 && f[0] == name {
            return Ok((parse_numeric_id("uid", f[2])?, parse_numeric_id("gid", f[3])?));
        }
    }
    Err(errorf!("user: unknown user {}", name))
}

/// `user.LookupGroup(name)` from /etc/group: gid.
pub(crate) fn lookup_group(name: &str) -> Result<u32> {
    let group = std::fs::read_to_string("/etc/group")?;
    for line in group.lines() {
        let f: Vec<&str> = line.split(':').collect();
        if f.len() >= 3 && f[0] == name {
            return parse_numeric_id("gid", f[2]);
        }
    }
    Err(errorf!("group: unknown group {}", name))
}

#[cfg(unix)]
pub(crate) fn chown(path: &str, uid: u32, gid: u32) -> std::io::Result<()> {
    std::os::unix::fs::chown(path, Some(uid), Some(gid))
}

#[cfg(not(unix))]
pub(crate) fn chown(_path: &str, _uid: u32, _gid: u32) -> std::io::Result<()> {
    Ok(())
}

/// Narrows ambient behaviour before any request is accepted: the helper stays
/// root but cannot gain privilege through exec or dump its memory.
#[cfg(target_os = "linux")]
pub(crate) fn harden_process() -> Result<()> {
    // SAFETY: constant-argument process attribute calls.
    unsafe {
        libc::umask(0o077);
        if libc::prctl(libc::PR_SET_DUMPABLE, 0, 0, 0, 0) != 0 {
            return Err(errorf!("disable core dumps: {}", std::io::Error::last_os_error()));
        }
        if libc::prctl(libc::PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) != 0 {
            return Err(errorf!("enable no_new_privs: {}", std::io::Error::last_os_error()));
        }
        for (resource, limit) in [(libc::RLIMIT_CORE, 0u64), (libc::RLIMIT_NOFILE, 1024), (libc::RLIMIT_NPROC, 128)] {
            let rl = libc::rlimit { rlim_cur: limit as libc::rlim_t, rlim_max: limit as libc::rlim_t };
            if libc::setrlimit(resource, &rl) != 0 {
                return Err(errorf!("set resource limit {}: {}", resource, std::io::Error::last_os_error()));
            }
        }
    }
    // Child processes use only project-selected absolute binaries.
    // SAFETY: called during single-threaded startup before any thread spawns.
    unsafe {
        std::env::set_var("PATH", "/usr/sbin:/usr/bin:/sbin:/bin");
        for var in ["LD_PRELOAD", "LD_LIBRARY_PATH", "ENV", "BASH_ENV"] {
            std::env::remove_var(var);
        }
    }
    Ok(())
}

#[cfg(not(target_os = "linux"))]
pub(crate) fn harden_process() -> Result<()> {
    Ok(())
}

/// Only root and the routerd account may talk to the helper sockets.
#[cfg(target_os = "linux")]
pub(crate) fn validate_peer(stream: &std::os::unix::net::UnixStream) -> Result<()> {
    use std::os::unix::io::AsRawFd;
    let mut cred = libc::ucred { pid: 0, uid: 0, gid: 0 };
    let mut len = std::mem::size_of::<libc::ucred>() as libc::socklen_t;
    // SAFETY: cred/len are valid out pointers for SO_PEERCRED.
    let rc = unsafe { libc::getsockopt(stream.as_raw_fd(), libc::SOL_SOCKET, libc::SO_PEERCRED, &mut cred as *mut _ as *mut libc::c_void, &mut len) };
    if rc != 0 {
        return Err(std::io::Error::last_os_error().into());
    }
    let (routerd_uid, _) = lookup_user("routerd")?;
    if cred.uid != 0 && cred.uid != routerd_uid {
        return Err(errorf!("unexpected peer uid {}", cred.uid));
    }
    Ok(())
}

#[cfg(all(unix, not(target_os = "linux")))]
pub(crate) fn validate_peer(_stream: &std::os::unix::net::UnixStream) -> Result<()> {
    Err(errorf!("router-applyd peer authentication is supported only on Linux"))
}

/// Restricts a helper socket (and its directory) to root:routerd.
pub(crate) fn secure_socket_for_routerd(path: &str) -> Result<()> {
    let gid = lookup_group("routerd").map_err(|e| crate::goish::errors::wrapf("routerd group is required", e))?;
    chown(SOCKET_DIR, 0, gid)?;
    crate::config::set_mode(SOCKET_DIR, 0o750)?;
    chown(path, 0, gid)?;
    crate::config::set_mode(path, 0o660)?;
    Ok(())
}

/// Reads until EOF, at most `limit` bytes, with an absolute deadline.
#[cfg(unix)]
pub(crate) fn read_request(stream: &std::os::unix::net::UnixStream, limit: usize, deadline: Instant) -> std::io::Result<Vec<u8>> {
    let mut data = Vec::new();
    let mut buf = [0u8; 16384];
    loop {
        let remaining = deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            return Err(std::io::Error::new(std::io::ErrorKind::TimedOut, "i/o timeout"));
        }
        stream.set_read_timeout(Some(remaining))?;
        let n = (&*stream).read(&mut buf)?;
        if n == 0 {
            return Ok(data);
        }
        let take = n.min(limit - data.len());
        data.extend_from_slice(&buf[..take]);
        if data.len() >= limit {
            return Ok(data);
        }
    }
}

/// Encodes like `json.NewEncoder(conn).Encode(v)`, under a write deadline.
#[cfg(unix)]
pub(crate) fn write_json_line<T: serde::Serialize>(stream: &std::os::unix::net::UnixStream, value: &T) -> Result<()> {
    stream.set_write_timeout(Some(RESPONSE_WRITE_TIMEOUT))?;
    let mut body = crate::goish::json::marshal(value)?;
    body.push(b'\n');
    (&*stream).write_all(&body)?;
    Ok(())
}

/// Accepts with at most `limit` concurrent handlers, acquiring before Accept
/// so idle peers cannot create unbounded descriptors or threads.
#[cfg(unix)]
pub(crate) fn serve_bounded(listener: std::os::unix::net::UnixListener, limit: usize, handle: fn(std::os::unix::net::UnixStream)) {
    let (tx, rx) = std::sync::mpsc::sync_channel::<()>(limit);
    let rx = std::sync::Arc::new(std::sync::Mutex::new(rx));
    loop {
        if tx.send(()).is_err() {
            return;
        }
        match listener.accept() {
            Ok((stream, _)) => {
                let rx = rx.clone();
                std::thread::spawn(move || {
                    handle(stream);
                    let _ = rx.lock().map(|r| r.recv());
                });
            }
            Err(err) => {
                let _ = rx.lock().map(|r| r.recv());
                log::warn!("accept connection: {}", err);
                std::thread::sleep(Duration::from_millis(100));
            }
        }
    }
}
