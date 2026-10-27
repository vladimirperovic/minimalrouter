//! The v0.2 portable backup: configuration plus DNS filter policy. Version-one
//! import stays in config for legacy compatibility.

use aes_gcm::aead::{Aead, KeyInit, Payload as AeadPayload};
use aes_gcm::{Aes256Gcm, Nonce};
use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use serde::{Deserialize, Serialize};

use crate::config::{decrypt_config_backup, BackupEnvelope, SystemConfig};
use crate::dnsfilter::Policy;
use crate::errorf;
use crate::goish::errors::Result;
use crate::goish::{json, rand, time::Time};

const PRODUCT: &str = "Minimal Router OS encrypted backup";
const AAD: &[u8] = b"minimalrouter-backup-v2";

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Payload {
    pub product: String,
    pub version: i64,
    pub created_at: String,
    pub config: SystemConfig,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub dns_filter: Option<Policy>,
}

fn cipher_for(password: &str, salt: &[u8]) -> Result<Aes256Gcm> {
    if password.len() > 1024 {
        return Err(errorf!("backup password is too long"));
    }
    let _slot = crate::kdf::Slot::acquire()?;
    let params = argon2::Params::new(64 * 1024, 3, 1, Some(32)).map_err(|e| errorf!("{}", e))?;
    let mut key = [0u8; 32];
    argon2::Argon2::new(argon2::Algorithm::Argon2id, argon2::Version::V0x13, params)
        .hash_password_into(password.as_bytes(), salt, &mut key)
        .map_err(|e| errorf!("{}", e))?;
    let cipher = Aes256Gcm::new_from_slice(&key).map_err(|e| errorf!("{}", e))?;
    key.fill(0);
    Ok(cipher)
}

pub fn encrypt(cfg: &SystemConfig, policy: Option<&Policy>, password: &str) -> Result<Vec<u8>> {
    if password.len() < 12 || password.len() > 1024 {
        return Err(errorf!("dashboard password must contain 12-1024 characters"));
    }
    if let Some(policy) = policy {
        policy.validate()?;
    }
    let mut plaintext = json::marshal(&Payload {
        product: "Minimal Router OS".into(),
        version: 2,
        created_at: Time::now_utc().rfc3339(),
        config: cfg.clone(),
        dns_filter: policy.cloned(),
    })?;
    let salt = rand::bytes(16).map_err(|e| errorf!("{}", e))?;
    let aead = cipher_for(password, &salt)?;
    let nonce = rand::bytes(12).map_err(|e| errorf!("{}", e))?;
    let sealed = aead.encrypt(Nonce::from_slice(&nonce), AeadPayload { msg: &plaintext, aad: AAD }).map_err(|_| errorf!("seal backup"))?;
    plaintext.fill(0);
    let envelope = BackupEnvelope {
        product: PRODUCT.into(),
        format_version: 2,
        kdf: "argon2id".into(),
        argon_time: 3,
        argon_memory: 64 * 1024,
        argon_threads: 1,
        salt: STANDARD.encode(&salt),
        nonce: STANDARD.encode(&nonce),
        ciphertext: STANDARD.encode(sealed),
    };
    Ok(json::marshal_indent(&envelope, "  ")?)
}

pub fn decrypt(raw: &[u8], password: &str) -> Result<Payload> {
    if raw.is_empty() || raw.len() > 16 << 20 {
        return Err(errorf!("invalid encrypted backup size"));
    }
    let envelope: BackupEnvelope = match crate::apply::decode_single(raw) {
        Ok(e) => e,
        Err(crate::apply::SingleDecodeError::Trailing) => return Err(errorf!("backup contains trailing data")),
        Err(crate::apply::SingleDecodeError::Invalid(e)) => return Err(e.into()),
    };
    if envelope.format_version == 1 {
        let cfg = decrypt_config_backup(raw, password)?;
        return Ok(Payload { config: cfg, version: 1, ..Default::default() });
    }
    if envelope.product != PRODUCT
        || envelope.format_version != 2
        || envelope.kdf != "argon2id"
        || envelope.argon_time != 3
        || envelope.argon_memory != 64 * 1024
        || envelope.argon_threads != 1
    {
        return Err(errorf!("unsupported backup encryption profile"));
    }
    let salt = crate::goish::base64::std_decode(&envelope.salt).filter(|s| s.len() == 16).ok_or_else(|| errorf!("invalid backup salt"))?;
    let nonce = crate::goish::base64::std_decode(&envelope.nonce).filter(|n| n.len() == 12).ok_or_else(|| errorf!("invalid backup nonce"))?;
    let ciphertext = crate::goish::base64::std_decode(&envelope.ciphertext).filter(|c| c.len() >= 16).ok_or_else(|| errorf!("invalid backup ciphertext"))?;
    let aead = cipher_for(password, &salt)?;
    let mut plaintext = aead.decrypt(Nonce::from_slice(&nonce), AeadPayload { msg: &ciphertext, aad: AAD }).map_err(|_| errorf!("backup authentication failed"))?;
    let parsed: std::result::Result<Payload, _> = json::unmarshal(&plaintext);
    plaintext.fill(0);
    let mut payload = parsed.map_err(|_| errorf!("invalid backup payload"))?;
    if payload.product != "Minimal Router OS" || payload.version != 2 {
        return Err(errorf!("unsupported backup payload"));
    }
    payload.config.migrate_legacy_fields();
    payload.config.validate()?;
    if let Some(policy) = &payload.dns_filter {
        policy.validate()?;
    }
    Ok(payload)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn v2_round_trip_with_policy() {
        let cfg = crate::config::default_config();
        let mut policy = crate::dnsfilter::default_policy();
        policy.categories.insert("ads".into(), true);
        let sealed = encrypt(&cfg, Some(&policy), "dashboard-password").unwrap();
        let back = decrypt(&sealed, "dashboard-password").unwrap();
        assert_eq!(back.version, 2);
        assert_eq!(back.dns_filter.unwrap().categories.get("ads"), Some(&true));
        assert_eq!(decrypt(&sealed, "other-password!").unwrap_err().to_string(), "backup authentication failed");
    }
}
