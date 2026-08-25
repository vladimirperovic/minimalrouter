package config

import "testing"

// dhcp.lease_time goes through time.ParseDuration, which has no day unit. The
// dashboard hint used to offer "7d", so the appliance refused a value its own
// UI recommended. This pins the units the dashboard is allowed to suggest.
func TestLeaseTimeAcceptsOnlyMinutesAndHours(t *testing.T) {
	for _, accepted := range []string{"1m", "30m", "12h", "24h", "168h"} {
		cfg := DefaultConfig()
		cfg.DHCP.LeaseTime = accepted
		if err := cfg.Validate(); err != nil {
			t.Errorf("lease_time %q must be accepted: %v", accepted, err)
		}
	}
	for _, rejected := range []string{"7d", "30s", "0m", "169h", "", "12"} {
		cfg := DefaultConfig()
		cfg.DHCP.LeaseTime = rejected
		if err := cfg.Validate(); err == nil {
			t.Errorf("lease_time %q must be rejected", rejected)
		}
	}
}

// The dashboard renders a fixed channel list per band; these are the values it
// is allowed to offer.
func TestWiFiChannelsOfferedByTheDashboardAreAccepted(t *testing.T) {
	base := func() SystemConfig {
		cfg := DefaultConfig()
		cfg.WiFi.Enabled = true
		cfg.WiFi.Interface = "wlan0"
		cfg.WiFi.SSID = "home"
		cfg.WiFi.Passphrase = "correcthorsebattery"
		return cfg
	}
	for _, channel := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11} {
		cfg := base()
		cfg.WiFi.Band = "2.4ghz"
		cfg.WiFi.Channel = channel
		if err := cfg.Validate(); err != nil {
			t.Errorf("2.4 GHz channel %d must be accepted: %v", channel, err)
		}
	}
	for _, channel := range []int{36, 40, 44, 48} {
		cfg := base()
		cfg.WiFi.Band = "5ghz"
		cfg.WiFi.Channel = channel
		if err := cfg.Validate(); err != nil {
			t.Errorf("5 GHz channel %d must be accepted: %v", channel, err)
		}
	}
}
