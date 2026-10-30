//! Password hashing (Argon2id), login rate limiting, in-memory sessions and TOTP.

use base64::Engine;
use base64::engine::general_purpose::STANDARD_NO_PAD;
use hmac::{Hmac, Mac};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use crate::config::SQLiteStore;
use crate::goish::errors::{Result, wrapf};
use crate::goish::time::{Duration as GoDuration, Time};
use crate::{errorf, kdf, sentinel};

// Argon2id parameters following SECURITY.md section 5.
const ARGON_MEMORY: u32 = 64 * 1024;
const ARGON_ITERATIONS: u32 = 3;
const ARGON_PARALLELISM: u32 = 2;
const ARGON_KEY_LEN: usize = 32;
const ARGON_SALT_LEN: usize = 16;
const ARGON_VERSION: u32 = 0x13;

sentinel!(ErrPasswordTooShort, "password must be at least 12 characters long");
sentinel!(ErrPasswordTooLong, "password must be at most 1024 bytes");
sentinel!(ErrInvalidHash, "the encoded hash is not in the correct format");
sentinel!(ErrIncompatibleVer, "incompatible version of argon2");

fn argon2_id(password: &[u8], salt: &[u8], time: u32, memory: u32, threads: u32, len: usize) -> Result<Vec<u8>> {
    let params = argon2::Params::new(memory, time, threads, Some(len)).map_err(|e| errorf!("argon2 parameters: {}", e))?;
    let mut out = vec![0u8; len];
    argon2::Argon2::new(argon2::Algorithm::Argon2id, argon2::Version::V0x13, params)
        .hash_password_into(password, salt, &mut out)
        .map_err(|e| errorf!("argon2: {}", e))?;
    Ok(out)
}

/// Hashes a password with Argon2id in the Go encoding:
/// `$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>` (unpadded base64).
pub fn hash_password(password: &str) -> Result<String> {
    if password.chars().count() < 12 {
        return Err(ErrPasswordTooShort.into());
    }
    if password.len() > 1024 {
        return Err(ErrPasswordTooLong.into());
    }
    let salt = crate::goish::rand::bytes(ARGON_SALT_LEN).map_err(|e| wrapf("failed to generate random salt", errorf!("{}", e)))?;
    let hash = {
        let _slot = kdf::Slot::acquire()?;
        argon2_id(password.as_bytes(), &salt, ARGON_ITERATIONS, ARGON_MEMORY, ARGON_PARALLELISM, ARGON_KEY_LEN)?
    };
    Ok(format!(
        "$argon2id$v={}$m={},t={},p={}${}${}",
        ARGON_VERSION,
        ARGON_MEMORY,
        ARGON_ITERATIONS,
        ARGON_PARALLELISM,
        STANDARD_NO_PAD.encode(&salt),
        STANDARD_NO_PAD.encode(&hash)
    ))
}

/// Parses `name=<digits>` the way `fmt.Sscanf("name=%d")` accepts it.
fn scan_field<T: std::str::FromStr>(text: &str, name: &str) -> Option<T> {
    let digits = text.strip_prefix(name)?.strip_prefix('=')?;
    if digits.is_empty() || !digits.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    digits.parse().ok()
}

/// Constant-time verification of a password against an Argon2id hash.
pub fn verify_password(password: &str, encoded_hash: &str) -> Result<bool> {
    if password.len() > 1024 || encoded_hash.len() > 512 {
        return Err(ErrInvalidHash.into());
    }
    let parts: Vec<&str> = encoded_hash.split('$').collect();
    if parts.len() != 6 {
        return Err(ErrInvalidHash.into());
    }
    let version: u32 = scan_field(parts[2], "v").ok_or(ErrInvalidHash)?;
    if version != ARGON_VERSION {
        return Err(ErrIncompatibleVer.into());
    }
    let params: Vec<&str> = parts[3].split(',').collect();
    if params.len() != 3 {
        return Err(ErrInvalidHash.into());
    }
    let memory: u32 = scan_field(params[0], "m").ok_or(ErrInvalidHash)?;
    let iterations: u32 = scan_field(params[1], "t").ok_or(ErrInvalidHash)?;
    let parallelism: u8 = scan_field(params[2], "p").ok_or(ErrInvalidHash)?;
    if !(16 * 1024..=256 * 1024).contains(&memory) || !(1..=10).contains(&iterations) || !(1..=8).contains(&parallelism) {
        return Err(ErrInvalidHash.into());
    }
    let salt = crate::goish::base64::raw_std_decode(parts[4]).ok_or(ErrInvalidHash)?;
    if !(16..=64).contains(&salt.len()) {
        return Err(ErrInvalidHash.into());
    }
    let hash = crate::goish::base64::raw_std_decode(parts[5]).ok_or(ErrInvalidHash)?;
    if !(16..=64).contains(&hash.len()) {
        return Err(ErrInvalidHash.into());
    }
    let other = {
        let _slot = kdf::Slot::acquire()?;
        argon2_id(password.as_bytes(), &salt, iterations, memory, parallelism as u32, hash.len())?
    };
    Ok(subtle::ConstantTimeEq::ct_eq(&hash[..], &other[..]).into())
}

/// Enforces N attempts per window per source IP.
pub struct RateLimiter {
    history: Mutex<HashMap<String, (u32, Instant)>>,
    limit: u32,
    window: Duration,
}

impl RateLimiter {
    pub fn new(limit: u32, window: Duration) -> Arc<RateLimiter> {
        let limiter = Arc::new(RateLimiter { history: Mutex::new(HashMap::new()), limit, window });
        let weak = Arc::downgrade(&limiter);
        std::thread::spawn(move || {
            loop {
                std::thread::sleep(Duration::from_secs(120));
                let Some(limiter) = weak.upgrade() else { return };
                let now = Instant::now();
                limiter.history.lock().unwrap_or_else(|e| e.into_inner()).retain(|_, (_, first)| now.duration_since(*first) <= limiter.window);
            }
        });
        limiter
    }

    pub fn allow(&self, ip: &str) -> bool {
        let mut history = self.history.lock().unwrap_or_else(|e| e.into_inner());
        let now = Instant::now();
        match history.get_mut(ip) {
            None => {
                history.insert(ip.to_string(), (1, now));
                true
            }
            Some(rec) if now.duration_since(rec.1) > self.window => {
                *rec = (1, now);
                true
            }
            Some(rec) if rec.0 >= self.limit => false,
            Some(rec) => {
                rec.0 += 1;
                true
            }
        }
    }
}

pub const SESSION_COOKIE_NAME: &str = "mr_session";
pub const CSRF_HEADER_NAME: &str = "X-CSRF-Token";
pub const IDLE_TIMEOUT: GoDuration = GoDuration::minutes(30);
pub const ABSOLUTE_TIMEOUT: GoDuration = GoDuration::hours(8);

sentinel!(ErrUnauthorized, "unauthorized or expired session");
sentinel!(
    /// The credential generation advanced between verification and session
    /// issue; the verified credential is already revoked.
    ErrGenerationChanged,
    "authentication credentials changed; please log in again"
);

/// An authenticated administrator session.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
pub struct Session {
    pub id: String,
    pub csrf_token: String,
    pub read_only: bool,
    #[serde(skip)]
    pub auth_generation: u64,
    pub created_at: Time,
    pub last_seen: Time,
}

pub fn generate_random_hex(n: usize) -> Result<String> {
    Ok(hex::encode(crate::goish::rand::bytes(n).map_err(|e| errorf!("{}", e))?))
}

fn expired(session: &Session, now: &Time) -> bool {
    now.sub(&session.created_at) > ABSOLUTE_TIMEOUT || now.sub(&session.last_seen) > IDLE_TIMEOUT
}

/// Server-side sessions held in memory.
pub struct SessionManager {
    sessions: Mutex<HashMap<String, Session>>,
}

impl SessionManager {
    pub fn new() -> Arc<SessionManager> {
        let manager = Arc::new(SessionManager { sessions: Mutex::new(HashMap::new()) });
        let weak = Arc::downgrade(&manager);
        std::thread::spawn(move || {
            loop {
                std::thread::sleep(Duration::from_secs(300));
                let Some(manager) = weak.upgrade() else { return };
                let now = Time::now();
                manager.sessions.lock().unwrap_or_else(|e| e.into_inner()).retain(|_, s| !expired(s, &now));
            }
        });
        manager
    }

    pub fn create_session(&self, read_only: bool) -> Option<Session> {
        let now = Time::now();
        let session = Session {
            id: generate_random_hex(32).ok()?,
            csrf_token: generate_random_hex(32).ok()?,
            read_only,
            auth_generation: 0,
            created_at: now,
            last_seen: now,
        };
        self.sessions.lock().unwrap_or_else(|e| e.into_inner()).insert(session.id.clone(), session.clone());
        Some(session)
    }

    /// Verifies the session cookie value; constant-time key comparison.
    pub fn validate_session(&self, cookie: &str) -> Result<Session> {
        if cookie.is_empty() {
            return Err(ErrUnauthorized.into());
        }
        let mut sessions = self.sessions.lock().unwrap_or_else(|e| e.into_inner());
        let id = sessions.keys().find(|sid| subtle::ConstantTimeEq::ct_eq(sid.as_bytes(), cookie.as_bytes()).into()).cloned().ok_or(ErrUnauthorized)?;
        let now = Time::now();
        if expired(&sessions[&id], &now) {
            sessions.remove(&id);
            return Err(ErrUnauthorized.into());
        }
        let session = sessions.get_mut(&id).expect("present");
        session.last_seen = now;
        Ok(session.clone())
    }

    pub fn destroy_session(&self, cookie: &str) {
        if !cookie.is_empty() {
            self.sessions.lock().unwrap_or_else(|e| e.into_inner()).remove(cookie);
        }
    }

    pub fn destroy_all_sessions(&self) {
        self.sessions.lock().unwrap_or_else(|e| e.into_inner()).clear();
    }
}

/// The `Set-Cookie` header value for a session (HttpOnly, Secure, Strict).
pub fn session_cookie(session_id: &str) -> String {
    format!("{}={}; Path=/; Max-Age={}; HttpOnly; Secure; SameSite=Strict", SESSION_COOKIE_NAME, session_id, ABSOLUTE_TIMEOUT.0 / 1_000_000_000)
}

/// The `Set-Cookie` header value that expires the session cookie.
pub fn expired_session_cookie() -> String {
    format!("{}=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Strict", SESSION_COOKIE_NAME)
}

pub const TOTP_PERIOD: i64 = 30;
pub const TOTP_DIGITS: usize = 6;
pub const TOTP_ISSUER: &str = "Minimal Router OS";

sentinel!(ErrTOTPNotConfigured, "TOTP not configured");

const BASE32: &[u8; 32] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

/// `base32.StdEncoding.EncodeToString`.
pub fn base32_encode(data: &[u8]) -> String {
    let mut out = String::with_capacity(data.len().div_ceil(5) * 8);
    for chunk in data.chunks(5) {
        let mut buf = [0u8; 5];
        buf[..chunk.len()].copy_from_slice(chunk);
        let bits = u64::from_be_bytes([0, 0, 0, buf[0], buf[1], buf[2], buf[3], buf[4]]);
        let chars = match chunk.len() {
            1 => 2,
            2 => 4,
            3 => 5,
            4 => 7,
            _ => 8,
        };
        for i in 0..8 {
            if i < chars {
                out.push(BASE32[((bits >> (35 - i * 5)) & 31) as usize] as char);
            } else {
                out.push('=');
            }
        }
    }
    out
}

/// `base32.StdEncoding.DecodeString`: padded quanta, CR/LF ignored, unused
/// trailing bits not checked.
pub fn base32_decode(text: &str) -> Option<Vec<u8>> {
    let clean: Vec<u8> = text.bytes().filter(|b| *b != b'\r' && *b != b'\n').collect();
    if clean.len() % 8 != 0 {
        return None;
    }
    let mut out = Vec::with_capacity(clean.len() / 8 * 5);
    for (q, quantum) in clean.chunks(8).enumerate() {
        let pad = quantum.iter().rev().take_while(|b| **b == b'=').count();
        let data_len = 8 - pad;
        // Padding may appear only in the final quantum, with a valid length.
        if pad > 0 && q != clean.len() / 8 - 1 {
            return None;
        }
        let bytes = match data_len {
            8 => 5,
            7 => 4,
            5 => 3,
            4 => 2,
            2 => 1,
            _ => return None,
        };
        let mut bits: u64 = 0;
        for (i, b) in quantum[..data_len].iter().enumerate() {
            let v = BASE32.iter().position(|c| c == b)? as u64;
            bits |= v << (35 - i * 5);
        }
        let be = bits.to_be_bytes();
        out.extend_from_slice(&be[3..3 + bytes]);
    }
    Some(out)
}

/// A new random 160-bit TOTP secret, base32 encoded.
pub fn generate_totp_secret() -> Result<String> {
    Ok(base32_encode(&crate::goish::rand::bytes(20).map_err(|e| errorf!("{}", e))?))
}

fn canonical_totp_inputs(secret: &str, code: &str) -> (String, String) {
    let mut secret = crate::goish::strings::to_upper(&secret.replace(' ', ""));
    // Decode/re-encode canonicalises line breaks and unused padding bits.
    if let Some(decoded) = base32_decode(&secret) {
        secret = base32_encode(&decoded);
    }
    (secret, code.trim().to_string())
}

/// Identifies a secret/code pair with the same canonical inputs as
/// [`validate_totp`]. Callers must validate the code before consuming it.
pub fn totp_replay_key(secret: &str, code: &str) -> [u8; 32] {
    let (secret, code) = canonical_totp_inputs(secret, code);
    Sha256::digest(format!("{}\0{}", secret, code).as_bytes()).into()
}

/// Validates a code against the current, previous and next 30 s window.
pub fn validate_totp(secret: &str, code: &str) -> bool {
    validate_totp_at(secret, code, Time::now().unix())
}

fn validate_totp_at(secret: &str, code: &str, now_unix: i64) -> bool {
    let (secret, code) = canonical_totp_inputs(secret, code);
    if code.len() != TOTP_DIGITS {
        return false;
    }
    let Some(decoded) = base32_decode(&secret) else { return false };
    let counter = now_unix.div_euclid(TOTP_PERIOD);
    (-1..=1).any(|i| subtle::ConstantTimeEq::ct_eq(code.as_bytes(), compute_totp(&decoded, counter + i).as_bytes()).into())
}

fn compute_totp(secret: &[u8], counter: i64) -> String {
    let mut mac = <Hmac<sha1::Sha1> as Mac>::new_from_slice(secret).expect("HMAC accepts any key length");
    mac.update(&(counter as u64).to_be_bytes());
    let hash = mac.finalize().into_bytes();
    let offset = (hash[hash.len() - 1] & 0x0f) as usize;
    let truncated = u32::from_be_bytes([hash[offset], hash[offset + 1], hash[offset + 2], hash[offset + 3]]) & 0x7fff_ffff;
    format!("{:06}", truncated % 1_000_000)
}

/// The otpauth:// URI for QR code generation.
pub fn build_totp_uri(username: &str, secret: &str) -> String {
    format!("otpauth://totp/{0}:{1}?secret={2}&issuer={0}&algorithm=SHA1&digits=6&period=30", TOTP_ISSUER, username, secret)
}

/// Enables TOTP for admin; returns (secret, uri).
pub fn setup_totp(store: &SQLiteStore) -> Result<(String, String)> {
    let secret = generate_totp_secret()?;
    store.set_admin_totp_secret(&secret)?;
    let uri = build_totp_uri("admin", &secret);
    Ok((secret, uri))
}

pub fn disable_totp(store: &SQLiteStore) -> Result<()> {
    store.clear_admin_totp_secret()
}

pub fn verify_totp(store: &SQLiteStore, code: &str) -> Result<bool> {
    let secret = store.get_admin_totp_secret()?;
    if secret.is_empty() {
        return Err(ErrTOTPNotConfigured.into());
    }
    Ok(validate_totp(&secret, code))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rfc6238_sha1_vectors() {
        // RFC 6238 appendix B, 8-digit codes truncated to their last 6 digits.
        let secret = base32_encode(b"12345678901234567890");
        assert!(validate_totp_at(&secret, "287082", 59));
        assert!(validate_totp_at(&secret, "081804", 1111111109));
        assert!(validate_totp_at(&secret, "050471", 1111111111));
        assert!(!validate_totp_at(&secret, "000000", 59));
    }

    #[test]
    fn base32_matches_go() {
        assert_eq!(base32_encode(b"f"), "MY======");
        assert_eq!(base32_encode(b"foobar"), "MZXW6YTBOI======");
        assert_eq!(base32_decode("MZXW6YTBOI======").unwrap(), b"foobar");
        assert!(base32_decode("MZXW6YTBOI").is_none());
        assert_eq!(base32_decode("MZXW6\nYTBOI======").unwrap(), b"foobar");
    }

    #[test]
    fn password_hash_round_trip_and_go_format() {
        let hash = hash_password("correct horse battery").unwrap();
        assert!(hash.starts_with("$argon2id$v=19$m=65536,t=3,p=2$"));
        assert!(verify_password("correct horse battery", &hash).unwrap());
        assert!(!verify_password("wrong horse battery", &hash).unwrap());
        assert!(crate::goish::errors::is::<ErrPasswordTooShort>(&hash_password("short").unwrap_err()));
        assert!(crate::goish::errors::is::<ErrInvalidHash>(&verify_password("x", "$argon2id$v=19$m=1,t=3,p=2$a$b").unwrap_err()));
    }
}
