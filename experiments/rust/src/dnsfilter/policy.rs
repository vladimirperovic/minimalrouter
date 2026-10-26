use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, HashSet};

use crate::errorf;
use crate::goish::errors::Result;

pub const SOCKET_PATH: &str = "/run/minimalrouter/dns-filter.sock";
pub const DROP_IN_PATH: &str = "/etc/dnsmasq.d/minimalrouter-dns-filter.conf";
pub const MAX_DOMAINS: usize = 800_000;
pub const MAX_FEED_DOMAINS: usize = 300_000;
pub const MAX_FEED_BYTES: usize = 24 << 20;
pub const MAX_STREAM_BYTES: usize = 48 << 20;
pub const MAX_METADATA_BYTES: usize = 128 << 10;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Source {
    pub id: &'static str,
    pub label: &'static str,
    pub file: &'static str,
    /// Names added to every downloaded copy, for endpoints the category
    /// depends on that the upstream list misses.
    pub extra: &'static [&'static str],
}

/// Names the bypass category depends on but upstream misses or may drop.
/// dnsmasq matches only the queried name, so every alias a device may query
/// directly on the way to a blocked target is listed too (iCloud Private
/// Relay ingress and its CNAME targets, and Firefox's DoH canary).
const BYPASS_EXTRA: &[&str] = &[
    "mask.icloud.com",
    "mask-h2.icloud.com",
    "mask.apple-dns.net",
    "mask-canary.icloud.com",
    "mask-canary.v.aaplimg.com",
    "use-application-dns.net",
];

pub fn sources() -> [Source; 5] {
    [
        Source { id: "threats", label: "Malware, phishing & scams", file: "tif.mini", extra: &[] },
        Source { id: "ads", label: "Ads & trackers", file: "light", extra: &[] },
        Source { id: "adult", label: "Adult content", file: "nsfw", extra: &[] },
        Source { id: "gambling", label: "Gambling", file: "gambling.mini", extra: &[] },
        // Encrypted DNS, relay ingress, VPN, Tor and proxy endpoints.
        Source { id: "bypass", label: "Encrypted DNS, VPN & proxy bypass", file: "doh-vpn-proxy-bypass", extra: BYPASS_EXTRA },
    ]
}

impl Source {
    pub fn url(&self) -> String {
        format!("https://cdn.jsdelivr.net/gh/hagezi/dns-blocklists@latest/wildcard/{}-onlydomains.txt", self.file)
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Exception {
    pub domain: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub reason: String,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Policy {
    pub revision: u64,
    pub categories: BTreeMap<String, bool>,
    pub exceptions: Vec<Exception>,
}

pub fn default_policy() -> Policy {
    Policy::default()
}

/// A public domain only: no IP, URL, wildcard, local suffix or public suffix.
pub fn domain(raw: &str) -> Option<String> {
    let trimmed = raw.trim();
    let name = crate::goish::strings::to_lower(trimmed.strip_suffix('.').unwrap_or(trimmed));
    if name.len() > 253 || !name.contains('.') {
        return None;
    }
    if name.parse::<std::net::IpAddr>().is_ok() {
        return None;
    }
    for label in name.split('.') {
        if label.is_empty() || label.len() > 63 || label.starts_with('-') || label.ends_with('-') {
            return None;
        }
        if !label.bytes().all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'-') {
            return None;
        }
    }
    let suffix = public_suffix(&name);
    if suffix == name || name.ends_with(".local") || name.ends_with(".lan") || name.ends_with(".arpa") {
        return None;
    }
    Some(name)
}

/// The public suffix (ICANN and private sections), with the implicit `*`
/// rule for unknown TLDs, as golang.org/x/net/publicsuffix returns it.
pub fn public_suffix(name: &str) -> String {
    match psl::suffix_str(name) {
        Some(suffix) => suffix.to_string(),
        None => name.rsplit('.').next().unwrap_or(name).to_string(),
    }
}

impl Policy {
    pub fn validate(&self) -> Result<()> {
        if self.categories.len() > sources().len() || self.exceptions.len() > 200 {
            return Err(errorf!("DNS filter policy exceeds safety limits"));
        }
        if self.categories.keys().any(|key| !sources().iter().any(|s| s.id == key)) {
            return Err(errorf!("unknown DNS filter category"));
        }
        let mut seen = HashSet::new();
        for e in &self.exceptions {
            let valid = domain(&e.domain).is_some_and(|d| d == e.domain)
                && seen.insert(e.domain.clone())
                && e.reason.len() <= 200
                && !e.reason.contains(['\0', '\r', '\n']);
            if !valid {
                return Err(errorf!("invalid or duplicate blocking exception"));
            }
        }
        Ok(())
    }

    /// Exceptions cover a domain and all its subdomains, matching dnsmasq's
    /// server routing semantics.
    pub fn allowed(&self, domain: &str) -> bool {
        self.exceptions.iter().any(|e| domain == e.domain || domain.ends_with(&format!(".{}", e.domain)))
    }

    pub fn category(&self, id: &str) -> bool {
        self.categories.get(id).copied().unwrap_or(false)
    }
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Applied {
    pub policy: Policy,
    pub domains: i64,
    pub applied_at: i64,
    pub healthy: bool,
    pub sources: BTreeMap<String, String>,
    pub health_checked_at: i64,
    pub health_checking: bool,
    pub activation_pending: bool,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Request {
    pub version: i64,
    pub operation: String,
    pub expected_revision: u64,
    pub policy: Policy,
    pub sources: BTreeMap<String, String>,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Response {
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
    pub state: Applied,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn domain_rules() {
        assert_eq!(domain(" Example.COM. ").as_deref(), Some("example.com"));
        assert_eq!(domain("sub.example.co.uk").as_deref(), Some("sub.example.co.uk"));
        assert!(domain("co.uk").is_none());
        assert!(domain("com").is_none());
        assert!(domain("1.2.3.4").is_none());
        assert!(domain("printer.lan").is_none());
        assert!(domain("-bad.example.com").is_none());
        assert!(domain("https://example.com").is_none());
    }
}
