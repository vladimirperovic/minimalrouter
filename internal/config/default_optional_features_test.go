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

// A fresh install must arrive with nothing switched on that needs a secret to
// work. A feature enabled without its credential is not merely useless: it
// fails validation, and before delta validation existed one such feature made
// the whole dashboard read-only.
func TestNothingRequiringASecretIsEnabledByDefault(t *testing.T) {
	cfg := DefaultConfig()

	for _, feature := range []struct {
		name    string
		enabled bool
	}{
		{"PPPoE WAN", cfg.WAN.Enabled},
		{"WireGuard server", cfg.WireGuard.Enabled},
		{"WireGuard client", cfg.WGClient.Enabled},
		{"Cloudflare DDNS", cfg.Cloudflare.DDNSEnabled},
		{"Cloudflare Tunnel", cfg.Cloudflare.TunnelEnabled},
		{"Squid proxy", cfg.SquidProxy.Enabled},
		{"DNS filter", cfg.AdGuard.Enabled},
		{"QoS", cfg.QoS.Enabled},
		{"Wi-Fi access point", cfg.WiFi.Enabled},
		{"Per-device accounting", cfg.Accounting.Enabled},
	} {
		if feature.enabled {
			t.Errorf("%s must be disabled on a fresh install", feature.name)
		}
	}

	if len(cfg.WireGuard.Peers) != 0 {
		t.Error("a fresh install must carry no WireGuard peers")
	}
	if len(cfg.Firewall.PortForwards) != 0 {
		t.Error("a fresh install must carry no port forwards")
	}
	if len(cfg.Firewall.CustomRules) != 0 {
		t.Error("a fresh install must carry no custom firewall rules")
	}
	if len(cfg.DHCP.StaticLeases) != 0 {
		t.Error("a fresh install must carry no DHCP reservations")
	}
}
