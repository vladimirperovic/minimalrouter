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
