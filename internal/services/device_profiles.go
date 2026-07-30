package services

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

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
		for _, ip := range profile.IPAddresses {
			// Managed devices must use the router resolver. Direct DNS and DoT
			// would otherwise bypass the DNS-derived destination sets.
			buf.WriteString(fmt.Sprintf("    ip saddr %s udp dport { 53, 853 } drop\n", ip))
			buf.WriteString(fmt.Sprintf("    ip saddr %s tcp dport { 53, 853 } drop\n", ip))
			for _, service := range profile.Services {
				if _, supported := ServiceDomains[service]; !supported {
					continue
				}
				writeAllowedWindows(buf, ip, service, "{ monday, tuesday, wednesday, thursday, friday }", profile.Schedule.WeekdayWindows)
				switch profile.Schedule.WeekendMode {
				case "all_day":
					buf.WriteString(fmt.Sprintf("    ip saddr %s ip daddr @svc_%s meta day { saturday, sunday } return\n", ip, service))
				case "same_as_weekdays":
					writeAllowedWindows(buf, ip, service, "{ saturday, sunday }", profile.Schedule.WeekdayWindows)
				case "custom":
					writeAllowedWindows(buf, ip, service, "{ saturday, sunday }", profile.Schedule.WeekendWindows)
				}
				buf.WriteString(fmt.Sprintf("    ip saddr %s ip daddr @svc_%s drop\n", ip, service))
			}
		}
	}
	buf.WriteString("    return\n  }\n\n")
}

func writeAllowedWindows(buf *bytes.Buffer, ip, service, days string, windows []config.AccessWindow) {
	for _, window := range windows {
		buf.WriteString(fmt.Sprintf(
			"    ip saddr %s ip daddr @svc_%s meta day %s meta hour \"%s\"-\"%s\" return\n",
			ip, service, days, window.Start, window.End,
		))
	}
}
