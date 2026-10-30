//! The login rate limiter against a wall clock that was stepped back.
//!
//! The GitHub lab moved the router's clock two hours forward and back again.
//! A login recorded in between opened a window that stayed "in the future",
//! so the source was refused until real time caught up with it. A limiter
//! must treat a window that starts after "now" as expired.

use std::sync::Arc;

use minimalrouter::auth_persistent::PersistentRateLimiter;
use minimalrouter::config::SQLiteStore;
use minimalrouter::goish::time::{Duration, Time};

fn store(name: &str) -> Arc<SQLiteStore> {
    let dir = std::env::temp_dir().join(format!("mr-rate-clock-{}-{}", name, std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);
    Arc::new(SQLiteStore::new_file_store(dir.to_str().unwrap()).unwrap())
}

#[test]
fn a_window_opened_in_the_future_does_not_lock_the_source_out() {
    let store = store("future");
    store.set_rate_limit_bucket("10.0.0.7", 5, Time::now().add(Duration::hours(2))).unwrap();
    // A new limiter has no cache, as after a restart: it reads the bucket back
    // from the database, without a monotonic reading.
    let limiter = PersistentRateLimiter::new(store, 5, Duration::secs(60));
    assert!(limiter.allow("10.0.0.7"), "a bucket dated two hours ahead must count as expired");
    assert!(limiter.allow("10.0.0.7"));
}

#[test]
fn a_full_open_window_still_refuses() {
    let store = store("open");
    store.set_rate_limit_bucket("10.0.0.8", 5, Time::now().add(Duration::secs(-10))).unwrap();
    let limiter = PersistentRateLimiter::new(store, 5, Duration::secs(60));
    assert!(!limiter.allow("10.0.0.8"), "five attempts in the last ten seconds are over the limit");
    assert!(!limiter.allow("10.0.0.8"));
}

#[test]
fn the_limit_applies_and_a_different_source_is_independent() {
    let limiter = PersistentRateLimiter::new(store("limit"), 3, Duration::secs(60));
    assert!(limiter.allow("10.0.0.9") && limiter.allow("10.0.0.9") && limiter.allow("10.0.0.9"));
    assert!(!limiter.allow("10.0.0.9"));
    assert!(limiter.allow("10.0.0.10"));
}
