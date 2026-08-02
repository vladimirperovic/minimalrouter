package services

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestGenerateCloudflareDDNSUsesInadynWithoutShell(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Cloudflare.DDNSEnabled = true
	cfg.Cloudflare.DDNSProvider = "cloudflare"
	cfg.Cloudflare.Domain = "router.example.com"
	cfg.Cloudflare.ZoneName = "example.com"
	cfg.Cloudflare.APIToken = "abcdefghijklmnopqrstuvwxyz_123456"

	out, err := GenerateCloudflareDDNS(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"provider cloudflare.com:1",
		`username = "example.com"`,
		`hostname = "router.example.com"`,
		"allow-ipv6 = false",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("missing %q in generated config:\n%s", expected, out)
		}
	}
	for _, forbidden := range []string{"#!/bin/sh", "curl ", "$API_TOKEN", "system("} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("generated DDNS configuration contains executable shell content %q", forbidden)
		}
	}
}

func TestGenerateDynamicDNSUsesNativeNoIPProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Cloudflare.DDNSEnabled = true
	cfg.Cloudflare.DDNSProvider = "noip"
	cfg.Cloudflare.DDNSUser = "ddns-key-user@example.com"
	cfg.Cloudflare.APIToken = `S3cret!with:special#chars`
	cfg.Cloudflare.Domain = "all.ddnskey.com"

	out, err := GenerateDynamicDNS(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"provider no-ip.com:1",
		`username = "ddns-key-user@example.com"`,
		`password = "S3cret!with:special#chars"`,
		`hostname = "all.ddnskey.com"`,
		"period = 300",
		"forced-update = 2592000",
		"secure-ssl = true",
		"checkip-server = api.ipify.org",
		"checkip-path = /",
		"checkip-ssl = true",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("missing %q in generated No-IP config:\n%s", expected, out)
		}
	}
	for _, forbidden := range []string{"#!/bin/sh", "curl ", "system("} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("generated No-IP configuration contains executable shell content %q", forbidden)
		}
	}
}

func TestGenerateDynamicDNSLegacyEmptyProviderMeansCloudflare(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Cloudflare.DDNSEnabled = true
	cfg.Cloudflare.DDNSProvider = ""
	cfg.Cloudflare.Domain = "router.example.com"
	cfg.Cloudflare.ZoneName = "example.com"
	cfg.Cloudflare.APIToken = "abcdefghijklmnopqrstuvwxyz_123456"

	out, err := GenerateDynamicDNS(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "provider cloudflare.com:1") {
		t.Fatalf("legacy provider did not remain Cloudflare:\n%s", out)
	}
}

func TestGenerateDynamicDNSRejectsUnknownProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Cloudflare.DDNSEnabled = true
	cfg.Cloudflare.DDNSProvider = "example-provider"
	if _, err := GenerateDynamicDNS(&cfg); err == nil {
		t.Fatal("unsupported DDNS provider was accepted")
	}
}
