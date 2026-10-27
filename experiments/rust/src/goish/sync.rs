//! Context-style cancellation helpers: a buffered wake signal (Go's
//! `make(chan struct{}, 1)`) whose waits also observe a stop flag.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Condvar, Mutex};
use std::time::{Duration, Instant};

const STOP_POLL: Duration = Duration::from_millis(250);

#[derive(Default)]
pub struct Wake {
    pending: Mutex<bool>,
    cv: Condvar,
}

impl Wake {
    pub fn new() -> Wake {
        Wake::default()
    }

    /// Non-blocking send: at most one wake is buffered.
    pub fn notify(&self) {
        *self.pending.lock().unwrap_or_else(|e| e.into_inner()) = true;
        self.cv.notify_all();
    }

    /// Waits for a wake, the timeout or `stop`; true when a wake was consumed.
    pub fn wait(&self, timeout: Duration, stop: &AtomicBool) -> bool {
        let deadline = Instant::now() + timeout;
        let mut pending = self.pending.lock().unwrap_or_else(|e| e.into_inner());
        loop {
            if *pending {
                *pending = false;
                return true;
            }
            let now = Instant::now();
            if stop.load(Ordering::SeqCst) || now >= deadline {
                return false;
            }
            let slice = (deadline - now).min(STOP_POLL);
            pending = self.cv.wait_timeout(pending, slice).unwrap_or_else(|e| e.into_inner()).0;
        }
    }
}

/// Sleeps up to `timeout`; false when `stop` ended the sleep early.
pub fn sleep(timeout: Duration, stop: &AtomicBool) -> bool {
    let deadline = Instant::now() + timeout;
    while !stop.load(Ordering::SeqCst) {
        let now = Instant::now();
        if now >= deadline {
            return true;
        }
        std::thread::sleep((deadline - now).min(STOP_POLL));
    }
    false
}
