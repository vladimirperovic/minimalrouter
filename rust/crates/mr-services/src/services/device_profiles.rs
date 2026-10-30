use std::collections::{BTreeMap, BTreeSet};
use std::fmt::Write;

use crate::config::{AccessWindow, SystemConfig, WeeklyAccessSchedule};
use crate::goish::json::List;
use crate::goish::strings::to_lower;

pub(crate) const ORDERED_SCHEDULE_DAYS: [&str; 7] = ["monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"];

/// Stable service identifiers mapped to domains whose resolved IPv4
/// destinations dnsmasq inserts into nftables sets.
pub fn service_domains(service: &str) -> Option<&'static [&'static str]> {
    Some(match service {
        "youtube" => &["youtube.com", "googlevideo.com", "ytimg.com", "youtu.be"],
        "steam" => &["steampowered.com", "steamcommunity.com", "steamstatic.com", "steamcontent.com", "steamserver.net"],
        "wiki" => &["wikipedia.org", "wikimedia.org", "wikimediafoundation.org", "mediawiki.org"],
        "tiktok" => &["tiktok.com", "byteoversea.com", "ibytedtos.com", "tiktokv.com"],
        "instagram" => &["instagram.com", "cdninstagram.com"],
        "facebook" => &["facebook.com", "fbcdn.net", "messenger.com"],
        "roblox" => &["roblox.com", "rbxcdn.com"],
        "epic" => &["epicgames.com", "unrealengine.com"],
        "twitch" => &["twitch.tv", "ttvnw.net", "jtvnw.net"],
        "adult" => &["pornhub.com", "xvideos.com", "xnxx.com", "onlyfans.com"],
        "gaming" => &["roblox.com", "rbxcdn.com", "twitch.tv", "ttvnw.net", "steampowered.com", "steamcommunity.com", "epicgames.com"],
        _ => return None,
    })
}

pub(crate) fn active_managed_services(cfg: &SystemConfig) -> Vec<String> {
    let mut seen = BTreeSet::new();
    for profile in cfg.adguard.device_profiles.iter() {
        if !cfg.adguard.enabled || !profile.enabled {
            continue;
        }
        for service in profile.services.iter() {
            let service = to_lower(service.trim());
            if service_domains(&service).is_some() {
                seen.insert(service);
            }
        }
    }
    seen.into_iter().collect()
}

pub(crate) fn write_dns_filter_nftsets(buf: &mut String, cfg: &SystemConfig) {
    let services = active_managed_services(cfg);
    if services.is_empty() {
        return;
    }
    buf.push_str("\n# Device profile service destination sets\n");
    for service in services {
        let domains = service_domains(&service).unwrap_or(&[]);
        if domains.is_empty() {
            continue;
        }
        let _ = writeln!(buf, "nftset=/{}/4#inet#minimalrouter#svc_{}", domains.join("/"), service);
    }
}

pub(crate) fn effective_day_windows(schedule: &WeeklyAccessSchedule) -> BTreeMap<String, List<AccessWindow>> {
    if !schedule.day_windows.is_empty() {
        return schedule.day_windows.clone();
    }
    let mut result = BTreeMap::new();
    for day in &ORDERED_SCHEDULE_DAYS[..5] {
        result.insert(day.to_string(), schedule.weekday_windows.clone());
    }
    let all_day = || List::from(vec![AccessWindow { start: "00:00".into(), end: "23:59".into() }]);
    match schedule.weekend_mode.as_str() {
        "all_day" => {
            result.insert("saturday".into(), all_day());
            result.insert("sunday".into(), all_day());
        }
        "same_as_weekdays" => {
            result.insert("saturday".into(), schedule.weekday_windows.clone());
            result.insert("sunday".into(), schedule.weekday_windows.clone());
        }
        "custom" => {
            result.insert("saturday".into(), schedule.weekend_windows.clone());
            result.insert("sunday".into(), schedule.weekend_windows.clone());
        }
        _ => {}
    }
    result
}

pub(crate) fn write_device_profile_objects(buf: &mut String, cfg: &SystemConfig) {
    let services = active_managed_services(cfg);
    if services.is_empty() {
        return;
    }
    buf.push_str("  # Dynamic destination sets populated by dnsmasq from DNS answers.\n");
    for service in &services {
        let _ = writeln!(buf, "  set svc_{} {{ type ipv4_addr; flags timeout; timeout 4h; }}", service);
    }
    buf.push_str("\n  chain device_profiles {\n");
    for profile in cfg.adguard.device_profiles.iter() {
        if !profile.enabled {
            continue;
        }
        let _ = writeln!(buf, "    # Device profile: {}", profile.name);
        let day_windows = effective_day_windows(&profile.schedule);
        for ip in profile.ip_addresses.iter() {
            // Managed devices must use the router resolver; direct DNS and DoT
            // would bypass the DNS-derived destination sets.
            let _ = writeln!(buf, "    ip saddr {} udp dport {{ 53, 853 }} drop", ip);
            let _ = writeln!(buf, "    ip saddr {} tcp dport {{ 53, 853 }} drop", ip);
            for service in profile.services.iter() {
                if service_domains(service).is_none() {
                    continue;
                }
                for (index, day) in ORDERED_SCHEDULE_DAYS.iter().enumerate() {
                    // nft's weekday ABI is Sunday=0 through Saturday=6.
                    let windows = day_windows.get(*day).map(|w| w.as_slice()).unwrap_or(&[]);
                    write_allowed_windows(buf, ip, service, (index + 1) % 7, windows);
                }
                let _ = writeln!(buf, "    ip saddr {} ip daddr @svc_{} drop", ip, service);
            }
        }
    }
    buf.push_str("    return\n  }\n\n");
}

fn write_allowed_windows(buf: &mut String, ip: &str, service: &str, day: usize, windows: &[AccessWindow]) {
    for window in windows {
        // The UI uses 23:59 as the sentinel for an allowed day through midnight.
        let end = if window.end == "23:59" { "23:59:59" } else { window.end.as_str() };
        let _ = writeln!(buf, "    ip saddr {} ip daddr @svc_{} meta day {} meta hour \"{}\"-\"{}\" return", ip, service, day, window.start, end);
    }
}
