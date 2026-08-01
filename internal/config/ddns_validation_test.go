package config

import (
	"strings"
	"testing"
)

func ddnsValidationConfig() SystemConfig {
	cfg := DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "pppoe-user"
	cfg.WAN.Password = "pppoe-password-for-test"
	cfg.Cloudflare.DDNSEnabled = true
	return cfg
}

func TestValidateNoIPDDNSAcceptsDDNSKeyCredentials(t *testing.T) {
	cfg := ddnsValidationConfig()
	cfg.Cloudflare.DDNSProvider = "noip"
	cfg.Cloudflare.DDNSUser = "ddns-key-user@example.com"
	cfg.Cloudflare.APIToken = `NoIP!key:with#special-chars`
	cfg.Cloudflare.Domain = "all.ddnskey.com"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid No-IP DDNS configuration rejected: %v", err)
	}
}

func TestValidateNoIPDDNSRequiresUsername(t *testing.T) {
	cfg := ddnsValidationConfig()
	cfg.Cloudflare.DDNSProvider = "noip"
	cfg.Cloudflare.APIToken = "test-password"
	cfg.Cloudflare.Domain = "router.example.net"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "cloudflare.ddns_username") {
		t.Fatalf("missing No-IP username was not rejected: %v", err)
	}
}

func TestValidateLegacyDDNSProviderRemainsCloudflare(t *testing.T) {
	cfg := ddnsValidationConfig()
	cfg.Cloudflare.DDNSProvider = ""
	cfg.Cloudflare.Domain = "router.example.com"
	cfg.Cloudflare.ZoneName = "example.com"
	cfg.Cloudflare.APIToken = "abcdefghijklmnopqrstuvwxyz_123456"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("legacy Cloudflare DDNS configuration rejected: %v", err)
	}
}

func TestValidateDDNSRejectsUnknownProvider(t *testing.T) {
	cfg := ddnsValidationConfig()
	cfg.Cloudflare.DDNSProvider = "unsupported"
	cfg.Cloudflare.Domain = "router.example.com"
	cfg.Cloudflare.APIToken = "some-provider-password"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "cloudflare.ddns_provider") {
		t.Fatalf("unsupported DDNS provider was not rejected: %v", err)
	}
}
