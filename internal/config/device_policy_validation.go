package config

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

var (
	policyIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,47}$`)
	timezonePattern = regexp.MustCompile(`^[A-Za-z0-9_+.-]+(?:/[A-Za-z0-9_+.-]+)*$`)
)

var supportedPolicyServices = map[string]struct{}{
	"youtube": {},
	"steam":   {},
}

var supportedScheduleDays = map[string]struct{}{
	"monday": {}, "tuesday": {}, "wednesday": {}, "thursday": {},
	"friday": {}, "saturday": {}, "sunday": {},
}

func networksOverlap(a, b *net.IPNet) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Contains(b.IP) || b.Contains(a.IP)
}

func canonicalMAC(raw string) string {
	mac, err := net.ParseMAC(raw)
	if err != nil || len(mac) != 6 {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	return mac.String()
}

func validateStaticLeases(
	errs *ValidationErrors,
	field string,
	leases []StaticLease,
	network *net.IPNet,
	gateway net.IP,
) map[string]StaticLease {
	byIP := make(map[string]StaticLease)
	seenMAC := make(map[string]struct{})
	for i, lease := range leases {
		prefix := fmt.Sprintf("%s[%d]", field, i)
		mac, err := net.ParseMAC(lease.MAC)
		if err != nil || len(mac) != 6 {
			appendFieldError(errs, prefix+".mac", "must be a valid 48-bit MAC address")
		}
		macKey := canonicalMAC(lease.MAC)
		if _, exists := seenMAC[macKey]; exists {
			appendFieldError(errs, prefix+".mac", "duplicates another static lease")
		}
		seenMAC[macKey] = struct{}{}
		ip := parseIPv4(lease.IPAddress)
		if ip == nil || network == nil || !network.Contains(ip) {
			appendFieldError(errs, prefix+".ip_address", "must be a valid address in the zone subnet")
		} else if (gateway != nil && ip.Equal(gateway)) || isIPv4NetworkOrBroadcast(ip, network) {
			appendFieldError(errs, prefix+".ip_address", "cannot use the zone gateway, network, or broadcast address")
		}
		if _, exists := byIP[lease.IPAddress]; exists {
			appendFieldError(errs, prefix+".ip_address", "duplicates another static lease")
		}
		byIP[lease.IPAddress] = lease
		if lease.Hostname != "" && !hostnamePattern.MatchString(lease.Hostname) {
			appendFieldError(errs, prefix+".hostname", "must be a valid hostname")
		}
	}
	return byIP
}

func validateIoTDHCP(errs *ValidationErrors, cfg *SystemConfig, network *net.IPNet, gateway net.IP) map[string]StaticLease {
	leases := validateStaticLeases(errs, "iot.dhcp.static_leases", cfg.IoT.DHCP.StaticLeases, network, gateway)
	if !cfg.IoT.DHCP.Enabled {
		return leases
	}
	start := parseIPv4(cfg.IoT.DHCP.RangeStart)
	end := parseIPv4(cfg.IoT.DHCP.RangeEnd)
	if start == nil {
		appendFieldError(errs, "iot.dhcp.range_start", "must be a valid IPv4 address")
	}
	if end == nil {
		appendFieldError(errs, "iot.dhcp.range_end", "must be a valid IPv4 address")
	}
	if start != nil && end != nil {
		if compareIPv4(start, end) > 0 {
			appendFieldError(errs, "iot.dhcp.range", "range start must not be after range end")
		}
		if network == nil || !network.Contains(start) || !network.Contains(end) {
			appendFieldError(errs, "iot.dhcp.range", "must be contained in the IoT subnet")
		}
		if gateway != nil && compareIPv4(start, gateway) <= 0 && compareIPv4(gateway, end) <= 0 {
			appendFieldError(errs, "iot.dhcp.range", "cannot contain the IoT gateway address")
		}
		if isIPv4NetworkOrBroadcast(start, network) || isIPv4NetworkOrBroadcast(end, network) {
			appendFieldError(errs, "iot.dhcp.range", "cannot include the network or broadcast address")
		}
	}
	leaseTime, err := time.ParseDuration(cfg.IoT.DHCP.LeaseTime)
	if err != nil || leaseTime < time.Minute || leaseTime > 7*24*time.Hour {
		appendFieldError(errs, "iot.dhcp.lease_time", "must be a duration between 1m and 168h")
	}
	return leases
}

func validateIoTAndPolicies(c *SystemConfig, errs *ValidationErrors) {
	tz := c.EffectiveTimezone()
	if len(tz) > 64 || !timezonePattern.MatchString(tz) || strings.Contains(tz, "..") {
		appendFieldError(errs, "system.timezone", "must be a safe IANA timezone name such as Europe/Belgrade or UTC")
	}

	lanIP := parseIPv4(c.LAN.IPAddress)
	_, lanNetwork, _ := net.ParseCIDR(c.LAN.CIDR)
	lanLeases := validateStaticLeases(errs, "dhcp.static_leases", c.DHCP.StaticLeases, lanNetwork, lanIP)

	var iotIP net.IP
	var iotNetwork *net.IPNet
	iotLeases := map[string]StaticLease{}
	if c.IoT.Enabled {
		if c.IoT.Mode != "dedicated" && c.IoT.Mode != "vlan" {
			appendFieldError(errs, "iot.mode", "must be dedicated or vlan")
		}
		if c.IoT.Mode == "dedicated" {
			if !validInterfaceName(c.IoT.Interface) {
				appendFieldError(errs, "iot.interface", "must be a valid Linux interface name")
			}
			if c.IoT.Interface == "lo" || c.IoT.Interface == c.WAN.Interface || c.IoT.Interface == c.LAN.Interface ||
				c.IoT.Interface == c.WiFi.Interface || c.IoT.Interface == c.WireGuard.Interface ||
				c.IoT.Interface == WiFiBridgeInterface || c.IoT.Interface == IoTVLANInterface {
				appendFieldError(errs, "iot.interface", "must be a dedicated interface not used by WAN, LAN, Wi-Fi, or a project-owned bridge")
			}
		} else {
			if !validInterfaceName(c.IoT.ParentInterface) {
				appendFieldError(errs, "iot.parent_interface", "must be a valid Linux interface name")
			}
			if c.IoT.ParentInterface == "lo" || c.IoT.ParentInterface == c.WAN.Interface ||
				c.IoT.ParentInterface == c.WiFi.Interface || c.IoT.ParentInterface == c.WireGuard.Interface ||
				c.IoT.ParentInterface == IoTVLANInterface {
				appendFieldError(errs, "iot.parent_interface", "must not reuse WAN, Wi-Fi, or the project-owned IoT interface")
			}
			if c.IoT.VLANID < 1 || c.IoT.VLANID > 4094 {
				appendFieldError(errs, "iot.vlan_id", "must be between 1 and 4094")
			}
		}

		parsedIP := parseIPv4(c.IoT.IPAddress)
		cidrIP, network, err := net.ParseCIDR(c.IoT.CIDR)
		if parsedIP == nil {
			appendFieldError(errs, "iot.ip_address", "must be a valid IPv4 address")
		}
		if err != nil || cidrIP.To4() == nil {
			appendFieldError(errs, "iot.cidr", "must be valid IPv4 CIDR notation")
		} else {
			iotIP = cidrIP.To4()
			iotNetwork = network
			if isIPv4NetworkOrBroadcast(iotIP, network) {
				appendFieldError(errs, "iot.cidr", "gateway cannot be the network or broadcast address")
			}
			if parsedIP != nil && !cidrIP.Equal(parsedIP) {
				appendFieldError(errs, "iot.cidr", "address must exactly match iot.ip_address")
			}
			expectedMask := net.IP(network.Mask).String()
			if c.IoT.Netmask != expectedMask {
				appendFieldError(errs, "iot.netmask", "must match the CIDR prefix")
			}
			if networksOverlap(lanNetwork, network) {
				appendFieldError(errs, "iot.cidr", "must not overlap the LAN subnet")
			}
			if c.WireGuard.Address != "" {
				_, wgNetwork, _ := net.ParseCIDR(c.WireGuard.Address)
				if networksOverlap(wgNetwork, network) {
					appendFieldError(errs, "iot.cidr", "must not overlap the WireGuard subnet")
				}
			}
		}
		iotLeases = validateIoTDHCP(errs, c, iotNetwork, iotIP)
		lanMACs := make(map[string]struct{}, len(lanLeases))
		for _, lease := range lanLeases {
			lanMACs[canonicalMAC(lease.MAC)] = struct{}{}
		}
		for _, lease := range iotLeases {
			if _, exists := lanMACs[canonicalMAC(lease.MAC)]; exists {
				appendFieldError(errs, "iot.dhcp.static_leases", "must not reuse a MAC address reserved on the main LAN")
			}
		}
	} else if len(c.IoT.DHCP.StaticLeases) > 0 {
		appendFieldError(errs, "iot.dhcp.static_leases", "cannot contain leases while the IoT zone is disabled")
	}

	profiles := make(map[string]DeviceProfile)
	for i, profile := range c.Policies.Profiles {
		prefix := fmt.Sprintf("device_policies.profiles[%d]", i)
		if !policyIDPattern.MatchString(profile.ID) {
			appendFieldError(errs, prefix+".id", "must contain lowercase letters, numbers, underscore, or hyphen")
		}
		if _, exists := profiles[profile.ID]; exists {
			appendFieldError(errs, prefix+".id", "duplicates another profile")
		}
		profiles[profile.ID] = profile
		if !safeNamePattern.MatchString(profile.Name) || hasUnsafeControl(profile.Name) {
			appendFieldError(errs, prefix+".name", "contains unsupported characters")
		}
		if profile.AccessMode != "allow_all" && profile.AccessMode != "allow_services" {
			appendFieldError(errs, prefix+".access_mode", "must be allow_all or allow_services")
		}
		seenService := make(map[string]struct{})
		for j, service := range profile.AllowedServices {
			if _, ok := supportedPolicyServices[service]; !ok {
				appendFieldError(errs, fmt.Sprintf("%s.allowed_services[%d]", prefix, j), "unsupported service category")
			}
			if _, duplicate := seenService[service]; duplicate {
				appendFieldError(errs, fmt.Sprintf("%s.allowed_services[%d]", prefix, j), "duplicates another service")
			}
			seenService[service] = struct{}{}
		}
		if profile.AccessMode == "allow_services" && len(profile.AllowedServices) == 0 {
			appendFieldError(errs, prefix+".allowed_services", "must include at least one service in allow_services mode")
		}
		if profile.AccessMode == "allow_all" && len(profile.AllowedServices) > 0 {
			appendFieldError(errs, prefix+".allowed_services", "must be empty in allow_all mode")
		}
		if len(profile.Windows) == 0 {
			appendFieldError(errs, prefix+".windows", "must include at least one access window")
		}
		if len(profile.Windows) > 21 {
			appendFieldError(errs, prefix+".windows", "must not contain more than 21 windows")
		}
		for j, window := range profile.Windows {
			windowPrefix := fmt.Sprintf("%s.windows[%d]", prefix, j)
			if len(window.Days) == 0 || len(window.Days) > 7 {
				appendFieldError(errs, windowPrefix+".days", "must contain one to seven weekdays")
			}
			seenDay := make(map[string]struct{})
			for k, day := range window.Days {
				day = strings.ToLower(day)
				if _, ok := supportedScheduleDays[day]; !ok {
					appendFieldError(errs, fmt.Sprintf("%s.days[%d]", windowPrefix, k), "must be a full English weekday name")
				}
				if _, duplicate := seenDay[day]; duplicate {
					appendFieldError(errs, fmt.Sprintf("%s.days[%d]", windowPrefix, k), "duplicates another weekday")
				}
				seenDay[day] = struct{}{}
			}
			if window.AllDay {
				continue
			}
			start, startErr := time.Parse("15:04", window.Start)
			end, endErr := time.Parse("15:04", window.End)
			if startErr != nil {
				appendFieldError(errs, windowPrefix+".start", "must use 24-hour HH:MM format")
			}
			if endErr != nil {
				appendFieldError(errs, windowPrefix+".end", "must use 24-hour HH:MM format")
			}
			if startErr == nil && endErr == nil && !start.Before(end) {
				appendFieldError(errs, windowPrefix, "must end after it starts and cannot cross midnight")
			}
		}
	}

	seenAssignmentID := make(map[string]struct{})
	seenAssignmentIP := make(map[string]struct{})
	seenAssignmentMAC := make(map[string]struct{})
	for i, assignment := range c.Policies.Assignments {
		prefix := fmt.Sprintf("device_policies.assignments[%d]", i)
		if !policyIDPattern.MatchString(assignment.ID) {
			appendFieldError(errs, prefix+".id", "must contain lowercase letters, numbers, underscore, or hyphen")
		}
		if _, exists := seenAssignmentID[assignment.ID]; exists {
			appendFieldError(errs, prefix+".id", "duplicates another assignment")
		}
		seenAssignmentID[assignment.ID] = struct{}{}
		if assignment.Hostname != "" && !hostnamePattern.MatchString(assignment.Hostname) {
			appendFieldError(errs, prefix+".hostname", "must be a valid hostname")
		}
		mac, err := net.ParseMAC(assignment.MAC)
		if err != nil || len(mac) != 6 {
			appendFieldError(errs, prefix+".mac", "must be a valid 48-bit MAC address")
		}
		macKey := canonicalMAC(assignment.MAC)
		if _, exists := seenAssignmentMAC[macKey]; exists {
			appendFieldError(errs, prefix+".mac", "duplicates another assignment")
		}
		seenAssignmentMAC[macKey] = struct{}{}
		if _, exists := seenAssignmentIP[assignment.IPAddress]; exists {
			appendFieldError(errs, prefix+".ip_address", "duplicates another assignment")
		}
		seenAssignmentIP[assignment.IPAddress] = struct{}{}
		profile, exists := profiles[assignment.ProfileID]
		if !exists {
			appendFieldError(errs, prefix+".profile_id", "must reference an existing profile")
		} else if !profile.Enabled && c.Policies.Enabled {
			appendFieldError(errs, prefix+".profile_id", "cannot reference a disabled profile while policies are active")
		}

		var reservation StaticLease
		var reserved bool
		switch assignment.Zone {
		case "lan":
			if !c.DHCP.Enabled {
				appendFieldError(errs, prefix+".zone", "requires LAN DHCP to be enabled")
			}
			ip := parseIPv4(assignment.IPAddress)
			if ip == nil || lanNetwork == nil || !lanNetwork.Contains(ip) || (lanIP != nil && ip.Equal(lanIP)) {
				appendFieldError(errs, prefix+".ip_address", "must be a non-gateway address in the LAN subnet")
			}
			reservation, reserved = lanLeases[assignment.IPAddress]
		case "iot":
			if !c.IoT.Enabled {
				appendFieldError(errs, prefix+".zone", "requires the IoT zone to be enabled")
			} else if !c.IoT.DHCP.Enabled {
				appendFieldError(errs, prefix+".zone", "requires IoT DHCP to be enabled")
			}
			ip := parseIPv4(assignment.IPAddress)
			if ip == nil || iotNetwork == nil || !iotNetwork.Contains(ip) || (iotIP != nil && ip.Equal(iotIP)) {
				appendFieldError(errs, prefix+".ip_address", "must be a non-gateway address in the IoT subnet")
			}
			reservation, reserved = iotLeases[assignment.IPAddress]
		default:
			appendFieldError(errs, prefix+".zone", "must be lan or iot")
		}
		if !reserved {
			appendFieldError(errs, prefix, "must match a static DHCP reservation in the selected zone")
		} else if !strings.EqualFold(reservation.MAC, assignment.MAC) {
			appendFieldError(errs, prefix+".mac", "must match the MAC address of the static DHCP reservation")
		}
	}
}
