//! Running external commands with a deadline, without polling.
//!
//! On Unix a helper thread blocks until the child exits without reaping it
//! (`waitid` with `WNOWAIT`), and the caller blocks on a channel with the
//! deadline. Because the child stays unreaped until the caller collects it,
//! killing it at the deadline can never hit a recycled process ID. Pipes are
//! drained by reader threads so a chatty child cannot block on a full pipe.

use std::io::Read;
use std::process::{Child, Command, ExitStatus, Stdio};
use std::sync::mpsc;
use std::time::{Duration, Instant};

/// How long the output readers get after a command was killed at its deadline.
const KILL_PIPE_GRACE: Duration = Duration::from_secs(2);

#[derive(Debug, Default)]
pub struct Output {
    pub stdout: Vec<u8>,
    pub stderr: Vec<u8>,
    /// None when the child could not be started or was killed at the deadline.
    pub status: Option<ExitStatus>,
    pub timed_out: bool,
    pub spawn_error: Option<String>,
    /// More output than `Options::output_limit` allows was produced.
    pub truncated: bool,
}

impl Output {
    pub fn success(&self) -> bool {
        self.status.is_some_and(|s| s.success())
    }

    /// Standard output followed by standard error.
    pub fn combined(&self) -> Vec<u8> {
        let mut all = self.stdout.clone();
        all.extend_from_slice(&self.stderr);
        all
    }

    /// Why the command did not succeed, worded like Go's `exec` errors.
    pub fn error(&self) -> Option<String> {
        if let Some(err) = &self.spawn_error {
            return Some(err.clone());
        }
        if self.timed_out {
            return Some("signal: killed".into());
        }
        match self.status {
            Some(status) if status.success() => None,
            Some(status) => Some(match status.code() {
                Some(code) => format!("exit status {}", code),
                None => "signal: killed".into(),
            }),
            None => Some("process did not start".into()),
        }
    }
}

pub struct Options {
    pub timeout: Duration,
    pub stdin: Option<Vec<u8>>,
    /// Start the child in its own process group and kill the whole group at
    /// the deadline (OpenRC services leave shell descendants behind).
    pub process_group: bool,
    /// After the child exits, wait at most this long for its pipes to close;
    /// a descendant holding them open is then killed with the group, like
    /// Go's `Cmd.WaitDelay`. None waits until the pipes close.
    pub wait_delay: Option<Duration>,
    /// Keep at most this many bytes of each stream.
    pub output_limit: Option<u64>,
    /// False leaves standard output and error attached to this process.
    pub capture: bool,
}

impl Options {
    pub fn timeout(timeout: Duration) -> Options {
        Options { timeout, stdin: None, process_group: false, wait_delay: None, output_limit: None, capture: true }
    }
}

pub fn run(program: &str, args: &[&str], timeout: Duration) -> Output {
    execute(Command::new(program).args(args), Options::timeout(timeout))
}

/// Runs `command` with optional stdin bytes, killing it at `timeout`.
pub fn run_command(command: &mut Command, timeout: Duration, stdin: Option<Vec<u8>>) -> Output {
    execute(command, Options { stdin, ..Options::timeout(timeout) })
}

/// A command in its own process group whose pipes are abandoned after
/// `wait_delay` once it has exited.
pub fn run_group(program: &str, args: &[&str], timeout: Duration, wait_delay: Duration) -> Output {
    execute(Command::new(program).args(args), Options { process_group: true, wait_delay: Some(wait_delay), ..Options::timeout(timeout) })
}

pub fn execute(command: &mut Command, options: Options) -> Output {
    if options.capture {
        command.stdout(Stdio::piped()).stderr(Stdio::piped());
    }
    command.stdin(if options.stdin.is_some() { Stdio::piped() } else { Stdio::null() });
    #[cfg(unix)]
    if options.process_group {
        use std::os::unix::process::CommandExt;
        command.process_group(0);
    }
    let mut child = match command.spawn() {
        Ok(child) => child,
        Err(err) => return Output { spawn_error: Some(err.to_string()), ..Default::default() },
    };
    if let (Some(input), Some(mut pipe)) = (options.stdin, child.stdin.take()) {
        std::thread::spawn(move || {
            use std::io::Write;
            let _ = pipe.write_all(&input);
        });
    }
    let (io_tx, io_rx) = mpsc::channel::<(bool, Vec<u8>, bool)>();
    let mut readers = 0;
    let streams: [(bool, Option<Box<dyn Read + Send>>); 2] =
        [(false, child.stdout.take().map(|p| Box::new(p) as Box<dyn Read + Send>)), (true, child.stderr.take().map(|p| Box::new(p) as Box<dyn Read + Send>))];
    for (is_stderr, pipe) in streams {
        let Some(mut pipe) = pipe else { continue };
        readers += 1;
        let tx = io_tx.clone();
        let limit = options.output_limit;
        std::thread::spawn(move || {
            let mut buf = Vec::new();
            let truncated = match limit {
                Some(limit) => {
                    let _ = (&mut pipe).take(limit).read_to_end(&mut buf);
                    // Keep draining so the child is not blocked by a full pipe.
                    std::io::copy(&mut pipe, &mut std::io::sink()).unwrap_or(0) > 0
                }
                None => {
                    let _ = pipe.read_to_end(&mut buf);
                    false
                }
            };
            let _ = tx.send((is_stderr, buf, truncated));
        });
    }
    drop(io_tx);

    let (status, timed_out) = wait_with_deadline(&mut child, options.timeout, options.process_group);
    let mut output = Output { status, timed_out, ..Default::default() };
    // A killed child can leave a descendant holding the pipes (`sh -c "sleep
    // 30"` forks); never let that stretch a timeout. Without an explicit
    // wait_delay, give the readers a short grace period after a kill.
    let wait_delay = options.wait_delay.or(timed_out.then_some(KILL_PIPE_GRACE));
    let deadline = wait_delay.map(|delay| Instant::now() + delay);
    for _ in 0..readers {
        let received = match deadline {
            Some(deadline) => io_rx.recv_timeout(deadline.saturating_duration_since(Instant::now())).ok(),
            None => io_rx.recv().ok(),
        };
        match received {
            Some((is_stderr, data, truncated)) => {
                output.truncated |= truncated;
                if is_stderr {
                    output.stderr = data;
                } else {
                    output.stdout = data;
                }
            }
            None => {
                kill_group(&child, options.process_group);
                if output.spawn_error.is_none() && output.success() {
                    output.spawn_error = Some("exec: WaitDelay expired before I/O complete".into());
                }
                break;
            }
        }
    }
    output
}

/// Waits for `child` until `timeout`, killing it (and its group) at the
/// deadline. The status is None when the child was killed.
#[cfg(unix)]
fn wait_with_deadline(child: &mut Child, timeout: Duration, group: bool) -> (Option<ExitStatus>, bool) {
    let pid = child.id();
    let (exited_tx, exited_rx) = mpsc::channel();
    std::thread::spawn(move || {
        let _ = crate::sys::wait_exit_unreaped(pid);
        let _ = exited_tx.send(());
    });
    match exited_rx.recv_timeout(timeout) {
        Ok(()) | Err(mpsc::RecvTimeoutError::Disconnected) => (child.wait().ok(), false),
        Err(mpsc::RecvTimeoutError::Timeout) => {
            kill_group(child, group);
            let _ = child.kill();
            let _ = child.wait();
            (None, true)
        }
    }
}

/// Development hosts without `waitid` check the child again shortly.
#[cfg(not(unix))]
fn wait_with_deadline(child: &mut Child, timeout: Duration, _group: bool) -> (Option<ExitStatus>, bool) {
    let started = Instant::now();
    loop {
        match child.try_wait() {
            Ok(Some(status)) => return (Some(status), false),
            Ok(None) if started.elapsed() < timeout => std::thread::sleep(Duration::from_millis(5)),
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                return (None, true);
            }
        }
    }
}

fn kill_group(child: &Child, group: bool) {
    #[cfg(unix)]
    if group {
        crate::sys::kill_process_group(child.id());
    }
    #[cfg(not(unix))]
    let _ = (child, group);
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;

    #[test]
    fn captures_output_and_status() {
        let out = run("/bin/sh", &["-c", "echo out; echo err >&2; exit 3"], Duration::from_secs(10));
        assert_eq!(out.stdout, b"out\n");
        assert_eq!(out.stderr, b"err\n");
        assert_eq!(out.error().as_deref(), Some("exit status 3"));
        assert!(!out.timed_out);
    }

    #[test]
    fn kills_at_the_deadline() {
        let started = Instant::now();
        let out = run("/bin/sh", &["-c", "sleep 30"], Duration::from_millis(200));
        assert!(out.timed_out && out.status.is_none());
        assert!(started.elapsed() < Duration::from_secs(10));
    }

    #[test]
    fn wait_delay_abandons_pipes_held_by_a_descendant() {
        let started = Instant::now();
        let out = run_group("/bin/sh", &["-c", "sleep 30 & echo started"], Duration::from_secs(10), Duration::from_millis(200));
        assert!(started.elapsed() < Duration::from_secs(10));
        assert_eq!(out.spawn_error.as_deref(), Some("exec: WaitDelay expired before I/O complete"));
    }

    #[test]
    fn output_limit_truncates_and_still_drains() {
        let out = execute(
            Command::new("/bin/sh").args(["-c", "head -c 100000 /dev/zero"]),
            Options { output_limit: Some(10), ..Options::timeout(Duration::from_secs(10)) },
        );
        assert!(out.success() && out.truncated);
        assert_eq!(out.stdout.len(), 10);
    }

    #[test]
    fn stdin_is_delivered() {
        let out = run_command(&mut Command::new("/bin/cat"), Duration::from_secs(10), Some(b"hello".to_vec()));
        assert_eq!(out.stdout, b"hello");
    }
}
