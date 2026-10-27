//! Downloaded block lists stored as two alternating SQLite slots per category.

use rusqlite::{params, Connection, OpenFlags, OptionalExtension};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::io::{BufRead, Read, Write};

use super::policy::{domain, Source, MAX_FEED_BYTES, MAX_FEED_DOMAINS};
use crate::errorf;
use crate::goish::errors::Result;

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Catalog {
    pub category: String,
    pub label: String,
    pub url: String,
    pub hash: String,
    pub entries: i64,
    pub updated_at: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub error: String,
}

pub(crate) fn catalog_path(dir: &std::path::Path, id: &str, slot: usize) -> std::path::PathBuf {
    dir.join(format!("{}-{}.db", id, slot))
}

fn open_catalog(path: &std::path::Path, readonly: bool) -> Result<Connection> {
    let conn = if readonly {
        Connection::open_with_flags(path, OpenFlags::SQLITE_OPEN_READ_ONLY)?
    } else {
        Connection::open(path)?
    };
    conn.execute_batch("PRAGMA trusted_schema=OFF; PRAGMA cache_size=-512; PRAGMA max_page_count=16384; PRAGMA busy_timeout=1000;")?;
    if !readonly {
        conn.query_row("PRAGMA journal_mode=OFF", [], |_| Ok(()))?;
        conn.execute_batch("PRAGMA synchronous=OFF;")?;
    }
    Ok(conn)
}

fn meta_for(source: &Source) -> Catalog {
    Catalog { category: source.id.into(), label: source.label.into(), url: source.url(), ..Default::default() }
}

pub(crate) fn read_catalog(path: &std::path::Path, source: &Source) -> (Catalog, Result<()>) {
    let mut meta = meta_for(source);
    if !path.exists() {
        return (meta, Err(errorf!("no catalog")));
    }
    let result = (|| {
        let conn = open_catalog(path, true)?;
        let (hash, entries, updated): (String, i64, i64) = conn.query_row("SELECT hash,entries,updated_at FROM metadata", [], |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?)))?;
        Ok::<_, crate::goish::errors::Error>((hash, entries, updated))
    })();
    match result {
        Ok((hash, entries, updated)) => {
            meta.hash = hash;
            meta.entries = entries;
            meta.updated_at = updated;
            if meta.entries < 1 || meta.entries > MAX_FEED_DOMAINS as i64 || meta.hash.len() != 64 {
                return (meta, Err(errorf!("invalid catalog metadata")));
            }
            (meta, Ok(()))
        }
        Err(e) => (meta, Err(e)),
    }
}

pub(crate) fn build_catalog(path: &std::path::Path, source: &Source, body: impl Read, allow_write: Option<&(dyn Fn() -> bool + Send + Sync)>) -> Result<Catalog> {
    let mut meta = meta_for(source);
    match std::fs::remove_file(path) {
        Err(e) if e.kind() != std::io::ErrorKind::NotFound => return Err(e.into()),
        _ => {}
    }
    let mut conn = open_catalog(path, false)?;
    conn.execute_batch("CREATE TABLE domains(domain TEXT PRIMARY KEY) WITHOUT ROWID; CREATE TABLE metadata(hash TEXT, entries INTEGER, updated_at INTEGER)")?;
    let tx = conn.transaction()?;
    {
        let mut insert = tx.prepare("INSERT OR IGNORE INTO domains(domain) VALUES (?)")?;
        let mut limited = body.take(MAX_FEED_BYTES as u64 + 1);
        let mut reader = std::io::BufReader::with_capacity(4096, &mut limited);
        let (mut bad, mut total) = (0usize, 0usize);
        let mut line = Vec::new();
        loop {
            line.clear();
            let n = reader.read_until(b'\n', &mut line)?;
            if n == 0 {
                break;
            }
            // bufio.Scanner with a 4 KiB buffer rejects longer tokens.
            let text = String::from_utf8_lossy(&line);
            let text = text.strip_suffix('\n').unwrap_or(&text);
            let text = text.strip_suffix('\r').unwrap_or(text);
            if text.len() > 4096 {
                return Err(errorf!("bufio.Scanner: token too long"));
            }
            let entry = text.strip_prefix('\u{feff}').unwrap_or(text).trim();
            if entry.is_empty() || entry.starts_with('#') {
                continue;
            }
            total += 1;
            if total > MAX_FEED_DOMAINS * 2 {
                return Err(errorf!("list exceeds row limit"));
            }
            if total % 2048 == 0 && allow_write.is_some_and(|f| !f()) {
                return Err(errorf!("list refresh paused by storage pressure"));
            }
            let Some(d) = domain(entry) else {
                bad += 1;
                continue;
            };
            meta.entries += insert.execute(params![d])? as i64;
            if meta.entries > MAX_FEED_DOMAINS as i64 {
                return Err(errorf!("list exceeds appliance domain limit"));
            }
        }
        drop(reader);
        if limited.limit() == 0 {
            return Err(errorf!("list exceeds download limit"));
        }
        if meta.entries < 1 || bad * 100 > total {
            return Err(errorf!("empty or invalid domain list"));
        }
        // Extra names are added only to a download that is already valid.
        for extra in source.extra {
            let d = domain(extra).ok_or_else(|| errorf!("invalid built-in domain"))?;
            meta.entries += insert.execute(params![d])? as i64;
        }
        if meta.entries > MAX_FEED_DOMAINS as i64 {
            return Err(errorf!("list exceeds appliance domain limit"));
        }
        let mut hash = Sha256::new();
        let mut stmt = tx.prepare("SELECT domain FROM domains ORDER BY domain")?;
        let mut rows = stmt.query([])?;
        while let Some(row) = rows.next()? {
            let d: String = row.get(0)?;
            hash.update(d.as_bytes());
            hash.update(b"\n");
        }
        meta.hash = hex::encode(hash.finalize());
    }
    meta.updated_at = crate::goish::time::Time::now().unix();
    tx.execute("INSERT INTO metadata VALUES (?,?,?)", params![meta.hash, meta.entries, meta.updated_at])?;
    tx.commit()?;
    drop(conn);
    let file = std::fs::OpenOptions::new().read(true).write(true).open(path)?;
    crate::config::set_mode(&path.to_string_lossy(), 0o600)?;
    file.sync_all()?;
    Ok(meta)
}

pub(crate) fn download_catalog(path: &std::path::Path, source: &Source, previous: &Catalog, allow_write: Option<&(dyn Fn() -> bool + Send + Sync)>) -> Result<Catalog> {
    let agent = ureq::AgentBuilder::new().timeout(std::time::Duration::from_secs(180)).redirects(0).build();
    let mut url = source.url();
    let mut response = None;
    // Follow at most three redirects, and only within cdn.jsdelivr.net.
    for _ in 0..4 {
        match agent.get(&url).call() {
            Ok(r) if (300..400).contains(&r.status()) => {
                let next = r.header("location").unwrap_or("").to_string();
                if !next.starts_with("https://cdn.jsdelivr.net/") {
                    return Err(errorf!("list redirect refused"));
                }
                url = next;
            }
            Ok(r) => {
                response = Some(r);
                break;
            }
            Err(ureq::Error::Status(code, _)) => return Err(errorf!("list download returned HTTP {}", code)),
            Err(e) => return Err(errorf!("{}", e)),
        }
    }
    let response = response.ok_or_else(|| errorf!("list redirect refused"))?;
    if response.status() != 200 {
        return Err(errorf!("list download returned HTTP {}", response.status()));
    }
    let result = build_catalog(path, source, response.into_reader(), allow_write).and_then(|meta| {
        if previous.entries > 0 && meta.entries < previous.entries / 2 {
            return Err(errorf!("list shrank by more than half; previous list retained"));
        }
        Ok(meta)
    });
    if result.is_err() {
        let _ = std::fs::remove_file(path);
    }
    result
}

pub(crate) fn emit_catalog(path: &std::path::Path, w: &mut dyn Write) -> Result<()> {
    let conn = open_catalog(path, true)?;
    let mut stmt = conn.prepare("SELECT domain FROM domains ORDER BY domain")?;
    let mut rows = stmt.query([])?;
    while let Some(row) = rows.next()? {
        let d: String = row.get(0)?;
        w.write_all(d.as_bytes())?;
        w.write_all(b"\n")?;
    }
    Ok(())
}

pub(crate) fn catalog_match(path: &std::path::Path, name: &str) -> Result<String> {
    let conn = open_catalog(path, true)?;
    let mut candidate = name;
    while candidate.contains('.') {
        let found: Option<String> = conn.query_row("SELECT domain FROM domains WHERE domain=?", params![candidate], |r| r.get(0)).optional()?;
        if let Some(found) = found {
            return Ok(found);
        }
        candidate = &candidate[candidate.find('.').unwrap() + 1..];
    }
    Ok(String::new())
}
