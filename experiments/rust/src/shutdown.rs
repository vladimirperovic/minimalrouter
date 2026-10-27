//! Process-wide shutdown that wakes every waiting thread at once.
//!
//! Threads block on condition variables, sockets or child processes and are
//! woken only by events; nothing re-checks a flag on a timer, so an idle
//! router does not wake up. `trigger` also runs registered wakers, so blocking
//! calls that a condition variable cannot reach (a socket in `poll(2)`, a
//! channel receive) return as well.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex, MutexGuard, Weak};
use std::time::{Duration, Instant};

type Waker = Box<dyn Fn() + Send + Sync>;

fn lock<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
    mutex.lock().unwrap_or_else(|e| e.into_inner())
}

/// Cheap to clone; every clone observes and triggers the same shutdown.
#[derive(Clone, Default)]
pub struct Shutdown {
    inner: Arc<Inner>,
}

#[derive(Default)]
struct Inner {
    triggered: AtomicBool,
    lock: Mutex<()>,
    cv: Condvar,
    wakers: Mutex<Vec<Waker>>,
}

impl Shutdown {
    pub fn new() -> Shutdown {
        Shutdown::default()
    }

    pub fn is_triggered(&self) -> bool {
        self.inner.triggered.load(Ordering::SeqCst)
    }

    /// Starts the shutdown; later calls do nothing.
    pub fn trigger(&self) {
        if self.inner.triggered.swap(true, Ordering::SeqCst) {
            return;
        }
        // A sleeper checks the flag under `lock` before it waits, so taking
        // the lock here orders this notification after that check.
        drop(lock(&self.inner.lock));
        self.inner.cv.notify_all();
        for waker in lock(&self.inner.wakers).iter() {
            waker();
        }
    }

    /// Runs `waker` when the shutdown starts, or now if it already has.
    pub fn on_trigger(&self, waker: impl Fn() + Send + Sync + 'static) {
        let mut wakers = lock(&self.inner.wakers);
        if self.is_triggered() {
            drop(wakers);
            waker();
            return;
        }
        wakers.push(Box::new(waker));
    }

    /// Sleeps for `timeout`; false when the shutdown cut the sleep short.
    pub fn sleep(&self, timeout: Duration) -> bool {
        let deadline = Instant::now() + timeout;
        let mut guard = lock(&self.inner.lock);
        loop {
            if self.is_triggered() {
                return false;
            }
            let now = Instant::now();
            if now >= deadline {
                return true;
            }
            guard = self.inner.cv.wait_timeout(guard, deadline - now).unwrap_or_else(|e| e.into_inner()).0;
        }
    }

    /// Triggers the shutdown on the first of `signals`, from a thread that
    /// blocks until a signal arrives.
    #[cfg(unix)]
    pub fn trigger_on_signals(&self, signals: &[i32]) -> std::io::Result<()> {
        let mut signals = signal_hook::iterator::Signals::new(signals)?;
        let shutdown = self.clone();
        std::thread::Builder::new().name("signals".into()).spawn(move || {
            if signals.forever().next().is_some() {
                shutdown.trigger();
            }
        })?;
        Ok(())
    }
}

/// A notification that is kept until consumed (at most one is buffered).
/// Waiting also returns when the shutdown starts. A `Wake` is used with one
/// shutdown only.
#[derive(Default)]
pub struct Wake {
    inner: Arc<WakeInner>,
    registered: AtomicBool,
}

#[derive(Default)]
struct WakeInner {
    pending: Mutex<bool>,
    cv: Condvar,
}

impl WakeInner {
    fn interrupt(&self) {
        drop(lock(&self.pending));
        self.cv.notify_all();
    }
}

impl Wake {
    pub fn new() -> Wake {
        Wake::default()
    }

    pub fn notify(&self) {
        *lock(&self.inner.pending) = true;
        self.inner.cv.notify_all();
    }

    /// Waits for a notification, the timeout or the shutdown; true when a
    /// notification was consumed.
    pub fn wait(&self, timeout: Duration, shutdown: &Shutdown) -> bool {
        if !self.registered.swap(true, Ordering::SeqCst) {
            let inner: Weak<WakeInner> = Arc::downgrade(&self.inner);
            shutdown.on_trigger(move || {
                if let Some(inner) = inner.upgrade() {
                    inner.interrupt();
                }
            });
        }
        let deadline = Instant::now() + timeout;
        let mut pending = lock(&self.inner.pending);
        loop {
            if *pending {
                *pending = false;
                return true;
            }
            let now = Instant::now();
            if shutdown.is_triggered() || now >= deadline {
                return false;
            }
            pending = self.inner.cv.wait_timeout(pending, deadline - now).unwrap_or_else(|e| e.into_inner()).0;
        }
    }
}

/// Counts in-flight work and lets shutdown wait until it has drained.
#[derive(Default)]
pub struct InFlight {
    count: Mutex<usize>,
    cv: Condvar,
}

impl InFlight {
    pub fn len(&self) -> usize {
        *lock(&self.count)
    }

    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }

    /// Registers one unit of work until the returned guard is dropped.
    pub fn enter(self: &Arc<Self>) -> InFlightGuard {
        *lock(&self.count) += 1;
        InFlightGuard { owner: self.clone() }
    }

    /// Waits until nothing is in flight or `timeout` passes; true when drained.
    pub fn wait_idle(&self, timeout: Duration) -> bool {
        let count = lock(&self.count);
        let (count, _) = self.cv.wait_timeout_while(count, timeout, |n| *n > 0).unwrap_or_else(|e| e.into_inner());
        *count == 0
    }
}

pub struct InFlightGuard {
    owner: Arc<InFlight>,
}

impl Drop for InFlightGuard {
    fn drop(&mut self) {
        let mut count = lock(&self.owner.count);
        *count -= 1;
        if *count == 0 {
            self.owner.cv.notify_all();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn trigger_wakes_a_long_sleep_at_once() {
        let shutdown = Shutdown::new();
        let sleeper = shutdown.clone();
        let started = Instant::now();
        let handle = std::thread::spawn(move || sleeper.sleep(Duration::from_secs(3600)));
        std::thread::sleep(Duration::from_millis(20));
        shutdown.trigger();
        assert!(!handle.join().unwrap(), "the sleep was cut short");
        assert!(started.elapsed() < Duration::from_secs(5));
        assert!(!shutdown.sleep(Duration::from_secs(3600)), "sleeping after the trigger returns at once");
    }

    #[test]
    fn sleep_runs_to_its_deadline_without_a_trigger() {
        assert!(Shutdown::new().sleep(Duration::from_millis(10)));
    }

    #[test]
    fn wake_keeps_one_notification_and_returns_on_shutdown() {
        let shutdown = Shutdown::new();
        let wake = Arc::new(Wake::new());
        wake.notify();
        wake.notify();
        assert!(wake.wait(Duration::from_secs(3600), &shutdown), "a buffered notification is consumed");
        assert!(!wake.wait(Duration::from_millis(10), &shutdown), "only one was buffered");

        let waiter = (wake.clone(), shutdown.clone());
        let handle = std::thread::spawn(move || waiter.0.wait(Duration::from_secs(3600), &waiter.1));
        std::thread::sleep(Duration::from_millis(20));
        shutdown.trigger();
        assert!(!handle.join().unwrap());
    }

    #[test]
    fn wakers_run_once_and_late_registrations_run_immediately() {
        let shutdown = Shutdown::new();
        let calls = Arc::new(Mutex::new(0));
        let counted = calls.clone();
        shutdown.on_trigger(move || *lock(&counted) += 1);
        shutdown.trigger();
        shutdown.trigger();
        assert_eq!(*lock(&calls), 1);
        let late = calls.clone();
        shutdown.on_trigger(move || *lock(&late) += 10);
        assert_eq!(*lock(&calls), 11);
    }

    #[test]
    fn in_flight_drains() {
        let work = Arc::new(InFlight::default());
        let guard = work.enter();
        assert!(!work.wait_idle(Duration::from_millis(10)));
        let releaser = std::thread::spawn(move || {
            std::thread::sleep(Duration::from_millis(20));
            drop(guard);
        });
        assert!(work.wait_idle(Duration::from_secs(5)));
        releaser.join().unwrap();
        assert!(work.is_empty());
    }
}
