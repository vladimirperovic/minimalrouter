package config

import "testing"

func TestOptionalRemoteAndWirelessFeaturesDefaultOff(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Cloudflare.DDNSEnabled {
		t.Fatal("Cloudflare DDNS must be disabled by default")
	}
	if cfg.Cloudflare.TunnelEnabled {
		t.Fatal("Cloudflare Tunnel must be disabled by default")
	}
	if cfg.WiFi.Enabled {
		t.Fatal("Wi-Fi access point must be disabled by default")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("secure default configuration must remain valid: %v", err)
	}
}
