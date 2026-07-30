package services

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// PolicyServiceDomains are intentionally small, reviewed domain groups used by
// service-only access windows. Domain/IP classification is best effort: CDNs and
// encrypted application protocols can change, so this is not a parental-control
// guarantee or content-inspection system.
var PolicyServiceDomains = map[string][]string{
	"youtube": {
		"youtube.com",
		"youtu.be",
		"googlevideo.com",
		"ytimg.com",
		"youtube-nocookie.com",
		"youtube.googleapis.com",
		"youtubei.googleapis.com",
	},
	"steam": {
		"steampowered.com",
		"steamcommunity.com",
		"steamstatic.com",
		"steamcontent.com",
		"steamserver.net",
		"steamusercontent.com",
		"steam-chat.com",
		"steamgames.com",
		"steamcdn-a.akamaihd.net",
	},
}

func policySetName(service string) string {
	switch service {
	case "youtube":
		return "svc_youtube"
	case "steam":
		return "svc_steam"
	default:
		return ""
	}
}

// ActivePolicyServices returns sorted, unique service groups referenced by an
// enabled profile while device policies are active.
func ActivePolicyServices(cfg *config.SystemConfig) []string {
	if cfg == nil || !cfg.Policies.Enabled {
		return nil
	}
	seen := make(map[string]struct{})
	for _, profile := range cfg.Policies.Profiles {
		if !profile.Enabled || profile.AccessMode != "allow_services" {
			continue
		}
		for _, service := range profile.AllowedServices {
			if policySetName(service) != "" {
				seen[service] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for service := range seen {
		result = append(result, service)
	}
	sort.Strings(result)
	return result
}

// GeneratePolicyDNSConfig teaches dnsmasq to populate project-owned nftables
// sets with IPv4 addresses resolved for the configured service groups.
func GeneratePolicyDNSConfig(cfg *config.SystemConfig) string {
	var buf bytes.Buffer
	services := ActivePolicyServices(cfg)
	if len(services) == 0 {
		return ""
	}
	buf.WriteString("\n# Device policy service groups (dnsmasq -> nftables sets)\n")
	for _, service := range services {
		domains := append([]string(nil), PolicyServiceDomains[service]...)
		sort.Strings(domains)
		buf.WriteString(fmt.Sprintf("nftset=/%s/4#inet#minimalrouter#%s\n",
			strings.Join(domains, "/"), policySetName(service)))
	}
	return buf.String()
}

var nftDayNames = map[string]string{
	"monday": "Monday", "tuesday": "Tuesday", "wednesday": "Wednesday",
	"thursday": "Thursday", "friday": "Friday", "saturday": "Saturday",
	"sunday": "Sunday",
}

func writePolicySets(buf *bytes.Buffer, cfg *config.SystemConfig) {
	for _, service := range ActivePolicyServices(cfg) {
		setName := policySetName(service)
		if setName == "" {
			continue
		}
		buf.WriteString(fmt.Sprintf("  set %s {\n", setName))
		buf.WriteString("    type ipv4_addr\n")
		buf.WriteString("    flags timeout\n")
		buf.WriteString("    timeout 6h\n")
		buf.WriteString("  }\n\n")
	}
}

func scheduleMatch(window config.AccessWindow) string {
	var parts []string
	if len(window.Days) > 0 && len(window.Days) < 7 {
		days := make([]string, 0, len(window.Days))
		for _, day := range window.Days {
			if name := nftDayNames[strings.ToLower(day)]; name != "" {
				days = append(days, fmt.Sprintf("%q", name))
			}
		}
		parts = append(parts, "meta day { "+strings.Join(days, ", ")+" }")
	}
	if !window.AllDay {
		start := window.Start + ":00"
		end := window.End + ":59"
		parts = append(parts, fmt.Sprintf("meta hour %q-%q", start, end))
	}
	return strings.Join(parts, " ")
}

func zoneInterface(cfg *config.SystemConfig, zone string) string {
	if zone == "iot" {
		return cfg.RuntimeIoTInterface()
	}
	return cfg.RuntimeLANInterface()
}

// writeDevicePolicyRules emits source-device rules before the generic
// established/related accept. This makes the end of an access window effective
// for existing client-originated flows instead of only blocking new sessions.
func writeDevicePolicyRules(buf *bytes.Buffer, cfg *config.SystemConfig) {
	if !cfg.Policies.Enabled || len(cfg.Policies.Assignments) == 0 {
		return
	}
	profiles := make(map[string]config.DeviceProfile, len(cfg.Policies.Profiles))
	for _, profile := range cfg.Policies.Profiles {
		profiles[profile.ID] = profile
	}
	buf.WriteString("    # Time-bounded device policies (local appliance timezone)\n")
	for _, assignment := range cfg.Policies.Assignments {
		profile, ok := profiles[assignment.ProfileID]
		if !ok || !profile.Enabled {
			continue
		}
		iface := zoneInterface(cfg, assignment.Zone)
		base := fmt.Sprintf("iifname %q ip saddr %s", iface, assignment.IPAddress)
		buf.WriteString(fmt.Sprintf("    # Device policy: %s -> %s\n", assignment.Hostname, profile.Name))
		for _, window := range profile.Windows {
			match := scheduleMatch(window)
			if match != "" {
				match = " " + match
			}
			switch profile.AccessMode {
			case "allow_all":
				if cfg.WAN.Enabled {
					buf.WriteString(fmt.Sprintf("    %s%s oifname %q accept\n", base, match, cfg.WAN.Interface))
					buf.WriteString(fmt.Sprintf("    %s%s oifname \"ppp*\" accept\n", base, match))
				}
			case "allow_services":
				if cfg.WAN.Enabled {
					for _, service := range profile.AllowedServices {
						setName := policySetName(service)
						if setName == "" {
							continue
						}
						buf.WriteString(fmt.Sprintf("    %s%s ip daddr @%s oifname %q accept\n", base, match, setName, cfg.WAN.Interface))
						buf.WriteString(fmt.Sprintf("    %s%s ip daddr @%s oifname \"ppp*\" accept\n", base, match, setName))
					}
				}
			}
		}
		buf.WriteString(fmt.Sprintf("    %s drop\n\n", base))
	}
}
