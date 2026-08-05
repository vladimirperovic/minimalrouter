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
	cfg.Cloudflare.DDNSProvider = "cloudflare"
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

func TestValidationRejectsWireGuardPeerHostnameEndpoint(t *testing.T) {
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
		AllowedIPs: []string{"10.8.0.2/32"},
		Endpoint:   "peer.example.com:51820",
		Enabled:    true,
	}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("hostname peer endpoint must be rejected")
	}
	cfg.WireGuard.Peers[0].Endpoint = "203.0.113.7:51820"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("static peer endpoint must validate: %v", err)
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

func TestValidateDNSRecords(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DNS.Records = []DNSRecord{{Name: "immich.home.arpa", IP: "10.20.30.10"}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid DNS record rejected: %v", err)
	}
	cfg.DNS.Records = []DNSRecord{{Name: "immich.local", IP: "10.20.30.10"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal(".local mDNS namespace record must be rejected")
	}
	cfg.DNS.Records = []DNSRecord{{Name: "immich.home.arpa", IP: "10.20.30.10"}}
	cfg.DNS.Records = append(cfg.DNS.Records, DNSRecord{Name: "bad name!", IP: "10.20.30.10"})
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid hostname accepted")
	}
	cfg.DNS.Records = []DNSRecord{{Name: "ok.home.arpa", IP: "not-an-ip"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("invalid IP accepted")
	}
	cfg.DNS.Records = []DNSRecord{
		{Name: "nas.home.arpa", IP: "10.20.30.10"},
		{Name: "NAS.home.arpa", IP: "10.20.30.11"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("case-insensitive duplicate hostnames must be rejected")
	}
	cfg.DNS.Records = []DNSRecord{
		{Name: "a.home.arpa", IP: "10.20.30.10"},
		{Name: "b.home.arpa", IP: "10.20.30.10"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("duplicate record addresses must be rejected")
	}
	cfg.DNS.Records = []DNSRecord{{Name: "nas.home.arpa", IP: "10.20.30.10"}}
	cfg.DHCP.Enabled = true
	cfg.System.Domain = "home.arpa"
	cfg.DHCP.StaticLeases = []StaticLease{{ID: "l1", Hostname: "NAS", MAC: "aa:bb:cc:dd:ee:ff", IPAddress: "192.168.1.42"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("DNS record colliding with a DHCP static lease hostname must be rejected")
	}
}

func TestValidateWGClient(t *testing.T) {
	validKey := "WXK/gT9H1IPzj59FYyi7AERtHnpOqjR9nlUBFzYXjUU="
	cfg := DefaultConfig()
	cfg.WGClient = WGClientConfig{
		Enabled:             true,
		Interface:           "wg1",
		PrivateKey:          validKey,
		Address:             "10.7.0.2/32",
		PublicKey:           "DTSyebsPi8mscQzOPRpiarNste8XHvViiVVNpnZQ7AY=",
		Endpoint:            "office.example.com:51820",
		AllowedIPs:          []string{"10.8.0.0/24"},
		PersistentKeepalive: 25,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid wg_client rejected: %v", err)
	}

	cfg.WGClient.PrivateKey = "not-a-key"
	if err := cfg.Validate(); err == nil {
		t.Error("invalid private key must be rejected")
	}

	cfg.WGClient.PrivateKey = validKey
	cfg.WGClient.Interface = "wg0"
	if err := cfg.Validate(); err == nil {
		t.Error("non-wg1 interface must be rejected")
	}
	cfg.WGClient.Interface = "wg1"

	cfg.WGClient.AllowedIPs = []string{"192.168.1.0/24"}
	if err := cfg.Validate(); err == nil {
		t.Error("allowed network overlapping the LAN must be rejected")
	}
	cfg.WGClient.AllowedIPs = []string{"10.8.0.0/24"}

	cfg.WGClient.AllowedIPs = []string{"0.0.0.0/0"}
	if err := cfg.Validate(); err == nil {
		t.Error("default-route allowed network must be rejected")
	}
	cfg.WGClient.AllowedIPs = []string{"128.0.0.0/1"}
	if err := cfg.Validate(); err == nil {
		t.Error("/1 split-default allowed network must be rejected")
	}
	cfg.WGClient.AllowedIPs = []string{"10.8.0.0/24"}

	cfg.WGClient.Address = "192.168.1.50/24"
	if err := cfg.Validate(); err == nil {
		t.Error("address overlapping the LAN must be rejected")
	}
	cfg.WGClient.Address = "10.7.0.2/32"

	cfg.WGClient.Endpoint = "office.example.com"
	if err := cfg.Validate(); err == nil {
		t.Error("endpoint without port must be rejected")
	}
	cfg.WGClient.Endpoint = "office.example.com:51820"

	cfg.WGClient.Endpoint = "10.9.0.5:51820"
	cfg.WGClient.AllowedIPs = []string{"10.9.0.0/24"}
	if err := cfg.Validate(); err == nil {
		t.Error("allowed network capturing the peer endpoint must be rejected")
	}
	cfg.WGClient.Endpoint = "office.example.com:51820"
	cfg.WGClient.AllowedIPs = []string{"10.8.0.0/24"}

	cfg.WGClient.AllowedIPs = []string{"1.0.0.0/8"}
	if err := cfg.Validate(); err == nil {
		t.Error("allowed network capturing a configured DNS upstream must be rejected")
	}
	cfg.WGClient.AllowedIPs = []string{"10.8.0.0/24"}

	cfg.WGClient.Enabled = false
	cfg.WGClient.AllowedIPs = nil
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled wg_client must validate: %v", err)
	}
}

func TestValidateExtraLANAllowFromSubnets(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "test"
	cfg.WAN.Password = "long-enough-test-password"
	cfg.WireGuard.Enabled = true
	cfg.WireGuard.PrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	cfg.WireGuard.Address = "10.6.0.1/24"
	cfg.Firewall.ExtraLANs = []ExtraLANConfig{{
		ID: "x", Name: "X", Interface: "eth2", CIDR: "10.20.30.0/24", RouterAddress: "10.20.30.1/24",
		DstIP: "10.20.30.10", DstPort: 2283, AllowFrom: []string{"192.168.1.20/32", "10.6.0.5/32"}, Enabled: true,
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("trusted-device /32 sources must validate: %v", err)
	}

	cfg.Firewall.ExtraLANs[0].AllowFrom = []string{"10.6.0.0/16"}
	if err := cfg.Validate(); err == nil {
		t.Error("subnet broader than the WireGuard zone must be rejected")
	}

	cfg.Firewall.ExtraLANs[0].AllowFrom = []string{"10.99.0.0/24"}
	if err := cfg.Validate(); err == nil {
		t.Error("source outside LAN and WireGuard zones must be rejected")
	}
}
