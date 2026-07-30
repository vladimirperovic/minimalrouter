package config

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"
)

var deviceProfileIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

var supportedProfileServices = map[string]struct{}{
	"youtube": {}, "steam": {}, "tiktok": {}, "instagram": {}, "facebook": {},
	"roblox": {}, "epic": {}, "twitch": {}, "adult": {}, "gaming": {},
}

func (c *SystemConfig) validateDeviceProfiles(lanNetwork *net.IPNet) ValidationErrors {
	var errs ValidationErrors
	if len(c.AdGuard.FilterDevices) > 0 {
		appendFieldError(&errs, "adguard.filter_devices", "legacy per-device rules must be migrated to device_profiles")
	}
	if len(c.AdGuard.DeviceProfiles) > 64 {
		appendFieldError(&errs, "adguard.device_profiles", "cannot contain more than 64 profiles")
	}
	seenIDs := make(map[string]struct{})
	seenIPs := make(map[string]string)
	for i, profile := range c.AdGuard.DeviceProfiles {
		base := fmt.Sprintf("adguard.device_profiles[%d]", i)
		if !deviceProfileIDPattern.MatchString(profile.ID) {
			appendFieldError(&errs, base+".id", "must contain only letters, numbers, underscore, or hyphen")
		}
		if _, exists := seenIDs[profile.ID]; exists {
			appendFieldError(&errs, base+".id", "duplicates another profile")
		}
		seenIDs[profile.ID] = struct{}{}
		if !safeNamePattern.MatchString(profile.Name) || hasUnsafeControl(profile.Name) {
			appendFieldError(&errs, base+".name", "contains unsupported characters")
		}
		if len(profile.IPAddresses) == 0 || len(profile.IPAddresses) > 32 {
			appendFieldError(&errs, base+".ip_addresses", "must contain between one and 32 LAN addresses")
		}
		for j, rawIP := range profile.IPAddresses {
			ip := parseIPv4(rawIP)
			if ip == nil || (lanNetwork != nil && !lanNetwork.Contains(ip)) {
				appendFieldError(&errs, fmt.Sprintf("%s.ip_addresses[%d]", base, j), "must be a valid address in the LAN subnet")
			}
			if owner, exists := seenIPs[rawIP]; exists && owner != profile.ID {
				appendFieldError(&errs, fmt.Sprintf("%s.ip_addresses[%d]", base, j), "is already controlled by another profile")
			}
			seenIPs[rawIP] = profile.ID
		}
		if len(profile.Services) == 0 || len(profile.Services) > len(supportedProfileServices) {
			appendFieldError(&errs, base+".services", "must contain at least one supported service")
		}
		seenServices := make(map[string]struct{})
		for j, service := range profile.Services {
			if service != strings.ToLower(strings.TrimSpace(service)) {
				appendFieldError(&errs, fmt.Sprintf("%s.services[%d]", base, j), "must be a lowercase service identifier")
			}
			if _, supported := supportedProfileServices[service]; !supported {
				appendFieldError(&errs, fmt.Sprintf("%s.services[%d]", base, j), "is not a supported managed service")
			}
			if _, duplicate := seenServices[service]; duplicate {
				appendFieldError(&errs, fmt.Sprintf("%s.services[%d]", base, j), "duplicates another service")
			}
			seenServices[service] = struct{}{}
		}
		validateWindows(&errs, base+".schedule.weekday_windows", profile.Schedule.WeekdayWindows)
		switch profile.Schedule.WeekendMode {
		case "all_day", "blocked", "same_as_weekdays":
			if len(profile.Schedule.WeekendWindows) != 0 {
				appendFieldError(&errs, base+".schedule.weekend_windows", "must be empty unless weekend_mode is custom")
			}
		case "custom":
			if len(profile.Schedule.WeekendWindows) == 0 {
				appendFieldError(&errs, base+".schedule.weekend_windows", "must contain at least one window for custom weekend mode")
			}
			validateWindows(&errs, base+".schedule.weekend_windows", profile.Schedule.WeekendWindows)
		default:
			appendFieldError(&errs, base+".schedule.weekend_mode", "must be all_day, blocked, same_as_weekdays, or custom")
		}
	}
	return errs
}

func validateWindows(errs *ValidationErrors, field string, windows []AccessWindow) {
	if len(windows) > 8 {
		appendFieldError(errs, field, "cannot contain more than eight windows")
		return
	}
	type parsedWindow struct {
		start int
		end   int
	}
	parsed := make([]parsedWindow, 0, len(windows))
	for i, window := range windows {
		start, startErr := time.Parse("15:04", window.Start)
		end, endErr := time.Parse("15:04", window.End)
		if startErr != nil || endErr != nil {
			appendFieldError(errs, fmt.Sprintf("%s[%d]", field, i), "must use HH:MM 24-hour times")
			continue
		}
		startMinute := start.Hour()*60 + start.Minute()
		endMinute := end.Hour()*60 + end.Minute()
		if endMinute <= startMinute {
			appendFieldError(errs, fmt.Sprintf("%s[%d]", field, i), "end must be later than start; split midnight-spanning access")
			continue
		}
		parsed = append(parsed, parsedWindow{start: startMinute, end: endMinute})
	}
	sort.Slice(parsed, func(i, j int) bool { return parsed[i].start < parsed[j].start })
	for i := 1; i < len(parsed); i++ {
		if parsed[i].start < parsed[i-1].end {
			appendFieldError(errs, field, "windows must not overlap")
			break
		}
	}
}
