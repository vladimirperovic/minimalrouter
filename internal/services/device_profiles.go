package services

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

var orderedScheduleDays = []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

func activeManagedServices(cfg *config.SystemConfig) []string {
	seen := make(map[string]struct{})
	for _, profile := range cfg.AdGuard.DeviceProfiles {
		if !cfg.AdGuard.Enabled || !profile.Enabled {
			continue
		}
		for _, service := range profile.Services {
			service = strings.ToLower(strings.TrimSpace(service))
			if _, supported := ServiceDomains[service]; supported {
				seen[service] = struct{}{}
			}
		}
	}
	services := make([]string, 0, len(seen))
	for service := range seen {
		services = append(services, service)
	}
	sort.Strings(services)
	return services
}

func writeDNSFilterNftsets(buf *bytes.Buffer, cfg *config.SystemConfig) {
	services := activeManagedServices(cfg)
	if len(services) == 0 {
		return
	}
	buf.WriteString("\n# Device profile service destination sets\n")
	for _, service := range services {
		domains := ServiceDomains[service]
		if len(domains) == 0 {
			continue
		}
		buf.WriteString(fmt.Sprintf("nftset=/%s/4#inet#minimalrouter#svc_%s\n", strings.Join(domains, "/"), service))
	}
}

func effectiveDayWindows(schedule config.WeeklyAccessSchedule) map[string][]config.AccessWindow {
	if len(schedule.DayWindows) > 0 {
		return schedule.DayWindows
	}
	result := make(map[string][]config.AccessWindow, 7)
	for _, day := range orderedScheduleDays[:5] {
		result[day] = schedule.WeekdayWindows
	}
	switch schedule.WeekendMode {
	case "all_day":
		result["saturday"] = []config.AccessWindow{{Start: "00:00", End: "23:59"}}
		result["sunday"] = []config.AccessWindow{{Start: "00:00", End: "23:59"}}
	case "same_as_weekdays":
		result["saturday"] = schedule.WeekdayWindows
		result["sunday"] = schedule.WeekdayWindows
	case "custom":
		result["saturday"] = schedule.WeekendWindows
		result["sunday"] = schedule.WeekendWindows
	}
	return result
}

func writeDeviceProfileObjects(buf *bytes.Buffer, cfg *config.SystemConfig) {
	services := activeManagedServices(cfg)
	if len(services) == 0 {
		return
	}
	buf.WriteString("  # Dynamic destination sets populated by dnsmasq from DNS answers.\n")
	for _, service := range services {
		buf.WriteString(fmt.Sprintf("  set svc_%s { type ipv4_addr; flags timeout; timeout 4h; }\n", service))
	}
	buf.WriteString("\n  chain device_profiles {\n")
	for _, profile := range cfg.AdGuard.DeviceProfiles {
		if !profile.Enabled {
			continue
		}
		buf.WriteString(fmt.Sprintf("    # Device profile: %s\n", profile.Name))
		dayWindows := effectiveDayWindows(profile.Schedule)
		for _, ip := range profile.IPAddresses {
			// Managed devices must use the router resolver. Direct DNS and DoT
			// would otherwise bypass the DNS-derived destination sets.
			buf.WriteString(fmt.Sprintf("    ip saddr %s udp dport { 53, 853 } drop\n", ip))
			buf.WriteString(fmt.Sprintf("    ip saddr %s tcp dport { 53, 853 } drop\n", ip))
			for _, service := range profile.Services {
				if _, supported := ServiceDomains[service]; !supported {
					continue
				}
				for index, day := range orderedScheduleDays {
					// nft's weekday ABI is Sunday=0 through Saturday=6. Numeric
					// values avoid the case-sensitive symbolic parser on Alpine.
					writeAllowedWindows(buf, ip, service, (index+1)%7, dayWindows[day])
				}
				buf.WriteString(fmt.Sprintf("    ip saddr %s ip daddr @svc_%s drop\n", ip, service))
			}
		}
	}
	buf.WriteString("    return\n  }\n\n")
}

func writeAllowedWindows(buf *bytes.Buffer, ip, service string, day int, windows []config.AccessWindow) {
	for _, window := range windows {
		end := window.End
		if end == "23:59" {
			// The UI uses this sentinel for an allowed day through midnight.
			end = "23:59:59"
		}
		buf.WriteString(fmt.Sprintf(
			"    ip saddr %s ip daddr @svc_%s meta day %d meta hour \"%s\"-\"%s\" return\n",
			ip, service, day, window.Start, end,
		))
	}
}
