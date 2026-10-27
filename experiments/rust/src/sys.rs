//! Direct calls into the C library, behind safe functions. This module is the
//! only place in the crate allowed to use `unsafe`; every block states why the
//! call is sound.
#![allow(unsafe_code)]

#[cfg(unix)]
pub use self::unix::*;

#[cfg(unix)]
mod unix {
    use std::io;
    use std::os::fd::{AsRawFd, BorrowedFd};

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

    /// Sends SIGKILL to process group `pgid`. Errors (the group is already
    /// gone) are ignored.
    pub fn kill_process_group(pgid: u32) {
        let Ok(pgid) = libc::pid_t::try_from(pgid) else { return };
        if pgid <= 1 {
            return;
        }
        // SAFETY: kill has no memory-safety preconditions; a negative pid
        // addresses exactly the process group created for our own child.
        unsafe {
            libc::kill(-pgid, libc::SIGKILL);
        }
    }
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use std::io::Write;
    use std::os::fd::AsFd;

    #[test]
    fn wait_readable_reports_the_ready_descriptor() {
        let (idle, _idle_writer) = std::io::pipe().unwrap();
        let (ready, mut writer) = std::io::pipe().unwrap();
        writer.write_all(b"x").unwrap();
        assert_eq!(wait_readable(&[idle.as_fd(), ready.as_fd()]).unwrap(), vec![false, true]);
    }
}
