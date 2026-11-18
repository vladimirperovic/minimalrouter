//! Per-host nftables byte counters folded into bounded monthly totals per LAN
//! device.
//!
//! Only aggregate byte counts per LAN address are stored: no ports,
//! destinations or payload. The `inet minimalrouter` table is recreated on
//! every apply, so a counter decrease is a reset, never negative traffic.
//! routerd reads the counters through two exact doas-allowlisted
//! `nft -j list set` argument vectors and nothing else.
//!
//! The same store holds the separately opt-in DNS activity history (dns.rs),
//! which is browsing history by nature and is documented as such.

mod dns;
mod dns_collector;
mod store;

pub use dns::*;
pub use dns_collector::*;
pub use store::*;

use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::sync::Arc;
use std::time::{Duration as StdDuration, Instant};

use crate::errorf;
use crate::goish::errors::Result;
use crate::goish::json::{List, is_zero_i64};
use crate::goish::time::Time;

/// One host's cumulative byte count as currently held by the kernel.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Counter {
    pub address: String,
    pub bytes: u64,
}

/// The API-facing monthly total for one device.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct DeviceUsage {
    pub address: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub hostname: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub mac: String,
    pub rx_bytes: u64,
    pub tx_bytes: u64,
    pub total_bytes: u64,
    #[serde(rename = "last_seen_epoch", skip_serializing_if = "is_zero_i64")]
    pub last_seen_utc: i64,
}

/// One calendar month (YYYY-MM) of accounting, newest first in API output.
#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct MonthUsage {
    pub month: String,
    pub total_bytes: u64,
    pub devices: List<DeviceUsage>,
}

#[derive(Clone, Debug, Default, PartialEq, Serialize, Deserialize)]
#[serde(default)]
pub struct Snapshot {
    pub available: bool,
    pub enabled: bool,
    pub months: List<MonthUsage>,
    pub updated_at: Time,
}

/// Reads `nft -j list set inet minimalrouter <name>`. A set that does not
/// exist yet simply yields no counters; unknown keys are ignored so an nft
/// version bump cannot break accounting.
pub fn parse_counters(raw: &str) -> Result<Vec<Counter>> {
    parse_set(raw, None)
}

fn parse_set(raw: &str, expected: Option<&str>) -> Result<Vec<Counter>> {
    let trimmed = raw.trim();
    let document: Value = if trimmed.is_empty() { Value::Null } else { serde_json::from_str(trimmed).map_err(|e| errorf!("decode nft set output: {}", e))? };
    let entries: &[Value] = match &document {
        Value::Null => &[],
        Value::Object(map) => match map.get("nftables") {
            None | Some(Value::Null) => &[],
            Some(Value::Array(entries)) => entries,
            Some(_) => return Err(errorf!("decode nft set output: nftables is not an array")),
        },
        _ => return Err(errorf!("decode nft set output: document is not an object")),
    };
    let mut counters = Vec::new();
    let mut found = false;
    for entry in entries {
        let elements = match entry {
            Value::Null => continue,
            Value::Object(map) => match map.get("set") {
                None | Some(Value::Null) => continue,
                Some(Value::Object(set)) => {
                    if expected.is_some_and(|name| set.get("name").and_then(Value::as_str) != Some(name)) {
                        continue;
                    }
                    found = true;
                    match set.get("elem") {
                        None | Some(Value::Null) => continue,
                        Some(Value::Array(elements)) => elements,
                        Some(_) => return Err(errorf!("decode nft set output: elem is not an array")),
                    }
                }
                Some(_) => return Err(errorf!("decode nft set output: set is not an object")),
            },
            _ => return Err(errorf!("decode nft set output: entry is not an object")),
        };
        for element in elements {
            if let Some((address, bytes)) = parse_element(element) {
                counters.push(Counter { address, bytes });
            } else if expected.is_some() {
                return Err(errorf!("decode nft set output: incomplete accounting element"));
            }
        }
    }
    if expected.is_some() && !found {
        return Err(errorf!("requested accounting set is missing"));
    }
    Ok(counters)
}

/// Both element shapes nft emits: a bare address string for a plain set, and
/// the {"elem": {...}} wrapper once the element carries a counter or timeout.
fn parse_element(raw: &Value) -> Option<(String, u64)> {
    let Value::Object(wrapper) = raw else {
        return decode_address(raw).map(|address| (address, 0));
    };
    let Some(Value::Object(elem)) = wrapper.get("elem") else {
        return None;
    };
    let bytes = match elem.get("counter") {
        None | Some(Value::Null) => 0,
        Some(Value::Object(counter)) => match counter.get("bytes") {
            None | Some(Value::Null) => 0,
            Some(value) => value.as_u64()?,
        },
        Some(_) => return None,
    };
    Some((decode_address(elem.get("val")?)?, bytes))
}

fn decode_address(raw: &Value) -> Option<String> {
    let address = raw.as_str()?.trim();
    crate::goish::net::parse_ip(address).map(|_| address.to_string())
}

/// Bytes to add for a host given the previous raw counter within the same
/// counter generation. A lower value means the counter restarted and the
/// current value is itself the delta.
pub fn delta(previous: u64, current: u64) -> u64 {
    if current >= previous { current - previous } else { current }
}

/// A changed generation (the canonical configuration revision, which changes
/// exactly when the table is recreated) means the whole current value is new
/// traffic, even when it already climbed past the previous observation.
pub fn delta_across_generations(previous_generation: u64, current_generation: u64, previous: u64, current: u64) -> u64 {
    if previous_generation != current_generation { current } else { delta(previous, current) }
}

/// The bucket identifier: calendar month in UTC.
pub fn month_key(at: Time) -> String {
    at.utc().format("%Y-%m")
}

pub const ACCOUNTING_SET_RX_NAME: &str = "acct_rx";
pub const ACCOUNTING_SET_TX_NAME: &str = "acct_tx";

crate::sentinel!(ErrUnsupportedSet, "unsupported accounting set");

/// Returns the raw JSON of one accounting set.
pub trait Reader: Send + Sync {
    fn read_set(&self, deadline: Instant, name: &str) -> Result<String>;
}

/// Executes exactly the two argument vectors the installer grants routerd
/// through doas; any other set name is refused here as well.
pub struct CommandReader;

const COMMAND_TIMEOUT: StdDuration = StdDuration::from_secs(5);

impl Reader for CommandReader {
    fn read_set(&self, deadline: Instant, name: &str) -> Result<String> {
        if name != ACCOUNTING_SET_RX_NAME && name != ACCOUNTING_SET_TX_NAME {
            return Err(ErrUnsupportedSet.into());
        }
        let timeout = deadline.saturating_duration_since(Instant::now()).min(COMMAND_TIMEOUT);
        let output = crate::proc::run("doas", &["/usr/sbin/nft", "-j", "list", "set", "inet", "minimalrouter", name], timeout);
        if let Some(err) = output.error() {
            return Err(errorf!("{}", err));
        }
        Ok(String::from_utf8_lossy(&output.stdout).into_owned())
    }
}

/// The canonical accounting state one collection round works from.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Settings {
    pub enabled: bool,
    pub retention_months: i64,
    /// The canonical configuration revision: a changed value means every
    /// kernel counter restarted at zero.
    pub generation: u64,
}

/// Periodically folds kernel counters into the monthly buckets.
pub struct Collector {
    store: Arc<Store>,
    reader: Arc<dyn Reader>,
    settings: Box<dyn Fn() -> Settings + Send + Sync>,
    interval: StdDuration,
}

impl Collector {
    pub fn new(store: Arc<Store>, reader: Arc<dyn Reader>, settings: Box<dyn Fn() -> Settings + Send + Sync>) -> Collector {
        // Monthly aggregate telemetry, not a real-time safety signal: two
        // short nft reads per five minutes, far below the kernel set timeout.
        Collector { store, reader, settings, interval: StdDuration::from_secs(5 * 60) }
    }

    pub fn run(&self, stop: &crate::shutdown::Shutdown) {
        self.collect_once();
        while stop.sleep(self.interval) {
            self.collect_once();
        }
    }

    pub fn collect_once(&self) {
        let settings = (self.settings)();
        if !settings.enabled {
            // Disabling removes the per-device history rather than freezing
            // it; the durable "already cleared" answer survives a restart.
            match self.store.history_cleared() {
                Err(err) => log::info!("[ACCOUNTING] could not read disable state: {}", err),
                Ok(true) => {}
                Ok(false) => {
                    if let Err(err) = self.store.clear_history() {
                        log::info!("[ACCOUNTING] could not clear history after disable: {}", err);
                    }
                }
            }
            return;
        }
        let Some(rx) = self.read_counters(ACCOUNTING_SET_RX_NAME) else { return };
        let Some(tx) = self.read_counters(ACCOUNTING_SET_TX_NAME) else { return };
        if (self.settings)() != settings {
            return;
        }
        if let Err(err) = self.store.record(Time::now(), &rx, &tx, settings.retention_months, settings.generation) {
            log::info!("[ACCOUNTING] could not record counters: {}", err);
        }
    }

    // Between enabling accounting and the next apply the sets legitimately do
    // not exist yet; a failed read leaves a gap in recent history.
    fn read_counters(&self, name: &str) -> Option<Vec<Counter>> {
        let raw = self.reader.read_set(Instant::now() + COMMAND_TIMEOUT, name).ok()?;
        match parse_set(&raw, Some(name)) {
            Ok(counters) => Some(counters),
            Err(err) => {
                log::info!("[ACCOUNTING] could not parse {}: {}", name, err);
                None
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_both_nft_element_shapes() {
        let raw = r#"{"nftables":[{"metainfo":{"version":"1.0.9"}},{"set":{"name":"acct_rx","table":"minimalrouter","elem":[
            {"elem":{"val":"192.168.1.10","counter":{"packets":3,"bytes":1500}}},
            "192.168.1.11",
            {"elem":{"val":"not-an-ip","counter":{"bytes":9}}},
            {"elem":{"val":"192.168.1.12"}}]}}]}"#;
        let counters = parse_counters(raw).unwrap();
        assert_eq!(
            counters,
            vec![
                Counter { address: "192.168.1.10".into(), bytes: 1500 },
                Counter { address: "192.168.1.11".into(), bytes: 0 },
                Counter { address: "192.168.1.12".into(), bytes: 0 },
            ]
        );
        assert!(parse_counters("").unwrap().is_empty());
        assert!(parse_counters("[1]").is_err());
    }

    #[test]
    fn deltas_handle_resets_and_generations() {
        assert_eq!(delta(100, 250), 150);
        assert_eq!(delta(100, 40), 40);
        assert_eq!(delta_across_generations(1, 2, 100, 200), 200);
        assert_eq!(delta_across_generations(2, 2, 100, 200), 100);
        assert_eq!(month_key(Time::from_unix(1_798_761_599, 0)), "2026-12");
    }

    #[test]
    fn incomplete_reads_and_concurrent_settings_changes_preserve_the_last_complete_sample() {
        use std::sync::atomic::{AtomicU8, Ordering};
        struct Source {
            mode: AtomicU8,
            generation: AtomicU8,
        }
        impl Reader for Source {
            fn read_set(&self, _: Instant, name: &str) -> Result<String> {
                let mode = self.mode.load(Ordering::SeqCst);
                if name == ACCOUNTING_SET_TX_NAME && mode == 1 {
                    self.generation.store(2, Ordering::SeqCst);
                }
                if name == ACCOUNTING_SET_RX_NAME && mode == 2 {
                    return Ok(r#"{"nftables":[{"metainfo":{}}]}"#.into());
                }
                let bytes = if mode == 0 { 100 } else { 130 };
                Ok(serde_json::json!({"nftables":[{"set":{"name":name,"elem":[{"elem":{"val":"192.0.2.9","counter":{"bytes":bytes}}}]}}]}).to_string())
            }
        }
        let source = Arc::new(Source { mode: AtomicU8::new(0), generation: AtomicU8::new(1) });
        let settings = source.clone();
        let store = Arc::new(Store::in_memory());
        let collector = Collector::new(
            store.clone(),
            source.clone(),
            Box::new(move || Settings { enabled: true, retention_months: 13, generation: u64::from(settings.generation.load(Ordering::SeqCst)) }),
        );
        collector.collect_once();
        for mode in [1, 2] {
            source.mode.store(mode, Ordering::SeqCst);
            collector.collect_once();
            assert_eq!(store.months(1).unwrap()[0].total_bytes, 200);
        }
        source.mode.store(3, Ordering::SeqCst);
        collector.collect_once();
        assert_eq!(store.months(1).unwrap()[0].total_bytes, 460);
        assert!(parse_set(r#"{"nftables":[{"set":{"name":"acct_rx"}}]}"#, Some(ACCOUNTING_SET_RX_NAME)).unwrap().is_empty());
        assert!(parse_set(r#"{"nftables":[{"set":{"name":"acct_rx","elem":[{}]}}]}"#, Some(ACCOUNTING_SET_RX_NAME)).is_err());
    }
}
