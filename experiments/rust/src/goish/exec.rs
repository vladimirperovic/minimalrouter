//! `exec.CommandContext` with a deadline: the child is killed when the
//! timeout expires, and both pipes are drained so a chatty child cannot block.

use std::io::Read;
use std::process::{Command, ExitStatus, Stdio};
use std::time::{Duration, Instant};

#[derive(Debug, Default)]
pub struct Output {
    pub stdout: Vec<u8>,
    pub stderr: Vec<u8>,
    /// None when the child could not be started or was killed at the deadline.
    pub status: Option<ExitStatus>,
    pub timed_out: bool,
    pub spawn_error: Option<String>,
}

impl Output {
    pub fn success(&self) -> bool {
        self.status.is_some_and(|s| s.success())
    }

    /// Go's `CombinedOutput` approximation: stdout then stderr.
    pub fn combined(&self) -> Vec<u8> {
        let mut all = self.stdout.clone();
        all.extend_from_slice(&self.stderr);
        all
    }

    /// The error `cmd.Output()` would return, if any.
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

pub fn run(program: &str, args: &[&str], timeout: Duration) -> Output {
    run_command(Command::new(program).args(args), timeout, None)
}

/// Runs `command` with optional stdin bytes, killing it at `timeout`.
pub fn run_command(command: &mut Command, timeout: Duration, stdin: Option<Vec<u8>>) -> Output {
    command.stdout(Stdio::piped()).stderr(Stdio::piped());
    command.stdin(if stdin.is_some() { Stdio::piped() } else { Stdio::null() });
    let mut child = match command.spawn() {
        Ok(child) => child,
        Err(err) => return Output { spawn_error: Some(err.to_string()), ..Default::default() },
    };
    if let (Some(input), Some(mut pipe)) = (stdin, child.stdin.take()) {
        std::thread::spawn(move || {
            use std::io::Write;
            let _ = pipe.write_all(&input);
        });
    }
    let mut stdout = child.stdout.take().expect("piped");
    let mut stderr = child.stderr.take().expect("piped");
    let out = std::thread::spawn(move || {
        let mut buf = Vec::new();
        let _ = stdout.read_to_end(&mut buf);
        buf
    });
    let err = std::thread::spawn(move || {
        let mut buf = Vec::new();
        let _ = stderr.read_to_end(&mut buf);
        buf
    });
    let started = Instant::now();
    let (status, timed_out) = loop {
        match child.try_wait() {
            Ok(Some(status)) => break (Some(status), false),
            Ok(None) if started.elapsed() < timeout => std::thread::sleep(Duration::from_millis(5)),
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                break (None, true);
            }
        }
    };
    Output { stdout: out.join().unwrap_or_default(), stderr: err.join().unwrap_or_default(), status, timed_out, spawn_error: None }
}

/// A command in its own process group (OpenRC services spawn shell
/// children): the deadline kills the whole group, and pipes held open by a
/// descendant are abandoned after `wait_delay`, like Go's Cmd.WaitDelay.
pub fn run_group(program: &str, args: &[&str], timeout: Duration, wait_delay: Duration) -> Output {
    let mut command = Command::new(program);
    command.args(args).stdin(Stdio::null()).stdout(Stdio::piped()).stderr(Stdio::piped());
    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        command.process_group(0);
    }
    let mut child = match command.spawn() {
        Ok(child) => child,
        Err(err) => return Output { spawn_error: Some(err.to_string()), ..Default::default() },
    };
    let pid = child.id();
    let (tx, rx) = std::sync::mpsc::channel::<(bool, Vec<u8>)>();
    for (is_err, mut pipe) in [(false, Box::new(child.stdout.take().expect("piped")) as Box<dyn Read + Send>), (true, Box::new(child.stderr.take().expect("piped")))] {
        let tx = tx.clone();
        std::thread::spawn(move || {
            let mut buf = Vec::new();
            let _ = pipe.read_to_end(&mut buf);
            let _ = tx.send((is_err, buf));
        });
    }
    drop(tx);
    let kill_group = || {
        #[cfg(unix)]
        unsafe {
            libc::kill(-(pid as i32), libc::SIGKILL);
        }
        #[cfg(not(unix))]
        let _ = pid;
    };
    let started = Instant::now();
    let (status, timed_out) = loop {
        match child.try_wait() {
            Ok(Some(status)) => break (Some(status), false),
            Ok(None) if started.elapsed() < timeout => std::thread::sleep(Duration::from_millis(10)),
            _ => {
                kill_group();
                let _ = child.kill();
                let _ = child.wait();
                break (None, true);
            }
        }
    };
    let mut output = Output { status, timed_out, ..Default::default() };
    let deadline = Instant::now() + wait_delay;
    for _ in 0..2 {
        match rx.recv_timeout(deadline.saturating_duration_since(Instant::now())) {
            Ok((true, data)) => output.stderr = data,
            Ok((false, data)) => output.stdout = data,
            Err(_) => {
                kill_group();
                if output.spawn_error.is_none() && output.success() {
                    output.spawn_error = Some("exec: WaitDelay expired before I/O complete".into());
                }
                break;
            }
        }
    }
    output
}
