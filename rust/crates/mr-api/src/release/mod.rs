//! Fetches, selects and prepares published Minimal Router releases for the
//! privileged updater. Nothing here establishes trust: GitHub metadata only
//! proves that an asset exists; the pinned Ed25519 key and the root updater
//! decide what may run.

mod checker;
mod prepare;

pub use checker::*;
pub use prepare::{prepare_local_release, prepare_published};

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::HashMap;
use std::io::Read;
use std::time::Duration as StdDuration;

use crate::errorf;
use crate::firmware;
use crate::goish::errors::{Result, wrapf};
use crate::goish::time::{Duration, Time};

const DEFAULT_CATALOG_URL: &str = "https://api.github.com/repos/vladimirperovic/minimalrouter/releases?per_page=30";
pub(crate) const USER_AGENT: &str = "minimalrouter-update";
/// Bounds one discovery round so a hostile endpoint cannot cause an
/// unbounded fetch on an appliance.
const MAX_CATALOG_PAGES: usize = 3;
const MAX_CATALOG_BODY: u64 = 1 << 20;
const CATALOG_TIMEOUT: StdDuration = StdDuration::from_secs(20);

/// Which published releases an appliance is willing to see.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Channel {
    #[default]
    Stable,
    Beta,
}

impl Channel {
    pub fn as_str(&self) -> &'static str {
        match self {
            Channel::Stable => "stable",
            Channel::Beta => "beta",
        }
    }

    /// Beta is a superset: an appliance following pre-releases still takes
    /// stable ones.
    pub fn includes(&self, published: &Release) -> bool {
        *self == Channel::Beta || !published.prerelease
    }
}

pub fn parse_channel(value: &str) -> Option<Channel> {
    match value.trim().to_lowercase().as_str() {
        "stable" => Some(Channel::Stable),
        "beta" => Some(Channel::Beta),
        _ => None,
    }
}

/// Candidate metadata plus the canonical asset URLs this package constructs
/// itself. A URL supplied by the remote metadata is never followed.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Release {
    pub version: String,
    pub tag: String,
    pub prerelease: bool,
    pub published_at: Time,
    pub url: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub notes: String,
    #[serde(skip)]
    pub(crate) assets: HashMap<String, String>,
}

pub(crate) fn payload_asset_names(arch: &str) -> (String, String) {
    (format!("minimalrouter-linux-{}.tar.gz", arch), format!("minimalrouter-linux-{}.manifest.json", arch))
}

impl Release {
    /// Whether the release carries an installable payload for `arch`; the
    /// newest tag is not a candidate while its archive or manifest is missing.
    pub fn has_payload(&self, arch: &str) -> bool {
        let (archive, manifest) = payload_asset_names(arch);
        self.assets.get(&archive).is_some_and(|v| !v.is_empty()) && self.assets.get(&manifest).is_some_and(|v| !v.is_empty())
    }

    /// A stable candidate identifier the server re-derives before installing.
    pub fn id(&self) -> String {
        let input = [self.tag.as_str(), self.version.as_str(), &self.published_at.utc().rfc3339()].join("\0");
        hex::encode(Sha256::digest(input.as_bytes()))[..32].to_string()
    }
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct GithubAsset {
    name: String,
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct GithubRelease {
    tag_name: String,
    body: String,
    draft: bool,
    prerelease: bool,
    published_at: String,
    assets: Vec<GithubAsset>,
}

/// Reads the published release list. Only the endpoint is injectable.
#[derive(Clone, Default)]
pub struct Catalog {
    pub api_url: String,
    pub agent: Option<ureq::Agent>,
}

#[derive(Clone, Debug, Default)]
pub struct FetchResult {
    pub releases: Vec<Release>,
    pub etag: String,
    pub not_modified: bool,
    pub retry_after: Duration,
}

crate::sentinel!(ErrRateLimited, "release service rate limit reached");

/// RFC 8288 pagination: only a link the service returned for rel="next".
fn next_page_url(link_header: &str) -> String {
    for part in link_header.split(',') {
        let segments: Vec<&str> = part.trim().split(';').collect();
        if segments.len() < 2 {
            continue;
        }
        let target = segments[0].trim();
        if !target.starts_with('<') || !target.ends_with('>') {
            continue;
        }
        if segments[1..].iter().any(|a| a.trim().eq_ignore_ascii_case(r#"rel="next""#)) {
            return target[1..target.len() - 1].to_string();
        }
    }
    String::new()
}

/// Honours Retry-After (seconds or HTTP date) and the documented rate-limit
/// reset, so a throttled appliance waits the interval it was told to.
fn retry_after(header: &dyn Fn(&str) -> Option<String>, now: Time) -> Duration {
    if let Some(raw) = header("Retry-After").map(|v| v.trim().to_string()).filter(|v| !v.is_empty()) {
        if let Some(seconds) = crate::config::go_atoi(&raw).filter(|s| *s >= 0) {
            return Duration::secs(seconds);
        }
        if let Some(when) = parse_http_time(&raw) {
            let wait = when.sub(&now);
            return if wait.0 > 0 { wait } else { Duration(0) };
        }
    }
    if header("X-RateLimit-Remaining").is_some_and(|v| v.trim() == "0") {
        if let Some(epoch) = header("X-RateLimit-Reset").and_then(|v| v.trim().parse::<i64>().ok()) {
            let wait = Time::from_unix(epoch, 0).sub(&now);
            if wait.0 > 0 {
                return wait;
            }
        }
    }
    Duration(0)
}

/// net/http.ParseTime: RFC 1123 (GMT), RFC 850 and ANSI C asctime.
fn parse_http_time(raw: &str) -> Option<Time> {
    use chrono::{NaiveDateTime, TimeZone, Utc};
    for layout in ["%a, %d %b %Y %H:%M:%S GMT", "%A, %d-%b-%y %H:%M:%S GMT", "%a %b %e %H:%M:%S %Y"] {
        if let Ok(naive) = NaiveDateTime::parse_from_str(raw, layout) {
            return Some(Time(Utc.from_utc_datetime(&naive).fixed_offset()));
        }
    }
    None
}

struct Page {
    status: u16,
    headers: HashMap<String, String>,
    items: Vec<GithubRelease>,
}

impl Catalog {
    fn endpoint(&self) -> String {
        if self.api_url.trim().is_empty() { DEFAULT_CATALOG_URL.into() } else { self.api_url.clone() }
    }

    fn agent(&self) -> ureq::Agent {
        self.agent.clone().unwrap_or_else(|| ureq::AgentBuilder::new().timeout(CATALOG_TIMEOUT).build())
    }

    fn fetch_page(&self, endpoint: &str, etag: &str) -> (Option<Page>, Result<()>) {
        let mut request =
            self.agent().get(endpoint).set("Accept", "application/vnd.github+json").set("X-GitHub-Api-Version", "2022-11-28").set("User-Agent", USER_AGENT);
        if !etag.is_empty() {
            // Reduces transfer; it is not a guarantee about quota.
            request = request.set("If-None-Match", etag);
        }
        let response = match request.call() {
            Ok(response) => response,
            Err(ureq::Error::Status(_, response)) => response,
            Err(err) => return (None, Err(errorf!("{}", err))),
        };
        let headers =
            response.headers_names().into_iter().filter_map(|name| response.header(&name).map(|v| (name.to_ascii_lowercase(), v.to_string()))).collect();
        let mut page = Page { status: response.status(), headers, items: Vec::new() };
        match page.status {
            304 => return (Some(page), Ok(())),
            403 | 429 => {
                let status = page.status;
                return (Some(page), Err(crate::goish::errors::wrap(ErrRateLimited, format!("{} (HTTP {})", ErrRateLimited, status))));
            }
            200 => {}
            status => return (Some(page), Err(errorf!("release service returned HTTP {}", status))),
        }
        let mut body = Vec::new();
        if let Err(err) = response.into_reader().take(MAX_CATALOG_BODY).read_to_end(&mut body) {
            return (Some(page), Err(errorf!("decode release metadata: {}", err)));
        }
        // json.Decoder reads only the first value of the bounded stream.
        let first = serde_json::Deserializer::from_slice(&body).into_iter::<serde_json::Value>().next();
        match first
            .map(|v| v.map_err(|e| errorf!("{}", e)).and_then(|v| crate::goish::json::from_value::<Vec<GithubRelease>>(&v).map_err(|e| errorf!("{}", e))))
        {
            Some(Ok(items)) => page.items = items,
            Some(Err(err)) => return (Some(page), Err(wrapf("decode release metadata", err))),
            None => return (Some(page), Err(errorf!("decode release metadata: EOF"))),
        }
        (Some(page), Ok(()))
    }

    /// Reads the published releases, following pagination up to a bounded
    /// number of pages. A matching etag short-circuits the whole round.
    pub fn fetch(&self, etag: &str) -> (FetchResult, Result<()>) {
        let mut endpoint = self.endpoint();
        let mut result = FetchResult::default();
        let now = Time::now();
        for page_index in 0..MAX_CATALOG_PAGES {
            if endpoint.is_empty() {
                break;
            }
            let conditional = if page_index == 0 { etag } else { "" };
            let (page, outcome) = self.fetch_page(&endpoint, conditional);
            if let Some(page) = &page {
                result.retry_after = retry_after(&|name| page.headers.get(&name.to_ascii_lowercase()).cloned(), now);
            }
            if let Err(err) = outcome {
                return (result, Err(err));
            }
            let page = page.expect("page on success");
            if page.status == 304 {
                result.not_modified = true;
                result.etag = etag.to_string();
                return (result, Ok(()));
            }
            if page_index == 0 {
                result.etag = page.headers.get("etag").map(|v| v.trim().to_string()).unwrap_or_default();
            }
            result.releases.extend(page.items.into_iter().filter_map(convert_release));
            let mut next = next_page_url(page.headers.get("link").map(String::as_str).unwrap_or(""));
            // Pagination may only continue on the release API itself.
            if !next.is_empty() && !same_origin(&self.endpoint(), &next) {
                next.clear();
            }
            endpoint = next;
        }
        (result, Ok(()))
    }
}

fn origin(url: &str) -> Option<(String, String)> {
    let (scheme, rest) = url.split_once("://")?;
    let host = rest.split(['/', '?', '#']).next().unwrap_or("");
    let host = host.rsplit('@').next().unwrap_or(host);
    Some((scheme.to_ascii_lowercase(), host.to_ascii_lowercase()))
}

fn same_origin(base: &str, candidate: &str) -> bool {
    matches!((origin(base), origin(candidate)), (Some(a), Some(b)) if a == b)
}

fn convert_release(item: GithubRelease) -> Option<Release> {
    let tag = item.tag_name.trim().to_string();
    if item.draft || !firmware::is_release_version(&tag) {
        return None;
    }
    // Metadata only proves an asset exists; the URL is always canonical.
    let assets = item.assets.iter().filter(|a| !a.name.is_empty()).map(|a| (a.name.clone(), canonical_release_asset_url(&tag, &a.name))).collect();
    Some(Release {
        version: tag.strip_prefix('v').unwrap_or(&tag).to_string(),
        published_at: Time::parse_rfc3339(&item.published_at).unwrap_or_default(),
        url: canonical_release_html_url(&tag),
        notes: summarize_notes(&item.body),
        prerelease: item.prerelease,
        tag,
        assets,
    })
}

fn canonical_release_html_url(tag: &str) -> String {
    format!("https://github.com/vladimirperovic/minimalrouter/releases/tag/{}", tag)
}

pub(crate) fn canonical_release_asset_url(tag: &str, name: &str) -> String {
    format!("{}{}/{}", prepare::RELEASE_DOWNLOAD_BASE_URL, path_escape(tag), path_escape(name))
}

/// net/url.PathEscape: a single path segment.
fn path_escape(segment: &str) -> String {
    let mut out = String::new();
    for b in segment.bytes() {
        if b.is_ascii_alphanumeric() || b"-_.~$&+,;=:@".contains(&b) {
            out.push(b as char);
        } else {
            out.push_str(&format!("%{:02X}", b));
        }
    }
    out
}

/// Bounded plain text; author-controlled notes never reach the browser as
/// markup.
fn summarize_notes(body: &str) -> String {
    const MAX_NOTES: usize = 2000;
    let cleaned = body.trim().replace("\r\n", "\n");
    if cleaned.len() <= MAX_NOTES {
        return cleaned;
    }
    // Go slices bytes; each byte of a split character becomes U+FFFD once
    // the string is JSON encoded.
    let mut truncated = String::new();
    for chunk in cleaned.as_bytes()[..MAX_NOTES].utf8_chunks() {
        truncated.push_str(chunk.valid());
        truncated.extend(std::iter::repeat_n('\u{fffd}', chunk.invalid().len()));
    }
    format!("{}\n…", truncated.trim())
}

/// The newest release in the channel, and the newest one actually
/// installable for this architecture.
pub fn select_newest(releases: &[Release], channel: Channel, arch: &str) -> (Option<Release>, Option<Release>) {
    let (mut newest, mut candidate): (Option<&Release>, Option<&Release>) = (None, None);
    for item in releases.iter().filter(|r| channel.includes(r)) {
        if newest.is_none_or(|n| is_newer(&item.version, &n.version)) {
            newest = Some(item);
        }
        if item.has_payload(arch) && candidate.is_none_or(|c| is_newer(&item.version, &c.version)) {
            candidate = Some(item);
        }
    }
    (newest.cloned(), candidate.cloned())
}

pub fn is_newer_version(candidate: &str, current: &str) -> bool {
    is_newer(candidate, current)
}

fn is_newer(candidate: &str, current: &str) -> bool {
    firmware::is_release_version(candidate)
        && firmware::is_release_version(current)
        && firmware::compare_release_versions(candidate, current).is_ok_and(|c| c > 0)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn converts_and_selects_releases() {
        let page: Vec<GithubRelease> = crate::goish::json::unmarshal(
            br#"[{"tag_name":"v0.4.0-beta.1","prerelease":true,"published_at":"2026-10-01T10:00:00Z","assets":[{"name":"minimalrouter-linux-amd64.tar.gz"},{"name":"minimalrouter-linux-amd64.manifest.json"}]},
                 {"tag_name":"v0.3.2","published_at":"2026-09-01T10:00:00Z","assets":[{"name":"minimalrouter-linux-amd64.tar.gz"}]},
                 {"tag_name":"v0.3.1","published_at":"2026-08-01T10:00:00Z","body":"Fixes\r\n","assets":[{"name":"minimalrouter-linux-amd64.tar.gz"},{"name":"minimalrouter-linux-amd64.manifest.json"}]},
                 {"tag_name":"nightly","assets":[]},{"tag_name":"v9.9.9","draft":true}]"#,
        )
        .unwrap();
        let releases: Vec<Release> = page.into_iter().filter_map(convert_release).collect();
        assert_eq!(releases.len(), 3);
        assert_eq!(releases[2].notes, "Fixes");
        assert_eq!(
            releases[2].assets["minimalrouter-linux-amd64.tar.gz"],
            "https://github.com/vladimirperovic/minimalrouter/releases/download/v0.3.1/minimalrouter-linux-amd64.tar.gz"
        );
        let (newest, candidate) = select_newest(&releases, Channel::Stable, "amd64");
        assert_eq!((newest.unwrap().version, candidate.unwrap().version), ("0.3.2".to_string(), "0.3.1".to_string()));
        let (newest, candidate) = select_newest(&releases, Channel::Beta, "amd64");
        assert_eq!((newest.unwrap().version, candidate.clone().unwrap().version), ("0.4.0-beta.1".to_string(), "0.4.0-beta.1".to_string()));
        assert_eq!(candidate.unwrap().id().len(), 32);
    }

    #[test]
    fn pagination_and_retry_headers() {
        let link =
            r#"<https://api.github.com/repositories/1/releases?page=2>; rel="next", <https://api.github.com/repositories/1/releases?page=5>; rel="last""#;
        assert_eq!(next_page_url(link), "https://api.github.com/repositories/1/releases?page=2");
        assert!(same_origin(DEFAULT_CATALOG_URL, "https://API.github.com/x") && !same_origin(DEFAULT_CATALOG_URL, "https://evil.example/x"));
        let now = Time::from_unix(1_800_000_000, 0);
        let headers = |name: &str| match name {
            "X-RateLimit-Remaining" => Some("0".to_string()),
            "X-RateLimit-Reset" => Some("1800000120".to_string()),
            _ => None,
        };
        assert_eq!(retry_after(&headers, now), Duration::secs(120));
        assert_eq!(retry_after(&|n: &str| (n == "Retry-After").then(|| "30".to_string()), now), Duration::secs(30));
        assert_eq!(path_escape("v0.3.0+build/x"), "v0.3.0+build%2Fx");
    }
}
