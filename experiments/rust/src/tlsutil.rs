//! Device certificate generation and loading for the management HTTPS listener.

use std::collections::HashSet;
use std::net::IpAddr;

use crate::config::SystemConfig;
use crate::errorf;
use crate::goish::errors::{wrapf, Result};
use crate::goish::net::{self, IP};
use crate::goish::time::{Duration, Time};

/// Remaining validity below which a certificate is rotated rather than risking
/// a failed handshake later.
pub const RENEW_BEFORE_EXPIRY: Duration = Duration::hours(30 * 24);

pub struct CertManager {
    cert_path: String,
    key_path: String,
}

/// Whether a validity window is usable now. A certificate generated before the
/// clock synchronised may become not-yet-valid after a large correction, so
/// both ends matter; callers caching a certificate must re-check on every use.
pub fn certificate_time_valid(not_before: Time, not_after: Time, now: Time) -> bool {
    !now.before(&not_before) && not_after.sub(&now) >= RENEW_BEFORE_EXPIRY
}

fn normalize_ips(input: &[IP]) -> Vec<IP> {
    let mut seen = HashSet::new();
    let mut out = Vec::new();
    for ip in input {
        if ip.is_empty() {
            continue;
        }
        let canonical = ip.to4().unwrap_or_else(|| ip.clone());
        let key = canonical.to_string();
        if key.is_empty() || !seen.insert(key) {
            continue;
        }
        out.push(canonical);
    }
    out
}

fn normalize_dns_names(input: &[String]) -> Vec<String> {
    let mut seen = HashSet::new();
    let mut out = Vec::new();
    for raw in input {
        let trimmed = raw.trim();
        let name = crate::goish::strings::to_lower(trimmed.strip_suffix('.').unwrap_or(trimmed));
        if name.is_empty() || !seen.insert(name.clone()) {
            continue;
        }
        out.push(name);
    }
    out
}

fn canonical_wireguard_ip(cfg: &SystemConfig) -> Option<IP> {
    let raw = cfg.wireguard.address.trim();
    if raw.is_empty() {
        return None;
    }
    net::parse_cidr(raw).map(|(ip, _)| ip)
}

/// Parsed facts about a certificate needed for SAN checks.
struct CertFacts {
    not_before: Time,
    not_after: Time,
    ips: Vec<IP>,
    dns: Vec<String>,
}

fn parse_cert(cert_pem: &[u8]) -> Option<CertFacts> {
    let block = pem::parse(cert_pem).ok()?;
    if block.tag() != "CERTIFICATE" {
        return None;
    }
    let (_, cert) = x509_parser::parse_x509_certificate(block.contents()).ok()?;
    let validity = cert.validity();
    let mut facts = CertFacts {
        not_before: Time::from_unix(validity.not_before.timestamp(), 0),
        not_after: Time::from_unix(validity.not_after.timestamp(), 0),
        ips: Vec::new(),
        dns: Vec::new(),
    };
    if let Ok(Some(san)) = cert.subject_alternative_name() {
        for name in &san.value.general_names {
            match name {
                x509_parser::extensions::GeneralName::DNSName(d) => facts.dns.push(d.to_ascii_lowercase()),
                x509_parser::extensions::GeneralName::IPAddress(bytes) => facts.ips.push(IP::from_bytes(bytes)),
                _ => {}
            }
        }
    }
    Some(facts)
}

impl CertFacts {
    /// `x509.Certificate.VerifyHostname` for IP literals and exact DNS names.
    fn verify_hostname(&self, host: &str) -> bool {
        if let Some(ip) = net::parse_ip(host) {
            return self.ips.iter().any(|san| san.equal(&ip));
        }
        let host = crate::goish::strings::to_lower(host.strip_suffix('.').unwrap_or(host));
        self.dns.iter().any(|san| {
            if let Some(suffix) = san.strip_prefix("*.") {
                host.split_once('.').is_some_and(|(label, rest)| !label.is_empty() && rest == suffix)
            } else {
                *san == host
            }
        })
    }
}

fn certificate_matches_config(cert_pem: &[u8], cfg: &SystemConfig, additional_ips: &[IP], additional_dns: &[String]) -> bool {
    let Some(cert) = parse_cert(cert_pem) else { return false };
    if !certificate_time_valid(cert.not_before, cert.not_after, Time::now()) {
        return false;
    }
    if net::parse_ip(&cfg.lan.ip_address).is_none() || !cert.verify_hostname(&cfg.lan.ip_address) {
        return false;
    }
    // A valid canonical WG address stays a SAN while the service is disabled,
    // so enabling it later does not rotate the certificate; an empty or invalid
    // disabled address must not force regeneration on every handshake.
    match canonical_wireguard_ip(cfg) {
        Some(wg) => {
            if !cert.verify_hostname(&wg.to_string()) {
                return false;
            }
        }
        None if cfg.wireguard.enabled => return false,
        None => {}
    }
    let fqdn = format!("{}.{}", cfg.system.hostname, cfg.system.domain);
    let fqdn = fqdn.trim();
    let fqdn = fqdn.strip_suffix('.').unwrap_or(fqdn);
    if fqdn.is_empty() || !cert.verify_hostname(fqdn) {
        return false;
    }
    normalize_ips(additional_ips).iter().all(|ip| cert.verify_hostname(&ip.to_string()))
        && normalize_dns_names(additional_dns).iter().all(|name| cert.verify_hostname(name))
}

impl CertManager {
    pub fn new(data_dir: &str) -> CertManager {
        let dir = std::path::Path::new(data_dir);
        CertManager {
            cert_path: dir.join("server.crt").to_string_lossy().into_owned(),
            key_path: dir.join("server.key").to_string_lossy().into_owned(),
        }
    }

    pub fn ensure_certificate(&self, cfg: &SystemConfig) -> Result<(Vec<u8>, Vec<u8>)> {
        self.ensure_certificate_with_additional_sans(cfg, &[], &[])
    }

    /// During commit-confirm, routerd adds candidate LAN/WireGuard addresses
    /// and DNS identity while keeping every canonical identity.
    pub fn ensure_certificate_with_additional_sans(&self, cfg: &SystemConfig, additional_ips: &[IP], additional_dns: &[String]) -> Result<(Vec<u8>, Vec<u8>)> {
        let ips = normalize_ips(additional_ips);
        let dns = normalize_dns_names(additional_dns);
        if let Ok((cert, key)) = self.load_existing() {
            if certificate_matches_config(&cert, cfg, &ips, &dns) {
                return Ok((cert, key));
            }
        }
        self.generate_self_signed(cfg, &ips, &dns)
    }

    fn load_existing(&self) -> Result<(Vec<u8>, Vec<u8>)> {
        let cert = std::fs::read(&self.cert_path)?;
        let key = std::fs::read(&self.key_path)?;
        // Both halves must parse and belong together.
        let key_pair = rcgen::KeyPair::from_pem(&String::from_utf8_lossy(&key)).map_err(|e| errorf!("{}", e))?;
        let block = pem::parse(&cert).map_err(|e| errorf!("{}", e))?;
        let (_, parsed) = x509_parser::parse_x509_certificate(block.contents()).map_err(|e| errorf!("{}", e))?;
        if parsed.public_key().subject_public_key.data.as_ref() != key_pair.public_key_raw() {
            return Err(errorf!("tls: private key does not match public key"));
        }
        Ok((cert, key))
    }

    fn generate_self_signed(&self, cfg: &SystemConfig, additional_ips: &[IP], additional_dns: &[String]) -> Result<(Vec<u8>, Vec<u8>)> {
        let key_pair = rcgen::KeyPair::generate_for(&rcgen::PKCS_ECDSA_P256_SHA256).map_err(|e| wrapf("failed to generate private key", errorf!("{}", e)))?;
        let fqdn = format!("{}.{}", cfg.system.hostname, cfg.system.domain);
        let mut ips: Vec<IP> = vec![IP::v4(127, 0, 0, 1)];
        let mut push_ip = |ip: Option<IP>| {
            if let Some(ip) = ip {
                if !ips.iter().any(|existing| existing.to_string() == ip.to_string()) {
                    ips.push(ip);
                }
            }
        };
        push_ip(net::parse_ip(&cfg.lan.ip_address));
        push_ip(canonical_wireguard_ip(cfg));
        for ip in normalize_ips(additional_ips) {
            push_ip(Some(ip));
        }
        let mut names = vec![fqdn.clone(), "localhost".to_string()];
        for name in normalize_dns_names(additional_dns) {
            if !names.iter().any(|n| n.eq_ignore_ascii_case(&name)) {
                names.push(name);
            }
        }
        let mut params = rcgen::CertificateParams::default();
        let mut subject = rcgen::DistinguishedName::new();
        subject.push(rcgen::DnType::OrganizationName, "Minimal Router OS");
        subject.push(rcgen::DnType::CommonName, fqdn.as_str());
        params.distinguished_name = subject;
        let serial = crate::goish::rand::bytes(16).map_err(|e| errorf!("{}", e))?;
        params.serial_number = Some(rcgen::SerialNumber::from_slice(&serial));
        let now = Time::now_utc();
        params.not_before = to_offset(now.add(Duration::minutes(-5)));
        params.not_after = to_offset(now.add(Duration::hours(365 * 24)));
        params.key_usages = vec![rcgen::KeyUsagePurpose::KeyEncipherment, rcgen::KeyUsagePurpose::DigitalSignature];
        params.extended_key_usages = vec![rcgen::ExtendedKeyUsagePurpose::ServerAuth];
        params.is_ca = rcgen::IsCa::ExplicitNoCa;
        for name in &names {
            if let Ok(dns) = rcgen::Ia5String::try_from(name.as_str()) {
                params.subject_alt_names.push(rcgen::SanType::DnsName(dns));
            }
        }
        for ip in &ips {
            if let Ok(addr) = ip.to_string().parse::<IpAddr>() {
                params.subject_alt_names.push(rcgen::SanType::IpAddress(addr));
            }
        }
        let cert = params.self_signed(&key_pair).map_err(|e| wrapf("failed to create certificate", errorf!("{}", e)))?;
        let cert_pem = cert.pem().into_bytes();
        let key_pem = key_pair.serialize_pem().into_bytes();
        write_atomic(&self.key_path, &key_pem, 0o600).map_err(|e| wrapf("failed to write key", e))?;
        write_atomic(&self.cert_path, &cert_pem, 0o600).map_err(|e| wrapf("failed to write cert", e))?;
        Ok((cert_pem, key_pem))
    }
}

fn to_offset(t: Time) -> ::time::OffsetDateTime {
    ::time::OffsetDateTime::from_unix_timestamp(t.unix()).unwrap_or(::time::OffsetDateTime::UNIX_EPOCH)
}

fn write_atomic(path: &str, data: &[u8], mode: u32) -> Result<()> {
    use std::io::Write;
    let target = std::path::Path::new(path);
    let dir = target.parent().unwrap_or(std::path::Path::new("."));
    let tmp = dir.join(format!(".tls-{}", crate::auth::generate_random_hex(6)?));
    let result = (|| -> std::io::Result<()> {
        let mut file = std::fs::OpenOptions::new().write(true).create_new(true).open(&tmp)?;
        crate::config::set_mode(&tmp.to_string_lossy(), mode)?;
        file.write_all(data)?;
        file.sync_all()?;
        drop(file);
        std::fs::rename(&tmp, target)?;
        #[cfg(unix)]
        std::fs::File::open(dir)?.sync_all()?;
        Ok(())
    })();
    let _ = std::fs::remove_file(&tmp);
    result.map_err(Into::into)
}

/// Lower-case hex SHA-256 of the DER certificate.
pub fn get_certificate_fingerprint(cert_pem: &[u8]) -> Result<String> {
    use sha2::Digest;
    let block = pem::parse(cert_pem).map_err(|_| errorf!("failed to decode certificate PEM"))?;
    Ok(hex::encode(sha2::Sha256::digest(block.contents())))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn generated_certificate_covers_canonical_identities_and_is_reused() {
        let dir = std::env::temp_dir().join(format!("mr-tls-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let manager = CertManager::new(dir.to_str().unwrap());
        let cfg = crate::config::default_config();
        let (cert, _) = manager.ensure_certificate(&cfg).unwrap();
        let facts = parse_cert(&cert).unwrap();
        assert!(facts.verify_hostname("192.168.1.1"));
        assert!(facts.verify_hostname("10.8.0.1"));
        assert!(facts.verify_hostname("minimalrouter.lan"));
        assert!(!facts.verify_hostname("192.168.1.2"));
        let (again, _) = manager.ensure_certificate(&cfg).unwrap();
        assert_eq!(cert, again, "a matching certificate must be reused");
        let extra = [net::parse_ip("192.168.1.2").unwrap()];
        let (rotated, _) = manager.ensure_certificate_with_additional_sans(&cfg, &extra, &[]).unwrap();
        assert_ne!(cert, rotated);
        assert_eq!(get_certificate_fingerprint(&rotated).unwrap().len(), 64);
        std::fs::remove_dir_all(dir).ok();
    }
}
