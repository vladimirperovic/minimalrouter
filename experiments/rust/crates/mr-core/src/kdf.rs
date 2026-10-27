//! Bounds how many memory-hard key derivations may run at once.
//!
//! Every Argon2id derivation in this project — login, password change, recovery
//! credential reset and encrypted backups — reserves 64 MiB of live working
//! memory, while routerd runs under a 128 MiB budget. Login rate limits bound
//! attempts per minute, not attempts in flight, so several simultaneous
//! requests could otherwise reserve hundreds of MiB at once and push the
//! management plane into the kernel OOM killer. Admission control is the only
//! bound that actually holds.

use std::sync::{Condvar, Mutex};
use std::time::Duration;

/// Deliberately one: a single derivation already reserves half the process
/// budget on the smallest supported appliance.
const MAX_CONCURRENT: usize = 1;
/// How long a caller queues before being told to retry, so a burst degrades
/// into rejections rather than unbounded waiting threads.
const QUEUE_WAIT: Duration = Duration::from_secs(15);

crate::sentinel!(
    /// No derivation slot became available in time. Callers should surface it
    /// as a retryable service condition, never as a credential rejection.
    ErrBusy,
    "key derivation capacity is exhausted; retry shortly"
);

static IN_USE: Mutex<usize> = Mutex::new(0);
static RELEASED: Condvar = Condvar::new();

/// Reserves a derivation slot, waiting at most QUEUE_WAIT. Every successful
/// acquire must be paired with a release; prefer [`Slot`].
pub fn acquire() -> Result<(), ErrBusy> {
    acquire_within(QUEUE_WAIT)
}

fn acquire_within(wait: Duration) -> Result<(), ErrBusy> {
    let guard = IN_USE.lock().unwrap_or_else(|e| e.into_inner());
    let (mut guard, timeout) = RELEASED
        .wait_timeout_while(guard, wait, |in_use| *in_use >= MAX_CONCURRENT)
        .unwrap_or_else(|e| e.into_inner());
    if timeout.timed_out() && *guard >= MAX_CONCURRENT {
        return Err(ErrBusy);
    }
    *guard += 1;
    Ok(())
}

/// Returns a slot reserved by acquire. Releasing an unheld slot is a no-op.
pub fn release() {
    let mut guard = IN_USE.lock().unwrap_or_else(|e| e.into_inner());
    if *guard > 0 {
        *guard -= 1;
        RELEASED.notify_one();
    }
}

/// RAII form of acquire/release.
pub struct Slot(());

impl Slot {
    pub fn acquire() -> Result<Slot, ErrBusy> {
        acquire().map(|_| Slot(()))
    }
}

impl Drop for Slot {
    fn drop(&mut self) {
        release();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn second_caller_is_rejected_while_slot_is_held() {
        // Other tests derive keys concurrently; wait for the shared slot first.
        acquire_within(Duration::from_secs(60)).unwrap();
        assert_eq!(acquire_within(Duration::from_millis(20)), Err(ErrBusy));
        release();
    }
}
