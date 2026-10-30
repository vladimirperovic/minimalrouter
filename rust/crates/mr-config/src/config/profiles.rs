use regex::Regex;
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

use super::model::*;
use super::validation::{SAFE_NAME_PATTERN, ValidationErrors, has_unsafe_control, parse_ipv4};
use crate::goish::net::IPNet;
use crate::goish::time::parse_clock;

static DEVICE_PROFILE_ID_PATTERN: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$").expect("valid pattern"));

pub const SUPPORTED_PROFILE_SERVICES: [&str; 11] =
    ["youtube", "steam", "wiki", "tiktok", "instagram", "facebook", "roblox", "epic", "twitch", "adult", "gaming"];

const SUPPORTED_SCHEDULE_DAYS: [&str; 7] = ["monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"];

impl SystemConfig {
    pub(crate) fn validate_device_profiles(&self, lan_network: Option<&IPNet>) -> ValidationErrors {
        let mut errs = ValidationErrors::default();
        if !self.adguard.filter_devices.is_empty() {
            errs.add("adguard.filter_devices", "legacy per-device rules must be migrated to device_profiles");
        }
        if self.adguard.device_profiles.len() > 64 {
            errs.add("adguard.device_profiles", "cannot contain more than 64 profiles");
        }
        let mut seen_ids: HashSet<&str> = HashSet::new();
        let mut seen_ips: HashMap<&str, &str> = HashMap::new();
        for (i, profile) in self.adguard.device_profiles.iter().enumerate() {
            let base = format!("adguard.device_profiles[{}]", i);
            if !DEVICE_PROFILE_ID_PATTERN.is_match(&profile.id) {
                errs.add(format!("{base}.id"), "must contain only letters, numbers, underscore, or hyphen");
            }
            if !seen_ids.insert(profile.id.as_str()) {
                errs.add(format!("{base}.id"), "duplicates another profile");
            }
            if !SAFE_NAME_PATTERN.is_match(&profile.name) || has_unsafe_control(&profile.name) {
                errs.add(format!("{base}.name"), "contains unsupported characters");
            }
            if profile.ip_addresses.is_empty() || profile.ip_addresses.len() > 32 {
                errs.add(format!("{base}.ip_addresses"), "must contain between one and 32 LAN addresses");
            }
            for (j, raw_ip) in profile.ip_addresses.iter().enumerate() {
                let ip = parse_ipv4(raw_ip);
                if ip.is_none() || lan_network.is_some_and(|n| !n.contains_opt(ip.as_ref())) {
                    errs.add(format!("{base}.ip_addresses[{j}]"), "must be a valid address in the LAN subnet");
                }
                if let Some(owner) = seen_ips.get(raw_ip.as_str()) {
                    if *owner != profile.id {
                        errs.add(format!("{base}.ip_addresses[{j}]"), "is already controlled by another profile");
                    }
                }
                seen_ips.insert(raw_ip.as_str(), profile.id.as_str());
            }
            if profile.services.is_empty() || profile.services.len() > SUPPORTED_PROFILE_SERVICES.len() {
                errs.add(format!("{base}.services"), "must contain at least one supported service");
            }
            let mut seen_services: HashSet<&str> = HashSet::new();
            for (j, service) in profile.services.iter().enumerate() {
                if *service != crate::goish::strings::to_lower(service.trim()) {
                    errs.add(format!("{base}.services[{j}]"), "must be a lowercase service identifier");
                }
                if !SUPPORTED_PROFILE_SERVICES.contains(&service.as_str()) {
                    errs.add(format!("{base}.services[{j}]"), "is not a supported managed service");
                }
                if !seen_services.insert(service.as_str()) {
                    errs.add(format!("{base}.services[{j}]"), "duplicates another service");
                }
            }
            validate_profile_schedule(&mut errs, &format!("{base}.schedule"), &profile.schedule);
        }
        errs
    }
}

fn validate_profile_schedule(errs: &mut ValidationErrors, field: &str, schedule: &WeeklyAccessSchedule) {
    if !schedule.day_windows.is_empty() {
        if schedule.day_windows.len() > 7 {
            errs.add(format!("{field}.day_windows"), "cannot contain more than seven days");
        }
        if !schedule.weekday_windows.is_empty() || !schedule.weekend_mode.is_empty() || !schedule.weekend_windows.is_empty() {
            errs.add(field, "must not mix day_windows with legacy weekday/weekend fields");
        }
        // Go iterates the map in random order; the BTreeMap gives a stable
        // order, which only changes the order of reported errors.
        for (day, windows) in &schedule.day_windows {
            if !SUPPORTED_SCHEDULE_DAYS.contains(&day.as_str()) {
                errs.add(format!("{field}.day_windows.{day}"), "is not a supported weekday");
                continue;
            }
            validate_windows(errs, &format!("{field}.day_windows.{day}"), windows);
        }
        return;
    }

    validate_windows(errs, &format!("{field}.weekday_windows"), &schedule.weekday_windows);
    match schedule.weekend_mode.as_str() {
        "all_day" | "blocked" | "same_as_weekdays" | "" => {
            if !schedule.weekend_windows.is_empty() {
                errs.add(format!("{field}.weekend_windows"), "must be empty unless weekend_mode is custom");
            }
        }
        "custom" => {
            if schedule.weekend_windows.is_empty() {
                errs.add(format!("{field}.weekend_windows"), "must contain at least one window for custom weekend mode");
            }
            validate_windows(errs, &format!("{field}.weekend_windows"), &schedule.weekend_windows);
        }
        _ => errs.add(format!("{field}.weekend_mode"), "must be all_day, blocked, same_as_weekdays, or custom"),
    }
}

fn validate_windows(errs: &mut ValidationErrors, field: &str, windows: &[AccessWindow]) {
    // A 24-slot hourly scheduler can produce at most 12 non-overlapping windows.
    if windows.len() > 12 {
        errs.add(field, "cannot contain more than twelve windows");
        return;
    }
    let mut parsed: Vec<(u32, u32)> = Vec::with_capacity(windows.len());
    for (i, window) in windows.iter().enumerate() {
        let (Some((sh, sm)), Some((eh, em))) = (parse_clock(&window.start), parse_clock(&window.end)) else {
            errs.add(format!("{field}[{i}]"), "must use HH:MM 24-hour times");
            continue;
        };
        let start = sh * 60 + sm;
        let end = eh * 60 + em;
        if end <= start {
            errs.add(format!("{field}[{i}]"), "end must be later than start; split midnight-spanning access");
            continue;
        }
        parsed.push((start, end));
    }
    parsed.sort_by_key(|w| w.0);
    if parsed.windows(2).any(|pair| pair[1].0 < pair[0].1) {
        errs.add(field, "windows must not overlap");
    }
}
