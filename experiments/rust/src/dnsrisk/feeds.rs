use rusqlite::{params, Connection, OpenFlags, OptionalExtension};
use std::io::{BufRead, BufReader, Read};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Mutex;

use super::*;

pub(super) const MAX_FEED_BYTES: u64 = 128 << 20;
const MAX_FEED_ROWS: i64 = 4_000_000;
const FEED_INTERVAL: Duration = Duration::hours(24);
const FEED_RETRY: Duration = Duration::hours(6);
const MAX_LINE_BYTES: usize = 4096;

#[derive(Clone, Copy, Debug)]
pub(super) struct Source {
    pub id: &'static str,
    pub label: &'static str,
    pub list: &'static str,
    pub severity: &'static str,
}

pub(super) const SOURCES: [Source; 5] = [
    Source { id: "adult", label: "Adult", list: "porn", severity: "warning" },
    Source { id: "phishing", label: "Phishing", list: "phishing", severity: "high" },
    Source { id: "malware", label: "Malware", list: "malware", severity: "high" },
    Source { id: "fraud", label: "Fraud", list: "fraud", severity: "high" },
    Source { id: "gambling", label: "Gambling", list: "gambling", severity: "warning" },
];

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct FeedStatus {
    pub category: String,
    pub label: String,
    pub source: String,
    pub url: String,
    pub entries: i64,
    pub updated_at: i64,
    pub attempted_at: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
    pub stale: bool,
    pub updating: bool,
}

pub(super) struct Feed {
    pub source: Source,
    pub db: Option<Mutex<Connection>>,
    pub slot: i64,
    pub etag: String,
    pub status: FeedStatus,
}

pub(super) fn source_url(src: &Source) -> String {
    format!("https://blocklistproject.github.io/Lists/alt-version/{}-nl.txt", src.list)
}

pub(super) fn normalize_domain(raw: &str) -> Option<String> {
    let name = raw.trim();
    let name = name.strip_suffix('.').unwrap_or(name).to_lowercase();
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
        if !label.chars().all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-') {
            return None;
        }
    }
    let suffix = crate::dnsfilter::public_suffix(&name);
    if suffix == name || name.ends_with(".local") || name.ends_with(".lan") || name.ends_with(".arpa") {
        return None;
    }
    Some(name)
}

/// golang.org/x/net/publicsuffix.EffectiveTLDPlusOne.
fn effective_tld_plus_one(domain: &str) -> Option<String> {
    if domain.starts_with('.') || domain.ends_with('.') || domain.contains("..") {
        return None;
    }
    let suffix = crate::dnsfilter::public_suffix(domain);
    if domain.len() <= suffix.len() {
        return None;
    }
    let i = domain.len() - suffix.len() - 1;
    if domain.as_bytes()[i] != b'.' {
        return None;
    }
    Some(domain[domain[..i].rfind('.').map(|p| p + 1).unwrap_or(0)..].to_string())
}

/// Stops at the registrable domain, including private suffixes (github.io),
/// so a malicious tenant cannot taint all tenants of a shared platform.
pub(super) fn candidates(name: &str) -> Vec<String> {
    let Some(mut name) = normalize_domain(name) else { return Vec::new() };
    let Some(base) = effective_tld_plus_one(&name) else { return Vec::new() };
    let mut out = vec![name.clone()];
    while name != base {
        name = name.split_once('.').map(|(_, rest)| rest.to_string()).unwrap_or_default();
        out.push(name.clone());
    }
    out
}

pub(super) fn feed_path(dir: &str, id: &str, slot: i64) -> String {
    std::path::Path::new(dir).join(format!("{}-{}.db", id, slot)).to_string_lossy().into_owned()
}

pub(super) fn open_feed(path: &str) -> Result<Connection> {
    std::fs::metadata(path)?;
    let db = Connection::open_with_flags(path, OpenFlags::SQLITE_OPEN_READ_ONLY | OpenFlags::SQLITE_OPEN_NO_MUTEX)?;
    db.execute_batch("PRAGMA trusted_schema=OFF; PRAGMA cache_size=-512;")?;
    // Metadata already holds the validated count; a bounded probe avoids
    // reading every index page on each router boot.
    let probe: Option<i64> = db.query_row("SELECT 1 FROM domains LIMIT 1", [], |r| r.get(0)).optional().ok().flatten();
    if probe != Some(1) {
        return Err(errorf!("category index unavailable"));
    }
    Ok(db)
}

/// Builds a new disk index while the previous one stays available. A
/// partial, HTML or empty response is rejected.
pub(super) fn build_feed(stop: &AtomicBool, path: &str, body: impl Read, allow_write: &dyn Fn() -> bool) -> Result<i64> {
    match std::fs::remove_file(path) {
        Err(err) if err.kind() != std::io::ErrorKind::NotFound => return Err(err.into()),
        _ => {}
    }
    let mut db = Connection::open(path)?;
    db.execute_batch("PRAGMA page_size=4096; PRAGMA max_page_count=65536; PRAGMA journal_mode=OFF; PRAGMA synchronous=OFF; PRAGMA cache_size=-2048; PRAGMA trusted_schema=OFF;")?;
    db.execute("CREATE TABLE domains(domain TEXT PRIMARY KEY) WITHOUT ROWID", [])?;
    let count = {
        let tx = db.transaction()?;
        let count = {
            let mut insert = tx.prepare("INSERT OR IGNORE INTO domains(domain) VALUES (?)")?;
            let mut limited = BufReader::new(body.take(MAX_FEED_BYTES + 1));
            let (mut consumed, mut total, mut bad, mut count) = (0u64, 0i64, 0i64, 0i64);
            let mut line = Vec::new();
            loop {
                line.clear();
                let n = limited.read_until(b'\n', &mut line)?;
                if n == 0 {
                    break;
                }
                consumed += n as u64;
                if line.last() == Some(&b'\n') {
                    line.pop();
                } else if consumed > MAX_FEED_BYTES {
                    // The final fragment was cut by the download limit.
                    break;
                }
                if line.len() >= MAX_LINE_BYTES {
                    return Err(errorf!("bufio.Scanner: token too long"));
                }
                if line.last() == Some(&b'\r') {
                    line.pop();
                }
                let text = String::from_utf8_lossy(&line);
                let text = text.strip_prefix('\u{feff}').unwrap_or(&text).trim();
                if text.is_empty() || text.starts_with('#') {
                    continue;
                }
                total += 1;
                if total % 4096 == 0 && (!allow_write() || stop.load(Ordering::SeqCst)) {
                    return Err(errorf!("category update paused by storage pressure"));
                }
                if text.contains(['<', '>', '\0']) {
                    return Err(errorf!("category download is not a domain list"));
                }
                let Some(name) = normalize_domain(text) else {
                    bad += 1;
                    continue;
                };
                count += insert.execute(params![name])? as i64;
                if count > MAX_FEED_ROWS {
                    return Err(errorf!("category list exceeds entry limit"));
                }
            }
            if consumed > MAX_FEED_BYTES {
                return Err(errorf!("category list exceeds download limit"));
            }
            if count == 0 || bad * 100 > total {
                return Err(errorf!("empty or invalid category list ({} rejected of {} entries)", bad, total));
            }
            count
        };
        tx.commit()?;
        count
    };
    // The inactive file is durable before its slot is published in metadata.
    db.close().map_err(|(_, e)| e)?;
    std::fs::OpenOptions::new().read(true).write(true).open(path)?.sync_all()?;
    Ok(count)
}

impl Service {
    fn refresh_feed(&self, stop: &AtomicBool, index: usize, now: Time) -> Result<()> {
        let (source, slot, etag, loaded, entries) = {
            let feeds = self.feeds();
            let f = &feeds[index];
            (f.source, 1 - f.slot, f.etag.clone(), f.db.is_some(), f.status.entries)
        };
        if !(self.allow_write)() {
            return Err(errorf!("category update paused by storage pressure"));
        }
        let mut url = source_url(&source);
        let mut response = None;
        // URLs are fixed by the program; at most three https redirects within
        // the list host.
        for _ in 0..4 {
            let mut request = self.client.get(&url).set("User-Agent", "MinimalRouter-DNS-Risk/0.2");
            if loaded && !etag.is_empty() {
                request = request.set("If-None-Match", &etag);
            }
            match request.call() {
                Ok(r) if (300..400).contains(&r.status()) && r.status() != 304 => {
                    let next = r.header("location").unwrap_or("").to_string();
                    if !next.starts_with("https://blocklistproject.github.io/") {
                        return Err(errorf!("category download failed; previous list retained"));
                    }
                    url = next;
                }
                Ok(r) => {
                    response = Some(r);
                    break;
                }
                Err(ureq::Error::Status(_, r)) => {
                    response = Some(r);
                    break;
                }
                Err(_) => return Err(errorf!("category download failed; previous list retained")),
            }
        }
        let response = response.ok_or_else(|| errorf!("category download failed; previous list retained"))?;
        if response.status() == 304 && loaded {
            let mut feeds = self.feeds_mut();
            self.lock_db().execute("UPDATE feeds SET updated_at=?, error='' WHERE category=?", params![now.unix(), source.id])?;
            feeds[index].status.updated_at = now.unix();
            feeds[index].status.error.clear();
            return Ok(());
        }
        if response.status() != 200 {
            return Err(errorf!("category download returned HTTP {}; previous list retained", response.status()));
        }
        if response.header("content-length").and_then(|v| v.trim().parse::<u64>().ok()).is_some_and(|n| n > MAX_FEED_BYTES) {
            return Err(errorf!("category download exceeds size limit"));
        }
        let path = feed_path(&self.dir, source.id, slot);
        let next_etag = response.header("etag").filter(|e| e.len() <= 256).unwrap_or("").to_string();
        // Only the inactive, fixed-name slot is disposable; the active index
        // is never removed when validation or storage checks fail.
        let published = (|| -> Result<()> {
            let count = build_feed(stop, &path, response.into_reader(), &*self.allow_write).map_err(|e| errorf!("category index rejected: {}", e))?;
            // Refuse catastrophic upstream truncation.
            if entries > 100 && count < entries / 2 {
                return Err(errorf!("category list unexpectedly shrank; previous list retained"));
            }
            if !(self.allow_write)() {
                return Err(errorf!("category update paused by storage pressure"));
            }
            let next = open_feed(&path)?;
            let mut feeds = self.feeds_mut();
            self.lock_db().execute(
                "INSERT INTO feeds(category,slot,entries,updated_at,attempted_at,etag,error) VALUES(?,?,?,?,?,?,'')
                 ON CONFLICT(category) DO UPDATE SET slot=excluded.slot,entries=excluded.entries,updated_at=excluded.updated_at,attempted_at=excluded.attempted_at,etag=excluded.etag,error=''",
                params![source.id, slot, count, now.unix(), now.unix(), next_etag],
            )?;
            let feed = &mut feeds[index];
            feed.db = Some(Mutex::new(next));
            feed.slot = slot;
            feed.etag = next_etag.clone();
            feed.status.entries = count;
            feed.status.updated_at = now.unix();
            feed.status.error.clear();
            Ok(())
        })();
        if published.is_err() {
            let _ = std::fs::remove_file(&path);
        }
        published
    }

    pub(super) fn update_feeds(&self, stop: &AtomicBool, force: bool) {
        for i in 0..SOURCES.len() {
            let enabled = matches!((self.settings)(), Ok((true, _)));
            if !enabled || stop.load(Ordering::SeqCst) {
                return;
            }
            let now = Time::now();
            {
                let mut feeds = self.feeds_mut();
                let status = &feeds[i].status;
                let due = status.updated_at == 0 || now.sub(&Time::from_unix(status.updated_at, 0)) >= FEED_INTERVAL;
                let retry = status.attempted_at == 0 || now.sub(&Time::from_unix(status.attempted_at, 0)) >= FEED_RETRY;
                if !force && (!due || !retry) {
                    continue;
                }
                feeds[i].status.updating = true;
                feeds[i].status.attempted_at = now.unix();
            }
            let result = self.refresh_feed(stop, i, now);
            let (message, category) = {
                let mut feeds = self.feeds_mut();
                feeds[i].status.updating = false;
                if let Err(err) = result {
                    feeds[i].status.error = format!("Category update failed; previous list retained. {}", err);
                }
                (feeds[i].status.error.clone(), feeds[i].source.id)
            };
            // Persist attempts even for sources that never loaded successfully.
            if (self.allow_write)() {
                let _ = self.lock_db().execute(
                    "INSERT INTO feeds(category,slot,entries,updated_at,attempted_at,etag,error) VALUES(?,0,0,0,?,'',?)
                     ON CONFLICT(category) DO UPDATE SET attempted_at=excluded.attempted_at,error=excluded.error",
                    params![category, now.unix(), message],
                );
            }
        }
    }
}
