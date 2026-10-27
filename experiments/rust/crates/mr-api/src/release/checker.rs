use std::sync::{Mutex, MutexGuard};

use super::*;

/// Keeps discovery off the boot path.
pub const DEFAULT_INITIAL_DELAY: Duration = Duration::minutes(3);
/// A router checks for a release as a convenience, not as a safety signal.
pub const DEFAULT_INTERVAL: Duration = Duration::hours(6);
/// Spreads appliances so a release does not cause a synchronized burst.
pub const DEFAULT_JITTER: Duration = Duration::minutes(30);
pub const DEFAULT_COOLDOWN: Duration = Duration::secs(60);
/// When a cached answer stops being presented as current.
pub const DEFAULT_STALE_AFTER: Duration = Duration::hours(24);
const MAX_BACKOFF: Duration = Duration::hours(4);

/// What the dashboard reads; it always answers immediately from cache.
#[derive(Clone, Debug, Default)]
pub struct Snapshot {
    pub channel: Channel,
    pub checked_at: Time,
    pub last_success_at: Time,
    pub newest: Option<Release>,
    pub candidate: Option<Release>,
    pub error: String,
    pub rate_limited: bool,
    pub next_earliest: Time,
    pub never_succeeded: bool,
    pub stale_after: Duration,
    pub cooldown_expires: Time,
}

impl Snapshot {
    /// Not knowing and knowing there is nothing new are different states.
    pub fn stale(&self, now: Time) -> bool {
        self.last_success_at.is_zero() || now.sub(&self.last_success_at) > self.stale_after
    }
}

crate::sentinel!(ErrCheckTooSoon, "a release check ran recently; try again shortly");
crate::sentinel!(ErrCheckInProgress, "a release check is already running");

#[derive(Default)]
struct State {
    in_flight: bool,
    snapshot: Snapshot,
    etag: String,
    last_attempt: Time,
    backoff_until: Time,
    failures: u32,
    releases: Vec<Release>,
}

/// One cached view of published releases for the whole process, so any
/// number of dashboard tabs share a single upstream request budget.
pub struct Checker {
    catalog: Catalog,
    arch: String,
    channel_fn: Box<dyn Fn() -> Option<Channel> + Send + Sync>,
    clock: Mutex<Box<dyn Fn() -> Time + Send + Sync>>,
    pub initial_delay: Duration,
    pub interval: Duration,
    pub jitter: Duration,
    pub cooldown: Duration,
    pub stale_after: Duration,
    state: Mutex<State>,
}

impl Checker {
    pub fn new(catalog: Catalog, arch: &str, channel: Box<dyn Fn() -> Option<Channel> + Send + Sync>) -> Checker {
        Checker {
            catalog,
            arch: arch.into(),
            channel_fn: channel,
            clock: Mutex::new(Box::new(Time::now)),
            initial_delay: DEFAULT_INITIAL_DELAY,
            interval: DEFAULT_INTERVAL,
            jitter: DEFAULT_JITTER,
            cooldown: DEFAULT_COOLDOWN,
            stale_after: DEFAULT_STALE_AFTER,
            state: Mutex::new(State::default()),
        }
    }

    pub fn set_clock(&self, now: Box<dyn Fn() -> Time + Send + Sync>) {
        *self.clock.lock().unwrap_or_else(|e| e.into_inner()) = now;
    }

    fn now(&self) -> Time {
        (self.clock.lock().unwrap_or_else(|e| e.into_inner()))()
    }

    fn state(&self) -> MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|e| e.into_inner())
    }

    fn channel(&self) -> Channel {
        (self.channel_fn)().unwrap_or(Channel::Stable)
    }

    /// The cached view; never performs I/O.
    pub fn snapshot(&self) -> Snapshot {
        let state = self.state();
        self.snapshot_locked(&state)
    }

    fn snapshot_locked(&self, state: &State) -> Snapshot {
        let mut snapshot = state.snapshot.clone();
        snapshot.channel = self.channel();
        (snapshot.newest, snapshot.candidate) = select_newest(&state.releases, snapshot.channel, &self.arch);
        snapshot.stale_after = self.stale_after;
        snapshot.never_succeeded = state.snapshot.last_success_at.is_zero();
        snapshot.next_earliest = state.backoff_until;
        snapshot.cooldown_expires = state.last_attempt.add(self.cooldown);
        snapshot
    }

    /// An operator-triggered check, subject to the cooldown and single-flight.
    pub fn check_now(&self) -> (Snapshot, Result<()>) {
        {
            let mut state = self.state();
            let now = self.now();
            if state.in_flight {
                return (self.snapshot_locked(&state), Err(ErrCheckInProgress.into()));
            }
            if !state.last_attempt.is_zero() && now.sub(&state.last_attempt) < self.cooldown {
                return (self.snapshot_locked(&state), Err(ErrCheckTooSoon.into()));
            }
            state.in_flight = true;
        }
        let result = self.check();
        self.state().in_flight = false;
        (self.snapshot(), result)
    }

    fn check(&self) -> Result<()> {
        let (etag, channel) = {
            let mut state = self.state();
            state.last_attempt = self.now();
            (state.etag.clone(), self.channel())
        };
        let (result, outcome) = self.catalog.fetch(&etag);
        let mut state = self.state();
        let now = self.now();
        state.snapshot.checked_at = now;
        state.snapshot.channel = channel;
        if let Err(err) = outcome {
            state.failures += 1;
            state.snapshot.rate_limited = crate::goish::errors::is::<ErrRateLimited>(&err);
            state.snapshot.error = err.to_string();
            let mut wait = result.retry_after;
            if wait.0 <= 0 {
                // Exponential backoff on repeated failures.
                wait = Duration(self.interval.0 / 6 * (1i64 << (state.failures - 1).min(6)));
            }
            if wait > MAX_BACKOFF {
                wait = MAX_BACKOFF;
            }
            state.backoff_until = now.add(wait);
            return Err(err);
        }
        state.failures = 0;
        state.backoff_until = Time::zero();
        state.snapshot.error.clear();
        state.snapshot.rate_limited = false;
        state.snapshot.last_success_at = now;
        if !result.not_modified {
            state.etag = result.etag;
            state.releases = result.releases;
        }
        // The channel may change during the fetch; a 304 never retains
        // another channel's selection.
        let (newest, candidate) = select_newest(&state.releases, self.channel(), &self.arch);
        state.snapshot.newest = newest;
        state.snapshot.candidate = candidate;
        Ok(())
    }

    /// Periodic checks until `stop` is set.
    pub fn run(&self, stop: &crate::shutdown::Shutdown) {
        let mut delay = self.initial_delay;
        while stop.sleep(delay.to_std()) {
            let start = {
                let mut state = self.state();
                let blocked = self.now().before(&state.backoff_until);
                let start = !blocked && !state.in_flight;
                if start {
                    state.in_flight = true;
                }
                start
            };
            if start {
                let _ = self.check();
                self.state().in_flight = false;
            }
            delay = self.next_delay();
        }
    }

    fn next_delay(&self) -> Duration {
        let state = self.state();
        let mut delay = self.interval;
        let wait = state.backoff_until.sub(&self.now());
        if wait > delay {
            delay = wait;
        }
        if self.jitter.0 > 0 {
            let mut bytes = [0u8; 8];
            let _ = crate::goish::rand::fill(&mut bytes);
            delay = Duration(delay.0 + (u64::from_le_bytes(bytes) % self.jitter.0 as u64) as i64);
        }
        delay
    }
}
