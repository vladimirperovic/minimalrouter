//! Direct calls into the C library, behind safe functions. This module is the
//! only place in the crate allowed to use `unsafe` (the crate root denies it
//! everywhere else); every block states why the call is sound.
#![allow(unsafe_code)]

#[cfg(unix)]
pub use self::unix::*;

/// Overwrites `buf` with zeros in a way the optimiser cannot remove, for
/// buffers that held key material.
pub fn zeroize(buf: &mut [u8]) {
    for byte in buf.iter_mut() {
        // SAFETY: a volatile write to a valid, exclusively borrowed byte.
        unsafe { std::ptr::write_volatile(byte, 0) };
    }
    std::sync::atomic::compiler_fence(std::sync::atomic::Ordering::SeqCst);
}

#[cfg(unix)]
mod unix {
    use std::ffi::{CStr, CString};
    use std::fs::File;
    use std::io;
    use std::net::Ipv4Addr;
    use std::os::fd::{AsRawFd, BorrowedFd, FromRawFd};
    use std::os::unix::process::CommandExt;
    use std::process::Command;

    fn check(rc: libc::c_int) -> io::Result<()> {
        if rc == 0 { Ok(()) } else { Err(io::Error::last_os_error()) }
    }

    fn c_path(path: &str) -> io::Result<CString> {
        CString::new(path).map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "path contains a NUL byte"))
    }

    /// Blocks until at least one of `fds` is readable, hung up or in error,
    /// and returns which ones are.
    pub fn wait_readable(fds: &[BorrowedFd<'_>]) -> io::Result<Vec<bool>> {
        let mut pollfds: Vec<libc::pollfd> = fds.iter().map(|fd| libc::pollfd { fd: fd.as_raw_fd(), events: libc::POLLIN, revents: 0 }).collect();
        loop {
            // SAFETY: `pollfds` is a live, exclusively borrowed array of
            // exactly `len` pollfd structs for the duration of the call, and
            // every descriptor is kept open by the borrowed `fds`.
            let n = unsafe { libc::poll(pollfds.as_mut_ptr(), pollfds.len() as libc::nfds_t, -1) };
            if n >= 0 {
                break;
            }
            let err = io::Error::last_os_error();
            if err.kind() != io::ErrorKind::Interrupted {
                return Err(err);
            }
        }
        Ok(pollfds.iter().map(|p| p.revents & (libc::POLLIN | libc::POLLHUP | libc::POLLERR) != 0).collect())
    }

    /// Blocks until child `pid` has exited, leaving it unreaped so its
    /// process ID cannot be recycled before the owner collects it.
    pub fn wait_exit_unreaped(pid: u32) -> io::Result<()> {
        loop {
            // SAFETY: an all-zero siginfo_t is a valid value for this plain C
            // struct, and waitid only writes into the pointer we pass.
            let mut info: libc::siginfo_t = unsafe { std::mem::zeroed() };
            // SAFETY: `info` is a live, exclusively borrowed siginfo_t.
            let rc = unsafe { libc::waitid(libc::P_PID, pid as libc::id_t, &mut info, libc::WEXITED | libc::WNOWAIT) };
            if rc == 0 {
                return Ok(());
            }
            let err = io::Error::last_os_error();
            if err.kind() != io::ErrorKind::Interrupted {
                return Err(err);
            }
        }
    }

    /// Sends SIGKILL to process group `pgid`; a group that is already gone
    /// is not an error.
    pub fn kill_process_group(pgid: u32) {
        let Ok(pgid) = libc::pid_t::try_from(pgid) else { return };
        if pgid <= 1 {
            return;
        }
        // SAFETY: kill has no memory-safety preconditions; a negative pid
        // addresses exactly the process group created for our own child.
        unsafe { libc::kill(-pgid, libc::SIGKILL) };
    }

    /// Sends SIGKILL to our own child `pid` that has not been reaped yet.
    pub fn kill_child(pid: u32) {
        let Ok(pid) = libc::pid_t::try_from(pid) else { return };
        if pid <= 1 {
            return;
        }
        // SAFETY: kill has no memory-safety preconditions.
        unsafe { libc::kill(pid, libc::SIGKILL) };
    }

    pub fn effective_uid() -> u32 {
        // SAFETY: geteuid has no preconditions and cannot fail.
        unsafe { libc::geteuid() }
    }

    /// (real, effective, saved) user IDs.
    #[cfg(target_os = "linux")]
    pub fn user_ids() -> [u32; 3] {
        let (mut r, mut e, mut s) = (0, 0, 0);
        // SAFETY: three valid, exclusively borrowed out pointers.
        unsafe { libc::getresuid(&mut r, &mut e, &mut s) };
        [r, e, s]
    }

    /// (real, effective, saved) user IDs. Only Linux can read the saved ID;
    /// elsewhere (development hosts) it is reported as the effective one.
    #[cfg(not(target_os = "linux"))]
    pub fn user_ids() -> [u32; 3] {
        // SAFETY: getuid and geteuid have no preconditions and cannot fail.
        let (r, e) = unsafe { (libc::getuid(), libc::geteuid()) };
        [r, e, e]
    }

    /// (real, effective, saved) group IDs.
    #[cfg(target_os = "linux")]
    pub fn group_ids() -> [u32; 3] {
        let (mut r, mut e, mut s) = (0, 0, 0);
        // SAFETY: three valid, exclusively borrowed out pointers.
        unsafe { libc::getresgid(&mut r, &mut e, &mut s) };
        [r, e, s]
    }

    /// (real, effective, saved) group IDs. Only Linux can read the saved ID;
    /// elsewhere (development hosts) it is reported as the effective one.
    #[cfg(not(target_os = "linux"))]
    pub fn group_ids() -> [u32; 3] {
        // SAFETY: getgid and getegid have no preconditions and cannot fail.
        let (r, e) = unsafe { (libc::getgid(), libc::getegid()) };
        [r, e, e]
    }

    pub fn supplementary_group_count() -> io::Result<usize> {
        // SAFETY: a zero-length query writes nothing and returns the count.
        let n = unsafe { libc::getgroups(0, std::ptr::null_mut()) };
        usize::try_from(n).map_err(|_| io::Error::last_os_error())
    }

    pub fn set_umask(mask: u32) {
        // SAFETY: umask has no preconditions and cannot fail.
        unsafe { libc::umask(mask as libc::mode_t) };
    }

    /// Makes the process non-dumpable (no core files, no ptrace by peers).
    #[cfg(target_os = "linux")]
    pub fn set_non_dumpable() -> io::Result<()> {
        // SAFETY: prctl with constant integer arguments.
        check(unsafe { libc::prctl(libc::PR_SET_DUMPABLE, 0, 0, 0, 0) })
    }

    /// The process and its children can never gain privileges through exec.
    #[cfg(target_os = "linux")]
    pub fn set_no_new_privs() -> io::Result<()> {
        // SAFETY: prctl with constant integer arguments.
        check(unsafe { libc::prctl(libc::PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) })
    }

    /// Hardening that only Linux offers fails closed elsewhere instead of
    /// being skipped silently.
    #[cfg(not(target_os = "linux"))]
    pub fn set_non_dumpable() -> io::Result<()> {
        Err(io::Error::new(io::ErrorKind::Unsupported, "PR_SET_DUMPABLE needs Linux"))
    }

    /// See [`set_non_dumpable`].
    #[cfg(not(target_os = "linux"))]
    pub fn set_no_new_privs() -> io::Result<()> {
        Err(io::Error::new(io::ErrorKind::Unsupported, "PR_SET_NO_NEW_PRIVS needs Linux"))
    }

    #[derive(Clone, Copy, Debug)]
    pub enum Limit {
        CoreFileSize,
        OpenFiles,
        Processes,
    }

    /// Sets both the soft and the hard limit.
    pub fn set_limit(limit: Limit, value: u64) -> io::Result<()> {
        let resource = match limit {
            Limit::CoreFileSize => libc::RLIMIT_CORE,
            Limit::OpenFiles => libc::RLIMIT_NOFILE,
            Limit::Processes => libc::RLIMIT_NPROC,
        };
        let rl = libc::rlimit { rlim_cur: value as libc::rlim_t, rlim_max: value as libc::rlim_t };
        // SAFETY: `rl` is a valid rlimit for the duration of the call.
        check(unsafe { libc::setrlimit(resource, &rl) })
    }

    /// Fixes PATH and removes loader and shell start-up variables.
    ///
    /// Must run during single-threaded start-up: changing the environment
    /// races with any other thread reading it.
    pub fn sanitize_environment(path: &str) {
        // SAFETY: the documented contract above; callers run this from
        // `main` before spawning threads.
        unsafe {
            std::env::set_var("PATH", path);
            for var in ["LD_PRELOAD", "LD_LIBRARY_PATH", "ENV", "BASH_ENV"] {
                std::env::remove_var(var);
            }
        }
    }

    /// Starts the child in a new session so it survives this process.
    pub fn new_session_on_exec(command: &mut Command) {
        // SAFETY: setsid is async-signal-safe, touches no memory of the
        // parent and runs in the forked child just before exec.
        unsafe {
            command.pre_exec(|| {
                libc::setsid();
                Ok(())
            });
        }
    }

    /// Drops every supplementary group in the child before exec.
    pub fn clear_groups_on_exec(command: &mut Command) {
        // SAFETY: setgroups is async-signal-safe and runs in the forked
        // child just before exec.
        unsafe {
            command.pre_exec(|| {
                if libc::setgroups(0, std::ptr::null()) != 0 {
                    return Err(io::Error::last_os_error());
                }
                Ok(())
            });
        }
    }

    pub struct FsSpace {
        pub total: u64,
        pub available: u64,
    }

    /// Size and space available to unprivileged processes of the filesystem
    /// holding `path`.
    pub fn filesystem_space(path: &str) -> io::Result<FsSpace> {
        let path = c_path(path)?;
        // SAFETY: an all-zero statvfs is a valid value for this C struct.
        let mut stat: libc::statvfs = unsafe { std::mem::zeroed() };
        // SAFETY: `path` is NUL-terminated and `stat` is a valid out pointer.
        check(unsafe { libc::statvfs(path.as_ptr(), &mut stat) })?;
        let fragment = stat.f_frsize as u64;
        Ok(FsSpace { total: stat.f_blocks as u64 * fragment, available: stat.f_bavail as u64 * fragment })
    }

    /// Opens `path` read-only without following a final symlink, creating it
    /// with `mode` when `create` is set.
    pub fn open_nofollow(path: &str, create: bool, mode: u32) -> io::Result<File> {
        let path = c_path(path)?;
        let mut flags = libc::O_RDONLY | libc::O_CLOEXEC | libc::O_NOFOLLOW;
        if create {
            flags |= libc::O_CREAT;
        }
        // SAFETY: `path` is NUL-terminated for the duration of the call.
        let fd = unsafe { libc::open(path.as_ptr(), flags, mode as libc::c_uint) };
        if fd < 0 {
            return Err(io::Error::last_os_error());
        }
        // SAFETY: `fd` was just opened and is owned by nothing else.
        Ok(unsafe { File::from_raw_fd(fd) })
    }

    pub fn page_size() -> u64 {
        // SAFETY: sysconf has no preconditions.
        let size = unsafe { libc::sysconf(libc::_SC_PAGESIZE) };
        if size > 0 { size as u64 } else { 4096 }
    }

    /// Every IPv4 address assigned to a local interface, as (name, address).
    pub fn interface_ipv4_addresses() -> io::Result<Vec<(String, Ipv4Addr)>> {
        let mut head: *mut libc::ifaddrs = std::ptr::null_mut();
        // SAFETY: `head` is a valid out pointer; on success the list is ours
        // until freeifaddrs below.
        check(unsafe { libc::getifaddrs(&mut head) })?;
        let mut found = Vec::new();
        let mut cursor = head;
        // SAFETY: `as_ref` returns None for the null that ends the list; every
        // other pointer was produced by getifaddrs and its entries stay valid
        // until freeifaddrs.
        while let Some(entry) = unsafe { cursor.as_ref() } {
            // SAFETY: ifa_addr is either null or points at a sockaddr whose
            // family says which concrete type it is; ifa_name is a
            // NUL-terminated string owned by the list.
            unsafe {
                if !entry.ifa_addr.is_null() && i32::from((*entry.ifa_addr).sa_family) == libc::AF_INET {
                    let sin = &*(entry.ifa_addr as *const libc::sockaddr_in);
                    let name = CStr::from_ptr(entry.ifa_name).to_string_lossy().into_owned();
                    found.push((name, Ipv4Addr::from(u32::from_be(sin.sin_addr.s_addr))));
                }
            }
            cursor = entry.ifa_next;
        }
        // SAFETY: `head` came from getifaddrs and is freed exactly once.
        unsafe { libc::freeifaddrs(head) };
        Ok(found)
    }

    /// Whether the kernel considers the system clock synchronised.
    #[cfg(target_os = "linux")]
    pub fn clock_synchronized() -> bool {
        // SAFETY: an all-zero timex (modes = 0) only reads the clock state.
        let mut timex: libc::timex = unsafe { std::mem::zeroed() };
        // SAFETY: `timex` is a valid, exclusively borrowed timex.
        let state = unsafe { libc::adjtimex(&mut timex) };
        state >= 0 && state != libc::TIME_ERROR
    }

    #[derive(Clone, Copy, Debug)]
    pub struct PeerCredentials {
        pub pid: i32,
        pub uid: u32,
        pub gid: u32,
    }

    /// The kernel-verified identity of the process at the other end of a
    /// Unix socket.
    #[cfg(target_os = "linux")]
    pub fn peer_credentials(stream: &std::os::unix::net::UnixStream) -> io::Result<PeerCredentials> {
        let mut cred = libc::ucred { pid: 0, uid: 0, gid: 0 };
        let mut len = std::mem::size_of::<libc::ucred>() as libc::socklen_t;
        // SAFETY: `cred` and `len` are valid out pointers sized for ucred,
        // which is what SO_PEERCRED writes.
        check(unsafe { libc::getsockopt(stream.as_raw_fd(), libc::SOL_SOCKET, libc::SO_PEERCRED, &mut cred as *mut _ as *mut libc::c_void, &mut len) })?;
        Ok(PeerCredentials { pid: cred.pid, uid: cred.uid, gid: cred.gid })
    }

    #[repr(C)]
    struct CapHeader {
        version: u32,
        pid: i32,
    }

    #[repr(C)]
    #[derive(Default, Clone, Copy, PartialEq)]
    struct CapData {
        effective: u32,
        permitted: u32,
        inheritable: u32,
    }

    const CAPABILITY_VERSION_3: u32 = 0x2008_0522;

    /// Whether this process holds no capabilities at all.
    #[cfg(target_os = "linux")]
    pub fn has_no_capabilities() -> io::Result<bool> {
        let mut header = CapHeader { version: CAPABILITY_VERSION_3, pid: 0 };
        let mut caps = [CapData::default(); 2];
        // SAFETY: header and the two data structs match the kernel ABI for
        // capability version 3.
        if unsafe { libc::syscall(libc::SYS_capget, &mut header, caps.as_mut_ptr()) } != 0 {
            return Err(io::Error::last_os_error());
        }
        Ok(caps == [CapData::default(); 2])
    }

    /// Clears the capability sets and the ambient set.
    #[cfg(target_os = "linux")]
    pub fn drop_all_capabilities() -> io::Result<()> {
        let mut header = CapHeader { version: CAPABILITY_VERSION_3, pid: 0 };
        let caps = [CapData::default(); 2];
        // SAFETY: header and the two data structs match the kernel ABI for
        // capability version 3.
        if unsafe { libc::syscall(libc::SYS_capset, &mut header, caps.as_ptr()) } != 0 {
            return Err(io::Error::last_os_error());
        }
        // SAFETY: prctl with constant integer arguments.
        check(unsafe { libc::prctl(libc::PR_CAP_AMBIENT, libc::PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0) })
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn zeroize_clears_the_buffer() {
        let mut secret = *b"key material";
        super::zeroize(&mut secret);
        assert_eq!(secret, [0; 12]);
    }

    #[cfg(unix)]
    #[test]
    fn wait_readable_reports_the_ready_descriptor() {
        use std::io::Write;
        use std::os::fd::AsFd;
        let (idle, _idle_writer) = std::io::pipe().unwrap();
        let (ready, mut writer) = std::io::pipe().unwrap();
        writer.write_all(b"x").unwrap();
        assert_eq!(super::wait_readable(&[idle.as_fd(), ready.as_fd()]).unwrap(), vec![false, true]);
    }

    #[cfg(unix)]
    #[test]
    fn interface_addresses_include_loopback() {
        let addrs = super::interface_ipv4_addresses().unwrap();
        assert!(addrs.iter().any(|(_, ip)| ip.is_loopback()), "{addrs:?}");
    }
}
