package config

import (
	"strings"
	"testing"
)

func configuredKidsPolicy() SystemConfig {
	cfg := DefaultConfig()
	cfg.System.Timezone = "Europe/Belgrade"
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "test-user"
	cfg.WAN.Password = "test-password"
	cfg.DHCP.StaticLeases = []StaticLease{{
		ID:        "kid-tablet",
		Hostname:  "kid-tablet",
		MAC:       "02:00:00:00:00:10",
		IPAddress: "192.168.1.50",
	}}
	cfg.Policies = DevicePolicyConfig{
		Enabled: true,
		Profiles: []DeviceProfile{{
			ID:              "kids-evening",
			Name:            "Kids evening",
			Enabled:         true,
			AccessMode:      "allow_services",
			AllowedServices: []string{"youtube", "steam"},
			Windows: []AccessWindow{
				{Days: []string{"monday", "tuesday", "wednesday", "thursday", "friday"}, Start: "19:00", End: "23:59"},
				{Days: []string{"saturday", "sunday"}, AllDay: true},
			},
		}},
		Assignments: []DeviceAssignment{{
			ID:        "kid-tablet",
			Hostname:  "kid-tablet",
			MAC:       "02:00:00:00:00:10",
			IPAddress: "192.168.1.50",
			Zone:      "lan",
			ProfileID: "kids-evening",
		}},
	}
	return cfg
}

func TestDefaultIoTAndDevicePoliciesStayDisabled(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.IoT.Enabled {
		t.Fatal("IoT isolation zone must be disabled by default")
	}
	if cfg.Policies.Enabled {
		t.Fatal("device schedules must be disabled by default")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("secure default configuration must remain valid: %v", err)
	}
}

func TestKidsEveningTemplateIsValid(t *testing.T) {
	cfg := configuredKidsPolicy()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("kids evening template should be valid: %v", err)
	}
}

func TestDevicePolicyRequiresMatchingStaticReservation(t *testing.T) {
	cfg := configuredKidsPolicy()
	cfg.DHCP.StaticLeases = nil
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "must match a static DHCP reservation") {
		t.Fatalf("missing static reservation was accepted: %v", err)
	}

	cfg = configuredKidsPolicy()
	cfg.Policies.Assignments[0].MAC = "02:00:00:00:00:11"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "must match the MAC address") {
		t.Fatalf("mismatched reservation MAC was accepted: %v", err)
	}
}

func TestIoTZoneRejectsOverlappingOrReusedInterfaces(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IoT.Enabled = true
	cfg.IoT.Interface = cfg.LAN.Interface
	cfg.IoT.CIDR = "192.168.1.2/24"
	cfg.IoT.IPAddress = "192.168.1.2"
	cfg.IoT.Netmask = "255.255.255.0"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "dedicated interface") || !strings.Contains(err.Error(), "must not overlap") {
		t.Fatalf("unsafe IoT topology was accepted: %v", err)
	}
}

func TestIoTAssignmentUsesIoTReservation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.System.Timezone = "Europe/Belgrade"
	cfg.IoT.Enabled = true
	cfg.IoT.DHCP.StaticLeases = []StaticLease{{
		ID:        "camera",
		Hostname:  "camera",
		MAC:       "02:00:00:00:30:10",
		IPAddress: "192.168.30.50",
	}}
	cfg.Policies = DevicePolicyConfig{
		Enabled: true,
		Profiles: []DeviceProfile{{
			ID:         "iot-online",
			Name:       "IoT online",
			Enabled:    true,
			AccessMode: "allow_all",
			Windows:    []AccessWindow{{Days: []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}, AllDay: true}},
		}},
		Assignments: []DeviceAssignment{{
			ID: "camera", Hostname: "camera", MAC: "02:00:00:00:30:10",
			IPAddress: "192.168.30.50", Zone: "iot", ProfileID: "iot-online",
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid IoT reservation and assignment rejected: %v", err)
	}
}

func TestSchedulesRejectOvernightWindowsAndUnsafeTimezone(t *testing.T) {
	cfg := configuredKidsPolicy()
	cfg.System.Timezone = "../../etc/passwd"
	cfg.Policies.Profiles[0].Windows[0].Start = "22:00"
	cfg.Policies.Profiles[0].Windows[0].End = "07:00"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "system.timezone") || !strings.Contains(err.Error(), "cannot cross midnight") {
		t.Fatalf("unsafe timezone or overnight window was accepted: %v", err)
	}
}

func TestPoliciesRequireEnabledDHCPInSelectedZone(t *testing.T) {
	cfg := configuredKidsPolicy()
	cfg.DHCP.Enabled = false
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "requires LAN DHCP") {
		t.Fatalf("LAN policy without DHCP was accepted: %v", err)
	}

	cfg = DefaultConfig()
	cfg.IoT.Enabled = true
	cfg.IoT.DHCP.Enabled = false
	cfg.IoT.DHCP.StaticLeases = []StaticLease{{
		ID: "camera", Hostname: "camera", MAC: "02:00:00:00:30:10", IPAddress: "192.168.30.50",
	}}
	cfg.Policies = DevicePolicyConfig{
		Enabled: true,
		Profiles: []DeviceProfile{{
			ID: "iot-online", Name: "IoT online", Enabled: true, AccessMode: "allow_all",
			Windows: []AccessWindow{{Days: []string{"monday"}, AllDay: true}},
		}},
		Assignments: []DeviceAssignment{{
			ID: "camera", Hostname: "camera", MAC: "02:00:00:00:30:10",
			IPAddress: "192.168.30.50", Zone: "iot", ProfileID: "iot-online",
		}},
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "requires IoT DHCP") {
		t.Fatalf("IoT policy without DHCP was accepted: %v", err)
	}
}

func TestIoTLeaseCannotReuseMainLANMAC(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DHCP.StaticLeases = []StaticLease{{
		ID: "lan-device", Hostname: "lan-device", MAC: "02:00:00:00:00:10", IPAddress: "192.168.1.50",
	}}
	cfg.IoT.Enabled = true
	cfg.IoT.DHCP.StaticLeases = []StaticLease{{
		ID: "iot-device", Hostname: "iot-device", MAC: "02:00:00:00:00:10", IPAddress: "192.168.30.50",
	}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "must not reuse a MAC") {
		t.Fatalf("same MAC reservation in LAN and IoT was accepted: %v", err)
	}
}

func TestCanonicalMACComparisonRejectsAlternateFormattingAcrossZones(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DHCP.StaticLeases = []StaticLease{{
		ID: "lan-device", Hostname: "lan-device", MAC: "02:00:00:00:00:10", IPAddress: "192.168.1.50",
	}}
	cfg.IoT.Enabled = true
	cfg.IoT.DHCP.StaticLeases = []StaticLease{{
		ID: "iot-device", Hostname: "iot-device", MAC: "02-00-00-00-00-10", IPAddress: "192.168.30.50",
	}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "must not reuse a MAC") {
		t.Fatalf("alternate formatting bypassed cross-zone MAC uniqueness: %v", err)
	}
}

func TestActiveProfileRequiresAWindowAndCanonicalModeFields(t *testing.T) {
	cfg := configuredKidsPolicy()
	cfg.Policies.Profiles[0].Windows = nil
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "must include at least one access window") {
		t.Fatalf("profile without a window was accepted: %v", err)
	}

	cfg = configuredKidsPolicy()
	cfg.Policies.Profiles[0].AccessMode = "allow_all"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "must be empty in allow_all mode") {
		t.Fatalf("allow_all profile retained service categories: %v", err)
	}
}
