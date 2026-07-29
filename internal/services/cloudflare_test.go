package services

import (
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestGenerateCloudflareDDNSUsesInadynWithoutShell(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Cloudflare.DDNSEnabled = true
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
