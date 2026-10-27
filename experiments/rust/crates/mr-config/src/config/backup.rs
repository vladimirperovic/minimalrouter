//! Password-protected, authenticated configuration backups (Argon2id + AES-GCM).
//! The envelope format is byte-compatible with the Go implementation.

use aes_gcm::aead::{Aead, KeyInit, Payload};
use aes_gcm::{Aes256Gcm, Nonce};
use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use serde::{Deserialize, Serialize};

use super::model::SystemConfig;
use super::store::Snapshot;
use crate::errorf;
use crate::goish::errors::{wrapf, Result};
use crate::goish::{json, rand, time::Time};
use crate::kdf;

const BACKUP_FORMAT_VERSION: i64 = 1;
const BACKUP_ARGON_TIME: u32 = 3;
const BACKUP_ARGON_MEMORY: u32 = 64 * 1024;
const BACKUP_ARGON_THREADS: u8 = 1;
const BACKUP_AAD: &[u8] = b"minimalrouter-backup-v1";

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct BackupPayload {
    pub product: String,
    pub format_version: i64,
    pub created_at: String,
    pub config: SystemConfig,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct BackupEnvelope {
    pub product: String,
    pub format_version: i64,
    pub kdf: String,
    pub argon_time: u32,
    #[serde(rename = "argon_memory_kib")]
    pub argon_memory: u32,
    pub argon_threads: u8,
    pub salt: String,
    pub nonce: String,
    pub ciphertext: String,
}

/// Argon2id with the Go `argon2.IDKey` parameter order.
fn id_key(password: &[u8], salt: &[u8], time: u32, memory: u32, threads: u8, len: usize) -> Result<Vec<u8>> {
    let params = argon2::Params::new(memory, time, threads as u32, Some(len)).map_err(|e| errorf!("argon2 parameters: {}", e))?;
    let argon = argon2::Argon2::new(argon2::Algorithm::Argon2id, argon2::Version::V0x13, params);
    let mut out = vec![0u8; len];
    argon.hash_password_into(password, salt, &mut out).map_err(|e| errorf!("argon2: {}", e))?;
    Ok(out)
}

fn derive_backup_key(passphrase: &str, salt: &[u8], time: u32, memory: u32, threads: u8) -> Result<Vec<u8>> {
    let _slot = kdf::Slot::acquire()?;
    id_key(passphrase.as_bytes(), salt, time, memory, threads, 32)
}

/// Creates a password-protected, authenticated backup. The passphrase is never
/// persisted and the envelope carries the bounded Argon2id parameters.
pub fn encrypt_config_backup(cfg: &SystemConfig, passphrase: &str) -> Result<Vec<u8>> {
    if passphrase.len() < 15 || passphrase.len() > 1024 {
        return Err(errorf!("backup passphrase must contain 15-1024 characters"));
    }
    let payload = json::marshal(&BackupPayload {
        product: "Minimal Router OS".into(),
        format_version: BACKUP_FORMAT_VERSION,
        created_at: Time::now_utc().rfc3339(),
        config: cfg.clone(),
    })
    .map_err(|e| wrapf("marshal backup payload", e))?;
    let salt = rand::bytes(16).map_err(|e| wrapf("generate backup salt", errorf!("{}", e)))?;
    let mut key = derive_backup_key(passphrase, &salt, BACKUP_ARGON_TIME, BACKUP_ARGON_MEMORY, BACKUP_ARGON_THREADS)?;
    let cipher = Aes256Gcm::new_from_slice(&key).map_err(|e| wrapf("create backup cipher", errorf!("{}", e)))?;
    zeroize(&mut key);
    let nonce = rand::bytes(12).map_err(|e| wrapf("generate backup nonce", errorf!("{}", e)))?;
    let ciphertext = cipher
        .encrypt(Nonce::from_slice(&nonce), Payload { msg: &payload, aad: BACKUP_AAD })
        .map_err(|_| errorf!("seal backup"))?;
    let envelope = BackupEnvelope {
        product: "Minimal Router OS encrypted backup".into(),
        format_version: BACKUP_FORMAT_VERSION,
        kdf: "argon2id".into(),
        argon_time: BACKUP_ARGON_TIME,
        argon_memory: BACKUP_ARGON_MEMORY,
        argon_threads: BACKUP_ARGON_THREADS,
        salt: STANDARD.encode(&salt),
        nonce: STANDARD.encode(&nonce),
        ciphertext: STANDARD.encode(&ciphertext),
    };
    Ok(json::marshal_indent(&envelope, "  ")?)
}

pub fn decrypt_config_backup(data: &[u8], passphrase: &str) -> Result<SystemConfig> {
    if data.is_empty() || data.len() > 16 << 20 {
        return Err(errorf!("encrypted backup has an invalid size"));
    }
    // json.Decoder.Decode reads one value; trailing data after it is ignored.
    let mut stream = serde_json::Deserializer::from_slice(data).into_iter::<BackupEnvelope>();
    let envelope = match stream.next() {
        Some(Ok(envelope)) => envelope,
        Some(Err(e)) => return Err(wrapf("decode encrypted backup", e)),
        None => return Err(wrapf("decode encrypted backup", errorf!("EOF"))),
    };
    if envelope.product != "Minimal Router OS encrypted backup"
        || envelope.format_version != BACKUP_FORMAT_VERSION
        || envelope.kdf != "argon2id"
    {
        return Err(errorf!("unsupported backup format"));
    }
    if envelope.argon_time != BACKUP_ARGON_TIME
        || envelope.argon_memory != BACKUP_ARGON_MEMORY
        || envelope.argon_threads != BACKUP_ARGON_THREADS
    {
        return Err(errorf!("unsupported backup KDF profile"));
    }
    let salt = STANDARD.decode(&envelope.salt).ok().filter(|s| s.len() == 16).ok_or_else(|| errorf!("backup salt is invalid"))?;
    let nonce = STANDARD.decode(&envelope.nonce).map_err(|_| errorf!("backup nonce is invalid"))?;
    let ciphertext = STANDARD
        .decode(&envelope.ciphertext)
        .ok()
        .filter(|c| c.len() >= 16)
        .ok_or_else(|| errorf!("backup ciphertext is invalid"))?;
    let mut key = derive_backup_key(passphrase, &salt, envelope.argon_time, envelope.argon_memory, envelope.argon_threads)?;
    let cipher = Aes256Gcm::new_from_slice(&key).map_err(|e| wrapf("create backup cipher", errorf!("{}", e)))?;
    zeroize(&mut key);
    if nonce.len() != 12 {
        return Err(errorf!("backup nonce is invalid"));
    }
    let plaintext = cipher
        .decrypt(Nonce::from_slice(&nonce), Payload { msg: &ciphertext, aad: BACKUP_AAD })
        .map_err(|_| errorf!("backup authentication failed"))?;
    let mut payload: BackupPayload = json::unmarshal(&plaintext).map_err(|_| errorf!("backup payload is invalid"))?;
    if payload.product != "Minimal Router OS" || payload.format_version != BACKUP_FORMAT_VERSION {
        return Err(errorf!("backup payload version is unsupported"));
    }
    // A backup taken by an older release gets the same deterministic legacy
    // migration as the canonical store applies on load.
    payload.config.migrate_legacy_fields();
    payload.config.validate().map_err(|e| wrapf("backup configuration is invalid", e))?;
    Ok(payload.config)
}

use crate::sys::zeroize;

/// Authenticated encryption for snapshots with a raw 32-byte key.
pub struct BackupEncryption {
    key: Vec<u8>,
}

impl BackupEncryption {
    pub fn new(key: &[u8]) -> Result<BackupEncryption> {
        if key.len() != 32 {
            return Err(errorf!("key must be 32 bytes, got {}", key.len()));
        }
        Ok(BackupEncryption { key: key.to_vec() })
    }

    pub fn generate_key() -> Result<Vec<u8>> {
        rand::bytes(32).map_err(|e| errorf!("{}", e))
    }

    /// nonce(12) || ciphertext || tag, base64-encoded.
    pub fn encrypt_snapshot(&self, snap: &Snapshot) -> Result<String> {
        let data = json::marshal(snap).map_err(|e| wrapf("marshal snapshot", e))?;
        let cipher = Aes256Gcm::new_from_slice(&self.key).map_err(|e| wrapf("new cipher", errorf!("{}", e)))?;
        let mut out = rand::bytes(12).map_err(|e| wrapf("read nonce", errorf!("{}", e)))?;
        let sealed = cipher.encrypt(Nonce::from_slice(&out), data.as_slice()).map_err(|_| errorf!("seal snapshot"))?;
        out.extend_from_slice(&sealed);
        Ok(STANDARD.encode(out))
    }

    pub fn decrypt_snapshot(&self, encoded: &str) -> Result<Snapshot> {
        let data = STANDARD.decode(encoded).map_err(|e| wrapf("decode base64", e))?;
        let cipher = Aes256Gcm::new_from_slice(&self.key).map_err(|e| wrapf("new cipher", errorf!("{}", e)))?;
        if data.len() < 12 {
            return Err(errorf!("ciphertext too short"));
        }
        let (nonce, ciphertext) = data.split_at(12);
        let plaintext = cipher
            .decrypt(Nonce::from_slice(nonce), ciphertext)
            .map_err(|_| errorf!("decrypt: cipher: message authentication failed"))?;
        json::unmarshal(&plaintext).map_err(|e| wrapf("unmarshal snapshot", e))
    }

    pub fn key_to_base64(&self) -> String {
        STANDARD.encode(&self.key)
    }

    pub fn key_from_base64(encoded: &str) -> Result<BackupEncryption> {
        BackupEncryption::new(&STANDARD.decode(encoded)?)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn backup_round_trip() {
        let cfg = crate::config::default_config();
        let sealed = encrypt_config_backup(&cfg, "correct horse battery staple").unwrap();
        let restored = decrypt_config_backup(&sealed, "correct horse battery staple").unwrap();
        assert!(json::equal_json(&restored, &cfg));
        assert_eq!(
            decrypt_config_backup(&sealed, "wrong horse battery staple").unwrap_err().to_string(),
            "backup authentication failed"
        );
    }
}
