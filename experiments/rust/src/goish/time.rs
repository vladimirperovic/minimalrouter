//! `time` package compatibility: RFC3339Nano JSON encoding of `time.Time`,
//! `time.ParseDuration`, `Duration.String` and the `15:04` clock layout.

use chrono::{DateTime, FixedOffset, Local, NaiveDate, SecondsFormat, TimeZone, Utc};
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use std::fmt;
use std::time::Duration as StdDuration;

/// A Go `time.Time` value. The zero value is `0001-01-01T00:00:00Z`.
#[derive(Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub struct Time(pub DateTime<FixedOffset>);

impl fmt::Debug for Time {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "Time({})", self.rfc3339_nano())
    }
}

fn zero_instant() -> DateTime<FixedOffset> {
    let naive = NaiveDate::from_ymd_opt(1, 1, 1).unwrap().and_hms_opt(0, 0, 0).unwrap();
    Utc.from_utc_datetime(&naive).fixed_offset()
}

impl Default for Time {
    fn default() -> Self {
        Time(zero_instant())
    }
}

impl Time {
    pub fn now() -> Time {
        Time(Local::now().fixed_offset())
    }

    pub fn now_utc() -> Time {
        Time(Utc::now().fixed_offset())
    }

    pub fn zero() -> Time {
        Time::default()
    }

    pub fn is_zero(&self) -> bool {
        self.0.timestamp() == zero_instant().timestamp() && self.0.timestamp_subsec_nanos() == 0
    }

    pub fn utc(&self) -> Time {
        Time(self.0.with_timezone(&Utc).fixed_offset())
    }

    pub fn unix(&self) -> i64 {
        self.0.timestamp()
    }

    pub fn unix_nano(&self) -> i64 {
        self.0.timestamp_nanos_opt().unwrap_or(i64::MIN)
    }

    pub fn from_unix(secs: i64, nanos: u32) -> Time {
        Time(Utc.timestamp_opt(secs, nanos).single().unwrap_or_else(|| Utc.timestamp_opt(0, 0).unwrap()).fixed_offset())
    }

    pub fn add(&self, d: Duration) -> Time {
        Time(self.0 + chrono::Duration::nanoseconds(d.0))
    }

    pub fn sub(&self, other: &Time) -> Duration {
        let delta = self.0.signed_duration_since(other.0);
        Duration(delta.num_nanoseconds().unwrap_or(if delta > chrono::Duration::zero() { i64::MAX } else { i64::MIN }))
    }

    pub fn before(&self, other: &Time) -> bool {
        self.0 < other.0
    }

    pub fn after(&self, other: &Time) -> bool {
        self.0 > other.0
    }

    /// Go `time.RFC3339Nano` formatting.
    pub fn rfc3339_nano(&self) -> String {
        let text = self.0.to_rfc3339_opts(SecondsFormat::Nanos, true);
        trim_fraction(&text)
    }

    /// Go `time.RFC3339` formatting (whole seconds).
    pub fn rfc3339(&self) -> String {
        self.0.to_rfc3339_opts(SecondsFormat::Secs, true)
    }

    pub fn parse_rfc3339(value: &str) -> Option<Time> {
        DateTime::parse_from_rfc3339(value).ok().map(Time)
    }
}

/// Removes trailing zeros (and an empty fraction) the way `.999999999` does.
fn trim_fraction(text: &str) -> String {
    let Some(dot) = text.find('.') else {
        return text.to_string();
    };
    let zone_start = text[dot..].find(['Z', '+', '-']).map(|i| dot + i).unwrap_or(text.len());
    let fraction = text[dot + 1..zone_start].trim_end_matches('0');
    if fraction.is_empty() {
        format!("{}{}", &text[..dot], &text[zone_start..])
    } else {
        format!("{}.{}{}", &text[..dot], fraction, &text[zone_start..])
    }
}

impl Serialize for Time {
    fn serialize<S: Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        serializer.serialize_str(&self.rfc3339_nano())
    }
}

impl<'de> Deserialize<'de> for Time {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let value = Option::<String>::deserialize(deserializer)?;
        match value {
            // Go leaves the value unchanged on JSON null.
            None => Ok(Time::default()),
            Some(text) => Time::parse_rfc3339(&text)
                .ok_or_else(|| serde::de::Error::custom(format!("parsing time \"{}\" as RFC3339", text))),
        }
    }
}

/// A Go `time.Duration`: signed nanoseconds.
#[derive(Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Hash, Debug, Default)]
pub struct Duration(pub i64);

impl Duration {
    pub const NANOSECOND: Duration = Duration(1);
    pub const MICROSECOND: Duration = Duration(1_000);
    pub const MILLISECOND: Duration = Duration(1_000_000);
    pub const SECOND: Duration = Duration(1_000_000_000);
    pub const MINUTE: Duration = Duration(60 * 1_000_000_000);
    pub const HOUR: Duration = Duration(3600 * 1_000_000_000);

    pub const fn secs(n: i64) -> Duration {
        Duration(n * 1_000_000_000)
    }

    pub const fn millis(n: i64) -> Duration {
        Duration(n * 1_000_000)
    }

    pub const fn minutes(n: i64) -> Duration {
        Duration(n * 60 * 1_000_000_000)
    }

    pub const fn hours(n: i64) -> Duration {
        Duration(n * 3600 * 1_000_000_000)
    }

    pub fn as_secs_f64(&self) -> f64 {
        self.0 as f64 / 1e9
    }

    pub fn seconds(&self) -> f64 {
        self.as_secs_f64()
    }

    pub fn to_std(&self) -> StdDuration {
        if self.0 <= 0 { StdDuration::ZERO } else { StdDuration::from_nanos(self.0 as u64) }
    }

    pub fn from_std(d: StdDuration) -> Duration {
        Duration(d.as_nanos().min(i64::MAX as u128) as i64)
    }

    pub fn truncate(&self, m: Duration) -> Duration {
        if m.0 <= 0 { *self } else { Duration(self.0 - self.0 % m.0) }
    }

    pub fn round(&self, m: Duration) -> Duration {
        if m.0 <= 0 {
            return *self;
        }
        let mut r = self.0 % m.0;
        if self.0 < 0 {
            r = -r;
            if r + r < m.0 {
                return Duration(self.0 + r);
            }
            return Duration(self.0 - m.0 + r);
        }
        if r + r < m.0 {
            return Duration(self.0 - r);
        }
        Duration(self.0 + m.0 - r)
    }
}

impl std::ops::Add for Duration {
    type Output = Duration;
    fn add(self, rhs: Duration) -> Duration {
        Duration(self.0 + rhs.0)
    }
}

impl std::ops::Mul<i64> for Duration {
    type Output = Duration;
    fn mul(self, rhs: i64) -> Duration {
        Duration(self.0 * rhs)
    }
}

impl fmt::Display for Duration {
    /// `Duration.String()`, e.g. `1h2m3.5s`, `150ms`, `0s`.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&duration_string(self.0))
    }
}

fn fmt_frac(v: u64, prec: usize) -> (String, u64) {
    let mut digits = Vec::new();
    let mut v = v;
    let mut printed = false;
    for _ in 0..prec {
        let digit = v % 10;
        printed = printed || digit != 0;
        if printed {
            digits.push((b'0' + digit as u8) as char);
        }
        v /= 10;
    }
    let mut out = String::new();
    if printed {
        out.push('.');
        out.extend(digits.iter().rev());
    }
    (out, v)
}

fn duration_string(d: i64) -> String {
    let neg = d < 0;
    let u = d.unsigned_abs();
    let body = if u < 1_000_000_000 {
        if u == 0 {
            return "0s".into();
        }
        let (prec, unit) = if u < 1_000 {
            (0, "ns")
        } else if u < 1_000_000 {
            (3, "µs")
        } else {
            (6, "ms")
        };
        let (frac, int) = fmt_frac(u, prec);
        format!("{}{}{}", int, frac, unit)
    } else {
        let (frac, mut v) = fmt_frac(u, 9);
        let secs = v % 60;
        v /= 60;
        let mut out = format!("{}{}s", secs, frac);
        if v > 0 {
            let mins = v % 60;
            v /= 60;
            out = format!("{}m{}", mins, out);
            if v > 0 {
                out = format!("{}h{}", v, out);
            }
        }
        out
    };
    if neg { format!("-{}", body) } else { body }
}

/// `time.ParseDuration`.
pub fn parse_duration(input: &str) -> Result<Duration, String> {
    let err = || format!("time: invalid duration \"{}\"", input);
    let mut s = input;
    let mut neg = false;
    if let Some(rest) = s.strip_prefix('-') {
        neg = true;
        s = rest;
    } else if let Some(rest) = s.strip_prefix('+') {
        s = rest;
    }
    if s == "0" {
        return Ok(Duration(0));
    }
    if s.is_empty() {
        return Err(err());
    }
    let mut total: u64 = 0;
    while !s.is_empty() {
        let b = s.as_bytes();
        if !(b[0] == b'.' || b[0].is_ascii_digit()) {
            return Err(err());
        }
        // Integer part.
        let int_len = b.iter().take_while(|c| c.is_ascii_digit()).count();
        let pre = int_len > 0;
        let mut v: u64 = 0;
        for &c in &b[..int_len] {
            if v > (1u64 << 63) / 10 {
                return Err(err());
            }
            v = v * 10 + (c - b'0') as u64;
            if v > 1u64 << 63 {
                return Err(err());
            }
        }
        s = &s[int_len..];
        // Fraction.
        let mut f: u64 = 0;
        let mut scale: f64 = 1.0;
        let mut post = false;
        if let Some(rest) = s.strip_prefix('.') {
            s = rest;
            let frac_len = s.bytes().take_while(|c| c.is_ascii_digit()).count();
            post = frac_len > 0;
            let mut overflow = false;
            for c in s[..frac_len].bytes() {
                if overflow {
                    continue;
                }
                if f > (1u64 << 63) / 10 {
                    overflow = true;
                    continue;
                }
                let y = f * 10 + (c - b'0') as u64;
                if y > 1u64 << 63 {
                    overflow = true;
                    continue;
                }
                f = y;
                scale *= 10.0;
            }
            s = &s[frac_len..];
        }
        if !pre && !post {
            return Err(err());
        }
        // Unit.
        let unit_len = s.bytes().take_while(|c| *c != b'.' && !c.is_ascii_digit()).count();
        if unit_len == 0 {
            return Err(format!("time: missing unit in duration \"{}\"", input));
        }
        let unit = &s[..unit_len];
        s = &s[unit_len..];
        let multiplier: u64 = match unit {
            "ns" => 1,
            "us" | "µs" | "μs" => 1_000,
            "ms" => 1_000_000,
            "s" => 1_000_000_000,
            "m" => 60 * 1_000_000_000,
            "h" => 3600 * 1_000_000_000,
            _ => return Err(format!("time: unknown unit \"{}\" in duration \"{}\"", unit, input)),
        };
        if v > (1u64 << 63) / multiplier {
            return Err(err());
        }
        v *= multiplier;
        if f > 0 {
            v += (f as f64 * (multiplier as f64 / scale)) as u64;
            if v > 1u64 << 63 {
                return Err(err());
            }
        }
        total += v;
        if total > 1u64 << 63 {
            return Err(err());
        }
    }
    if neg {
        return Ok(Duration((total as i128 * -1) as i64));
    }
    if total > (1u64 << 63) - 1 {
        return Err(err());
    }
    Ok(Duration(total as i64))
}

/// `time.Parse("15:04", value)`: hour of one or two digits, minute of exactly two.
pub fn parse_clock(value: &str) -> Option<(u32, u32)> {
    let b = value.as_bytes();
    let (hour, rest) = getnum(b, false)?;
    if hour >= 24 {
        return None;
    }
    let rest = rest.strip_prefix(b":")?;
    let (minute, rest) = getnum(rest, true)?;
    if minute >= 60 || !rest.is_empty() {
        return None;
    }
    Some((hour, minute))
}

fn getnum(s: &[u8], fixed: bool) -> Option<(u32, &[u8])> {
    if s.is_empty() || !s[0].is_ascii_digit() {
        return None;
    }
    if s.len() < 2 || !s[1].is_ascii_digit() {
        if fixed {
            return None;
        }
        return Some(((s[0] - b'0') as u32, &s[1..]));
    }
    Some((((s[0] - b'0') * 10 + (s[1] - b'0')) as u32, &s[2..]))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn durations_match_go() {
        assert_eq!(parse_duration("12h").unwrap(), Duration::hours(12));
        assert_eq!(parse_duration("1m").unwrap(), Duration::MINUTE);
        assert_eq!(parse_duration("1.5h").unwrap(), Duration::minutes(90));
        assert_eq!(parse_duration("1h30m").unwrap(), Duration::minutes(90));
        assert_eq!(parse_duration("0").unwrap(), Duration(0));
        assert!(parse_duration("12").is_err());
        assert!(parse_duration("").is_err());
        assert!(parse_duration("1d").is_err());
        assert!(parse_duration(".h").is_err());
        assert_eq!(Duration::minutes(90).to_string(), "1h30m0s");
        assert_eq!(Duration::millis(150).to_string(), "150ms");
        assert_eq!(Duration(1_500_000_000).to_string(), "1.5s");
        assert_eq!(Duration(0).to_string(), "0s");
    }

    #[test]
    fn clock_layout_matches_go() {
        assert_eq!(parse_clock("09:05"), Some((9, 5)));
        assert_eq!(parse_clock("9:05"), Some((9, 5)));
        assert_eq!(parse_clock("23:59"), Some((23, 59)));
        assert_eq!(parse_clock("24:00"), None);
        assert_eq!(parse_clock("12:5"), None);
        assert_eq!(parse_clock("12:05x"), None);
    }

    #[test]
    fn time_json_matches_go() {
        assert_eq!(Time::default().rfc3339_nano(), "0001-01-01T00:00:00Z");
        let t = Time::parse_rfc3339("2026-10-26T12:00:00.120000000+02:00").unwrap();
        assert_eq!(t.rfc3339_nano(), "2026-10-26T12:00:00.12+02:00");
        assert!(Time::default().is_zero());
    }
}

impl Time {
    /// Go `Time.String()` without a monotonic reading, which is how the
    /// pure-Go SQLite driver persists `time.Time` arguments:
    /// `2006-01-02 15:04:05.999999999 -0700 MST`.
    pub fn go_string(&self) -> String {
        let offset = self.0.offset().local_minus_utc();
        let base = self.0.format("%Y-%m-%d %H:%M:%S%.9f").to_string();
        let base = trim_fraction(&format!("{}Z", base));
        let base = base.trim_end_matches('Z');
        let sign = if offset < 0 { '-' } else { '+' };
        let abs = offset.abs();
        let numeric = format!("{}{:02}{:02}", sign, abs / 3600, (abs % 3600) / 60);
        let name = if offset == 0 { "UTC".to_string() } else { numeric.clone() };
        format!("{} {} {}", base, numeric, name)
    }

    /// Parses the text forms the pure-Go SQLite driver accepts for DATETIME
    /// columns: `Time.String()` output and the ISO-like layouts.
    pub fn parse_sqlite(value: &str) -> Option<Time> {
        let text = match value.find("m=") {
            Some(i) if i > 0 => &value[..i],
            _ => value,
        }
        .trim();
        // "2006-01-02 15:04:05.999999999 -0700 MST"
        let parts: Vec<&str> = text.split(' ').collect();
        if parts.len() == 4 {
            let candidate = format!("{} {} {}", parts[0], parts[1], parts[2]);
            if let Ok(t) = DateTime::parse_from_str(&candidate, "%Y-%m-%d %H:%M:%S%.f %z") {
                return Some(Time(t));
            }
        }
        let trimmed = value.strip_suffix('Z').unwrap_or(value);
        for layout in ["%Y-%m-%d %H:%M:%S%.f%:z", "%Y-%m-%dT%H:%M:%S%.f%:z"] {
            if let Ok(t) = DateTime::parse_from_str(trimmed, layout) {
                return Some(Time(t));
            }
        }
        for layout in ["%Y-%m-%d %H:%M:%S%.f", "%Y-%m-%dT%H:%M:%S%.f", "%Y-%m-%d %H:%M", "%Y-%m-%dT%H:%M"] {
            if let Ok(naive) = chrono::NaiveDateTime::parse_from_str(trimmed, layout) {
                return Some(Time(Utc.from_utc_datetime(&naive).fixed_offset()));
            }
        }
        if let Ok(date) = NaiveDate::parse_from_str(trimmed, "%Y-%m-%d") {
            return Some(Time(Utc.from_utc_datetime(&date.and_hms_opt(0, 0, 0)?).fixed_offset()));
        }
        // RFC3339 strings written explicitly by the store.
        Time::parse_rfc3339(value)
    }
}

#[cfg(test)]
mod sqlite_time_tests {
    use super::*;

    #[test]
    fn go_string_round_trips_through_sqlite_parser() {
        let t = Time::parse_rfc3339("2026-10-26T12:34:56.120Z").unwrap();
        assert_eq!(t.go_string(), "2026-10-26 12:34:56.12 +0000 UTC");
        assert_eq!(Time::parse_sqlite(&t.go_string()).unwrap(), t);
        let whole = Time::parse_rfc3339("2026-10-26T12:34:56Z").unwrap();
        assert_eq!(whole.go_string(), "2026-10-26 12:34:56 +0000 UTC");
        assert_eq!(Time::parse_sqlite("2026-10-26T12:34:56Z").unwrap(), whole);
    }
}
