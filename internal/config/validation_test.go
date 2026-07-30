package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidationDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	err := cfg.Validate()
	if err != nil {
		t.Fatalf("DefaultConfig should be valid, got: %v", err)
	}
}

func TestValidationRejectsGeneratedConfigInjection(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*SystemConfig)
	}{
		{"interface newline", func(cfg *SystemConfig) { cfg.WAN.Interface = "eth0\nflush ruleset" }},
		{"hostname directive", func(cfg *SystemConfig) { cfg.System.Hostname = "router\nserver=evil" }},
		{"PPPoE quote", func(cfg *SystemConfig) {
			cfg.WAN.Enabled = true
			cfg.WAN.Username = "user\"\nnoauth"
			cfg.WAN.Password = strings.Repeat("x", 15)
		}},
		{"rule name newline", func(cfg *SystemConfig) {
			cfg.Firewall.CustomRules = []FirewallRule{{
				Name: "allow\naccept", Action: "allow", Direction: "forward",
				Protocol: "tcp", DstPort: 80, Enabled: true,
			}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected unsafe generated-config input to be rejected")
			}
		})
	}
}

func TestValidationInterfaceBoundaryCollision(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WAN.Interface = "eth0"
	cfg.LAN.Interface = "eth0"

	err := cfg.Validate()
	if err == nil {
		t.Fatalf("Expected error when WAN and LAN interfaces are identical, got nil")
	}
}

func TestValidationInvalidIP(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LAN.IPAddress = "999.999.999.999"

	err := cfg.Validate()
	if err == nil {
		t.Fatalf("Expected error for invalid LAN IP address, got nil")
	}
}

func TestValidationPortForwardRange(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Firewall.PortForwards = []PortForwardRule{
		{
			ID:           "pf1",
			Name:         "Invalid Port Test",
			Protocol:     "tcp",
			ExternalPort: 70000,
			InternalIP:   "192.168.1.50",
			InternalPort: 80,
			Enabled:      true,
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatalf("Expected error for external port > 65535, got nil")
	}
}

func TestValidationRejectsEveryEnabledWANPortForward(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "isp-user"
	cfg.WAN.Password = "isp-password"
	cfg.Firewall.PortForwards = []PortForwardRule{{
		ID: "pf1", Name: "Forbidden Web Server", Protocol: "tcp",
		ExternalPort: 443, InternalIP: "192.168.1.50", InternalPort: 443, Enabled: true,
	}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "WireGuard is the only allowed external entry point") {
		t.Fatalf("expected WireGuard-only WAN ingress rejection, got %v", err)
	}
}

func TestValidationRejectsUnavailableDashboardFeatures(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*SystemConfig)
	}{
		{"doh", func(cfg *SystemConfig) { cfg.DHCP.DNSEnabled = true }},
		{"per-device DNS", func(cfg *SystemConfig) {
			cfg.AdGuard.FilterDevices = []FilterDeviceRule{{
				Hostname: "child", IPAddress: "192.168.1.50", Enabled: true,
			}}
		}},
		{"external blocklist", func(cfg *SystemConfig) {
			cfg.AdGuard.BlocklistURL = "https://example.com/hosts"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("unavailable feature was accepted")
			}
		})
	}
}

func TestValidationAcceptsSupportedWiFiAndCloudflareDDNS(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "isp-user"
	cfg.WAN.Password = "isp-password"
	cfg.WiFi.Enabled = true
	cfg.WiFi.Interface = "wlan0"
	cfg.WiFi.SSID = "Office"
	cfg.WiFi.Passphrase = "secure-wifi-passphrase"
	cfg.WiFi.Band = "5ghz"
	cfg.WiFi.Channel = 36
	cfg.Cloudflare.DDNSEnabled = true
	cfg.Cloudflare.Domain = "router.example.com"
	cfg.Cloudflare.ZoneName = "example.com"
	cfg.Cloudflare.APIToken = "abcdefghijklmnopqrstuvwxyz_123456"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("supported Wi-Fi and Cloudflare DDNS config was rejected: %v", err)
	}
}

func TestValidationStillRejectsCloudflareTunnel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Cloudflare.TunnelEnabled = true
	cfg.Cloudflare.TunnelToken = "abcdefghijklmnopqrstuvwxyz_123456"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "WireGuard is the only allowed remote-entry path") {
		t.Fatalf("expected Cloudflare Tunnel rejection, got %v", err)
	}
}

func TestValidationBoundsQoSRate(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QoS.Enabled = true
	cfg.QoS.DownloadLimitMbps = 100001
	if err := cfg.Validate(); err == nil {
		t.Fatal("unbounded QoS rate was accepted")
	}
}

func TestValidationRejectsWireGuardRouteWiderThanTunnelSubnet(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "isp-user"
	cfg.WAN.Password = "isp-password"
	cfg.WireGuard.Enabled = true
	cfg.WireGuard.PrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	cfg.WireGuard.Peers = []WireGuardPeer{{
		ID:         "peer-1",
		Name:       "peer-one",
		PublicKey:  "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=",
		AllowedIPs: []string{"10.8.0.0/16"},
		Enabled:    true,
	}}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "inside the WireGuard subnet") {
		t.Fatalf("expected wider WireGuard route rejection, got %v", err)
	}
}

func TestValidationAcceptsMostFragmentedHourlyKidsSchedule(t *testing.T) {
	windows := make([]AccessWindow, 0, 12)
	for hour := 0; hour < 24; hour += 2 {
		windows = append(windows, AccessWindow{
			Start: fmt.Sprintf("%02d:00", hour),
			End:   fmt.Sprintf("%02d:00", hour+1),
		})
	}
	cfg := DefaultConfig()
	cfg.AdGuard.Enabled = true
	cfg.AdGuard.DeviceProfiles = []DeviceProfile{{
		ID:          "kids-fragmented",
		Name:        "Kids",
		IPAddresses: []string{"192.168.1.50"},
		Services:    []string{"youtube"},
		Enabled:     true,
		Schedule: WeeklyAccessSchedule{DayWindows: map[string][]AccessWindow{
			"monday": windows,
		}},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("12 non-overlapping hourly windows should be accepted: %v", err)
	}
}

func TestValidationRejectsMoreThanHourlyGridCanProduce(t *testing.T) {
	windows := make([]AccessWindow, 0, 13)
	for minute := 0; minute < 26; minute += 2 {
		windows = append(windows, AccessWindow{
			Start: fmt.Sprintf("00:%02d", minute),
			End:   fmt.Sprintf("00:%02d", minute+1),
		})
	}
	var errs ValidationErrors
	validateWindows(&errs, "schedule.day_windows.monday", windows)
	if len(errs) == 0 || !strings.Contains(errs.Error(), "twelve windows") {
		t.Fatalf("expected a 13-window validation error, got %v", errs)
	}
}
