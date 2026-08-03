package config

import (
	"net"
	"testing"
)

func TestIsTrustedClientAddress(t *testing.T) {
	cfg := SystemConfig{TrustedNetworks: []string{"192.168.1.0/24", "fd00:1234::/64"}}

	trusted := map[string]bool{
		"192.168.1.2:8443":          true,
		"192.168.1.50:12345":        true,
		"192.168.1.254:8080":        true,
		"192.168.2.10:8443":         false,
		"10.0.0.1:8443":             false,
		"8.8.8.8:8443":              false,
		"127.0.0.1:8443":            true, // loopback always trusted
		"[::1]:8443":                true, // IPv6 loopback always trusted
		"[fd00:1234::5]:8443":       true,
		"[fd00:9999::5]:8443":       false,
		"::ffff:192.168.1.2:8443":   false, // malformed (unbracketed IPv6:port) -> deny
		"[::ffff:192.168.1.2]:8443": true,  // IPv4-mapped IPv6 matches IPv4 network
		"not-an-address":            false, // malformed -> deny
		"":                          false, // empty -> deny
		"192.168.1.5":               true,  // bare IP without port is accepted
	}
	for remote, want := range trusted {
		if got := cfg.IsTrustedClientAddress(remote); got != want {
			t.Errorf("IsTrustedClientAddress(%q) = %v, want %v", remote, got, want)
		}
	}
}

func TestIsTrustedClientAddressEmptyListDeniesEverything(t *testing.T) {
	cfg := SystemConfig{TrustedNetworks: nil}
	if cfg.IsTrustedClientAddress("192.168.1.2:8443") {
		t.Error("empty trusted list must deny LAN clients")
	}
	if !cfg.IsTrustedClientAddress("127.0.0.1:8443") {
		t.Error("loopback must remain trusted even with empty list")
	}
}

func TestParseRemoteIP(t *testing.T) {
	if parseRemoteIP("192.168.1.2:1234") == nil {
		t.Error("host:port must parse")
	}
	if parseRemoteIP("192.168.1.2") == nil {
		t.Error("bare IP must parse")
	}
	if parseRemoteIP("") != nil {
		t.Error("empty must be nil")
	}
	if parseRemoteIP("garbage") != nil {
		t.Error("garbage must be nil")
	}
	if parseRemoteIP("[fd00::1]:1234") == nil {
		t.Error("IPv6 host:port must parse")
	}
	ip := parseRemoteIP("[::ffff:192.168.1.2]:8443")
	if ip == nil || ip.To4() == nil {
		t.Errorf("IPv4-mapped must normalize to IPv4, got %v", ip)
	}
}

func TestValidateTrustedNetworks(t *testing.T) {
	base := DefaultConfig()

	valid := []string{
		"192.168.1.0/24",
		"10.255.255.0/24",
		"fd00:1234::/64",
	}
	for _, netw := range valid {
		cfg := base
		cfg.TrustedNetworks = []string{netw}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate with %q failed: %v", netw, err)
		}
	}

	invalid := []string{
		"0.0.0.0/0",   // wildcard
		"::/0",        // wildcard IPv6
		"192.168.1.5", // not a CIDR
		"not-a-cidr",  // garbage
		"192.168.1.0/33",
		"192.168.1.0/24; rm -rf /", // control/injection characters
	}
	for _, netw := range invalid {
		cfg := base
		cfg.TrustedNetworks = []string{netw}
		if err := cfg.Validate(); err == nil {
			t.Errorf("Validate with %q should fail", netw)
		}
	}

	cfg := base
	cfg.TrustedNetworks = []string{}
	if err := cfg.Validate(); err == nil {
		t.Error("empty trusted_networks must be rejected (lockout protection)")
	}

	cfg = base
	cfg.TrustedNetworks = []string{"192.168.1.0/24", "192.168.1.0/24"}
	if err := cfg.Validate(); err == nil {
		t.Error("duplicate trusted_networks must be rejected")
	}
}

func TestValidateTrustedNetworksAllowsMultipleAndOverlapping(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TrustedNetworks = []string{"192.168.1.0/24", "10.255.255.0/24"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("multiple networks should validate: %v", err)
	}
	// Overlapping networks are permitted (harmless) but must not panic.
	cfg.TrustedNetworks = []string{"192.168.1.0/24", "192.168.1.0/25"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("overlapping networks should validate: %v", err)
	}
}

func TestDefaultConfigTrustedNetworks(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.TrustedNetworks) != 1 || cfg.TrustedNetworks[0] != "192.168.1.0/24" {
		t.Errorf("default trusted_networks = %v, want [192.168.1.0/24]", cfg.TrustedNetworks)
	}
	_, ipNet, err := net.ParseCIDR(cfg.TrustedNetworks[0])
	if err != nil {
		t.Fatal(err)
	}
	if !ipNet.Contains(net.ParseIP("192.168.1.50")) {
		t.Error("default network must contain LAN addresses")
	}
}
