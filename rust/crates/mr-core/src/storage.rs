//! Disk-pressure evaluation for durable and non-essential writes.

use serde::Serialize;

pub const WARNING_USAGE_PERCENT: f64 = 80.0;
pub const CRITICAL_USAGE_PERCENT: f64 = 90.0;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, serde::Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum PressureLevel {
    Unknown,
    Normal,
    Warning,
    Critical,
}

impl PressureLevel {
    pub fn as_str(&self) -> &'static str {
        match self {
            PressureLevel::Unknown => "unknown",
            PressureLevel::Normal => "normal",
            PressureLevel::Warning => "warning",
            PressureLevel::Critical => "critical",
        }
    }
}

crate::sentinel!(ErrCriticalPressure, "durable writes blocked by critical disk pressure");

#[derive(Clone, Debug, PartialEq, Serialize, serde::Deserialize)]
pub struct Status {
    pub available: bool,
    #[serde(default, skip_serializing_if = "crate::goish::json::is_zero_u64")]
    pub total_bytes: u64,
    #[serde(default, skip_serializing_if = "crate::goish::json::is_zero_u64")]
    pub used_bytes: u64,
    #[serde(default, skip_serializing_if = "crate::goish::json::is_zero_u64")]
    pub free_bytes: u64,
    #[serde(default, skip_serializing_if = "crate::goish::json::is_zero_f64")]
    pub usage_percent: f64,
    pub level: PressureLevel,
    pub nonessential_writes_allowed: bool,
    pub durable_writes_allowed: bool,
}

impl Default for Status {
    fn default() -> Self {
        evaluate(0, 0)
    }
}

pub fn evaluate(total_bytes: u64, available_bytes: u64) -> Status {
    let mut status = Status {
        available: total_bytes > 0,
        total_bytes,
        used_bytes: 0,
        free_bytes: available_bytes,
        usage_percent: 0.0,
        level: PressureLevel::Unknown,
        nonessential_writes_allowed: true,
        durable_writes_allowed: true,
    };
    if total_bytes == 0 {
        return status;
    }
    let available_bytes = available_bytes.min(total_bytes);
    status.free_bytes = available_bytes;
    status.used_bytes = total_bytes - available_bytes;
    status.usage_percent = status.used_bytes as f64 / total_bytes as f64 * 100.0;
    if status.usage_percent >= CRITICAL_USAGE_PERCENT {
        status.level = PressureLevel::Critical;
        status.nonessential_writes_allowed = false;
        status.durable_writes_allowed = false;
    } else if status.usage_percent >= WARNING_USAGE_PERCENT {
        status.level = PressureLevel::Warning;
    } else {
        status.level = PressureLevel::Normal;
    }
    status
}

pub fn require_durable_write(path: &str) -> crate::goish::errors::Result<()> {
    let status = inspect(path);
    if status.available && !status.durable_writes_allowed {
        return Err(crate::goish::errors::wrap(ErrCriticalPressure, format!("{}: {:.1}% used", ErrCriticalPressure, status.usage_percent)));
    }
    Ok(())
}

pub fn allow_nonessential_write(path: &str) -> bool {
    let status = inspect(path);
    !status.available || status.nonessential_writes_allowed
}

#[cfg(target_os = "linux")]
pub fn inspect(path: &str) -> Status {
    if path.is_empty() {
        return evaluate(0, 0);
    }
    match crate::sys::filesystem_space(path) {
        Ok(space) => evaluate(space.total, space.available),
        Err(_) => evaluate(0, 0),
    }
}

#[cfg(not(target_os = "linux"))]
pub fn inspect(_path: &str) -> Status {
    evaluate(0, 0)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn levels_follow_thresholds() {
        assert_eq!(evaluate(0, 0).level, PressureLevel::Unknown);
        assert_eq!(evaluate(100, 50).level, PressureLevel::Normal);
        assert_eq!(evaluate(100, 20).level, PressureLevel::Warning);
        let critical = evaluate(100, 10);
        assert_eq!(critical.level, PressureLevel::Critical);
        assert!(!critical.durable_writes_allowed);
        assert_eq!(evaluate(100, 200).used_bytes, 0);
    }

    #[test]
    fn status_json_matches_go() {
        let json = crate::goish::json::marshal_string(&evaluate(0, 0)).unwrap();
        assert_eq!(json, r#"{"available":false,"level":"unknown","nonessential_writes_allowed":true,"durable_writes_allowed":true}"#);
    }
}
