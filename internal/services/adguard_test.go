package services

import (
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestParseHostsFile(t *testing.T) {
	data := []byte(`
# This is a comment
127.0.0.1 localhost
0.0.0.0 ads.example.com
0.0.0.0 tracker.example.com
127.0.0.1 localhost.localdomain
0.0.0.0 malware.example.com
`)
	domains := ParseHostsFile(data)
	if len(domains) != 3 {
		t.Errorf("expected 3 domains, got %d: %v", len(domains), domains)
	}
}

func TestBuiltinBlocklist(t *testing.T) {
	domains := BuiltinBlocklist()
	if len(domains) == 0 {
		t.Error("builtin blocklist should not be empty")
	}
}

func TestGenerateAdBlockConf_Disabled(t *testing.T) {
	cfg := &config.SystemConfig{
		AdGuard: config.AdGuardConfig{Enabled: false},
	}
	result, err := GenerateAdBlockConf(cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Error("expected empty result when disabled")
	}
}

func TestGenerateAdBlockConf_WithBuiltin(t *testing.T) {
	cfg := &config.SystemConfig{
		AdGuard: config.AdGuardConfig{
			Enabled:      true,
			FilterDevices: []config.FilterDeviceRule{},
		},
	}
	result, err := GenerateAdBlockConf(cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
	// Should contain address= lines
	if !containsSubstring(result, "address=/") {
		t.Error("expected address= directives in output")
	}
}

func TestGenerateAdBlockConf_WithFilterDevices(t *testing.T) {
	cfg := &config.SystemConfig{
		AdGuard: config.AdGuardConfig{
			Enabled: true,
			FilterDevices: []config.FilterDeviceRule{
				{
					ID:              "1",
					Hostname:        "kids-pc",
					IPAddress:       "192.168.1.50",
					BlockedServices: []string{"youtube", "tiktok"},
					Enabled:         true,
				},
			},
		},
	}
	result, err := GenerateAdBlockConf(cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should contain per-device rules for youtube/tiktok
	if !containsSubstring(result, "youtube.com") {
		t.Error("expected youtube domain in per-device rules")
	}
	if !containsSubstring(result, "tiktok.com") {
		t.Error("expected tiktok domain in per-device rules")
	}
	if !containsSubstring(result, "192.168.1.50") {
		t.Error("expected device IP in per-device rules")
	}
}

func TestParseHostsFile_Empty(t *testing.T) {
	domains := ParseHostsFile([]byte(""))
	if len(domains) != 0 {
		t.Errorf("expected 0 domains, got %d", len(domains))
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
