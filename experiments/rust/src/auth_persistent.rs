//! SQLite-backed sessions and login rate limits (Go `auth/persistent`).
//! Sessions carry the credential generation they were issued under, so a
//! password or TOTP change revokes every older session, including after a
//! restart.

use std::collections::HashMap;
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::Duration as StdDuration;

use crate::auth::{generate_random_hex, ErrGenerationChanged, ErrUnauthorized, Session, ABSOLUTE_TIMEOUT, IDLE_TIMEOUT};
use crate::config::SQLiteStore;
use crate::goish::errors::Result;
use crate::goish::time::{Duration, Time};

const SESSION_LAST_SEEN_PERSIST_INTERVAL: Duration = Duration::MINUTE;

struct State {
    sessions: HashMap<String, Session>,
    last_persisted: HashMap<String, Time>,
}

pub struct PersistentSessionManager {
    store: Arc<SQLiteStore>,
    state: Mutex<State>,
    pub secure_cookies: bool,
}

fn expired(created_at: &Time, last_seen: &Time, now: &Time) -> bool {
    now.sub(created_at) > ABSOLUTE_TIMEOUT || now.sub(last_seen) > IDLE_TIMEOUT
}

impl PersistentSessionManager {
    pub fn new(store: Arc<SQLiteStore>) -> Arc<PersistentSessionManager> {
        Self::with_secure_cookies(store, true)
    }

    /// `secure = false` exists only for a loopback-only preview over HTTP.
    pub fn with_secure_cookies(store: Arc<SQLiteStore>, secure: bool) -> Arc<PersistentSessionManager> {
        let manager = Arc::new(PersistentSessionManager { store, state: Mutex::new(State { sessions: HashMap::new(), last_persisted: HashMap::new() }), secure_cookies: secure });
        let weak = Arc::downgrade(&manager);
        std::thread::spawn(move || loop {
            std::thread::sleep(StdDuration::from_secs(300));
            let Some(manager) = weak.upgrade() else { return };
            {
                let mut state = manager.lock();
                let now = Time::now();
                let stale: Vec<String> = state.sessions.iter().filter(|(_, s)| expired(&s.created_at, &s.last_seen, &now)).map(|(id, _)| id.clone()).collect();
                for id in stale {
                    state.sessions.remove(&id);
                    state.last_persisted.remove(&id);
                }
            }
            let _ = manager.store.clean_expired_sessions(IDLE_TIMEOUT, ABSOLUTE_TIMEOUT);
        });
        manager
    }

    fn lock(&self) -> MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// Authorization uses the in-memory last-seen value; SQLite is only the
    /// restart fallback, so it is refreshed at most once a minute.
    fn reserve_last_seen_persistence(state: &mut State, id: &str, now: Time) -> bool {
        if let Some(last) = state.last_persisted.get(id) {
            if !now.before(last) && now.sub(last) < SESSION_LAST_SEEN_PERSIST_INTERVAL {
                return false;
            }
        }
        state.last_persisted.insert(id.to_string(), now);
        true
    }

    fn persist_last_seen(&self, id: &str, last_seen: Time) {
        if self.store.update_session_last_seen(id, last_seen).is_ok() {
            return;
        }
        // Let the next request retry; keep a newer reservation intact.
        let mut state = self.lock();
        if state.last_persisted.get(id) == Some(&last_seen) {
            state.last_persisted.remove(id);
        }
    }

    pub fn create_session_with_mode(&self, read_only: bool) -> Option<Session> {
        let mut state = self.lock();
        let id = generate_random_hex(32).ok()?;
        let csrf_token = generate_random_hex(32).ok()?;
        let generation = self.store.get_auth_generation().ok()?;
        let now = Time::now();
        let session = Session { id, csrf_token, read_only, auth_generation: generation, created_at: now, last_seen: now };
        self.store.create_session(&session.id, &session.csrf_token, read_only, generation, now, now).ok()?;
        state.sessions.insert(session.id.clone(), session.clone());
        state.last_persisted.insert(session.id.clone(), now);
        Some(session)
    }

    /// Issues a session only if `expected_generation`, read with the hash the
    /// caller just verified, is still current.
    pub fn create_session_with_generation(&self, read_only: bool, expected_generation: u64) -> Result<Session> {
        let id = generate_random_hex(32)?;
        let csrf_token = generate_random_hex(32)?;
        let now = Time::now();
        if !self.store.create_session_if_generation_current(&id, &csrf_token, read_only, expected_generation, now, now)? {
            return Err(ErrGenerationChanged.into());
        }
        let session = Session { id, csrf_token, read_only, auth_generation: expected_generation, created_at: now, last_seen: now };
        let mut state = self.lock();
        state.sessions.insert(session.id.clone(), session.clone());
        state.last_persisted.insert(session.id.clone(), now);
        Ok(session)
    }

    pub fn validate_session(&self, id: &str) -> Result<Session> {
        if id.is_empty() {
            return Err(ErrUnauthorized.into());
        }
        // Durable loads and cache insertion share the revocation lock, so a
        // concurrent miss cannot restore a deleted session.
        let (session, persist_due) = {
            let mut state = self.lock();
            self.validate_locked(&mut state, id)?
        };
        if persist_due {
            self.persist_last_seen(id, session.last_seen);
        }
        Ok(session)
    }

    fn drop_session(&self, state: &mut State, id: &str) {
        state.sessions.remove(id);
        state.last_persisted.remove(id);
        let _ = self.store.delete_session(id);
    }

    fn validate_locked(&self, state: &mut State, id: &str) -> Result<(Session, bool)> {
        let now = Time::now();
        if let Some(cached) = state.sessions.get(id).cloned() {
            let current = self.store.get_auth_generation().map_err(|_| ErrUnauthorized)?;
            if cached.auth_generation != current || expired(&cached.created_at, &cached.last_seen, &now) {
                self.drop_session(state, id);
                return Err(ErrUnauthorized.into());
            }
            let session = state.sessions.get_mut(id).expect("cached");
            session.last_seen = now;
            let copy = session.clone();
            let due = Self::reserve_last_seen_persistence(state, id, now);
            return Ok((copy, due));
        }
        let (csrf_token, read_only, generation, created_at, last_seen) = self.store.get_session(id).map_err(|_| ErrUnauthorized)?;
        let current = self.store.get_auth_generation();
        if current.map_or(true, |c| c != generation) || expired(&created_at, &last_seen, &now) {
            let _ = self.store.delete_session(id);
            return Err(ErrUnauthorized.into());
        }
        // A crash can leave the durable timestamp one interval behind: that
        // can only expire a restarted session early, never extend it.
        let session = Session { id: id.to_string(), csrf_token, read_only, auth_generation: generation, created_at, last_seen: now };
        state.sessions.insert(id.to_string(), session.clone());
        state.last_persisted.insert(id.to_string(), last_seen);
        let due = Self::reserve_last_seen_persistence(state, id, now);
        Ok((session, due))
    }

    /// A failed durable delete leaves the cache intact for a logout retry.
    pub fn destroy_session(&self, id: &str) -> Result<()> {
        if !id.is_empty() {
            let mut state = self.lock();
            self.store.delete_session(id)?;
            state.sessions.remove(id);
            state.last_persisted.remove(id);
        }
        Ok(())
    }

    pub fn destroy_all_sessions(&self) -> Result<()> {
        let mut state = self.lock();
        self.store.delete_all_sessions()?;
        state.sessions.clear();
        state.last_persisted.clear();
        Ok(())
    }
}

/// Login rate limits that survive a restart.
pub struct PersistentRateLimiter {
    store: Arc<SQLiteStore>,
    limit: i64,
    window: Duration,
    cache: Mutex<HashMap<String, (i64, Time)>>,
}

impl PersistentRateLimiter {
    pub fn new(store: Arc<SQLiteStore>, limit: i64, window: Duration) -> Arc<PersistentRateLimiter> {
        let limiter = Arc::new(PersistentRateLimiter { store, limit, window, cache: Mutex::new(HashMap::new()) });
        let weak = Arc::downgrade(&limiter);
        std::thread::spawn(move || loop {
            std::thread::sleep(StdDuration::from_secs(120));
            let Some(limiter) = weak.upgrade() else { return };
            let now = Time::now();
            limiter.cache.lock().unwrap_or_else(|e| e.into_inner()).retain(|_, (_, first)| now.sub(first) <= limiter.window);
            let _ = limiter.store.clean_expired_rate_limit_buckets(limiter.window);
        });
        limiter
    }

    pub fn allow(&self, ip: &str) -> bool {
        let mut cache = self.cache.lock().unwrap_or_else(|e| e.into_inner());
        let now = Time::now();
        if let Some((attempts, first_seen)) = cache.get_mut(ip) {
            if now.sub(first_seen) > self.window {
                *attempts = 1;
                *first_seen = now;
                return self.store.set_rate_limit_bucket(ip, 1, now).is_ok();
            }
            if *attempts >= self.limit {
                return false;
            }
            *attempts += 1;
            return self.store.set_rate_limit_bucket(ip, *attempts, *first_seen).is_ok();
        }
        match self.store.get_rate_limit_bucket(ip) {
            Err(err) if !crate::goish::errors::is::<crate::config::ErrNoRows>(&err) => false,
            Err(_) => {
                cache.insert(ip.to_string(), (1, now));
                self.store.set_rate_limit_bucket(ip, 1, now).is_ok()
            }
            Ok((_, window_start)) if now.sub(&window_start) > self.window => {
                cache.insert(ip.to_string(), (1, now));
                self.store.set_rate_limit_bucket(ip, 1, now).is_ok()
            }
            Ok((attempts, window_start)) if attempts >= self.limit => {
                cache.insert(ip.to_string(), (attempts, window_start));
                false
            }
            Ok((attempts, window_start)) => {
                cache.insert(ip.to_string(), (attempts + 1, window_start));
                self.store.set_rate_limit_bucket(ip, attempts + 1, window_start).is_ok()
            }
        }
    }
}
