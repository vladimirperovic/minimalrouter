package main

import (
	"strings"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/services"
)

func dnsArtifactsForTest(t *testing.T, cfg config.SystemConfig) map[string]artifact {
	t.Helper()
	dnsmasq, err := services.GenerateDnsmasq(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	adblock := []byte("# AdGuard disabled\n")
	if cfg.AdGuard.Enabled {
		text, err := services.GenerateAdBlockConf(&cfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		adblock = []byte(text)
	}
	return map[string]artifact{
		"dnsmasq": {data: []byte(dnsmasq)},
		"adblock": {data: adblock},
	}
}

func TestDNSMasqArtifactsChangedForDNSFilter(t *testing.T) {
	previous := config.DefaultConfig()
	candidate := previous
	candidate.AdGuard.Enabled = !previous.AdGuard.Enabled

	changed, err := dnsmasqArtifactsChanged(dnsArtifactsForTest(t, candidate), &previous)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("DNS filter change did not request a dnsmasq reload")
	}
}

func TestDNSMasqArtifactsUnchangedForUnrelatedConfig(t *testing.T) {
	previous := config.DefaultConfig()
	candidate := previous
	candidate.QoS.Enabled = !previous.QoS.Enabled

	changed, err := dnsmasqArtifactsChanged(dnsArtifactsForTest(t, candidate), &previous)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("unrelated QoS change unexpectedly requested a dnsmasq reload")
	}
}

func TestRequiresFunctionalDNSVerification(t *testing.T) {
	base := config.DefaultConfig()
	base.WAN.Enabled = true
	base.WAN.Username = "user"
	base.WAN.Password = "password"

	unrelated := base
	unrelated.QoS.Enabled = !base.QoS.Enabled
	if requiresFunctionalDNSVerification(&base, unrelated) {
		t.Fatal("unrelated change should not depend on external DNS availability")
	}

	dnsChanged := base
	dnsChanged.DHCP.DNSServers = []string{"9.9.9.9"}
	if !requiresFunctionalDNSVerification(&base, dnsChanged) {
		t.Fatal("DNS server change must require a functional DNS check")
	}

	wanChanged := base
	wanChanged.WAN.Username = "other-user"
	if !requiresFunctionalDNSVerification(&base, wanChanged) {
		t.Fatal("WAN change must require a functional DNS check")
	}

	disabled := base
	disabled.WAN.Enabled = false
	if requiresFunctionalDNSVerification(&base, disabled) {
		t.Fatal("disabled WAN should not require public DNS resolution")
	}
}

func TestRunCommandOutputTimesOut(t *testing.T) {
	_, err := runCommandOutput(25*time.Millisecond, "/bin/sh", "-c", "sleep 1")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected bounded command timeout, got %v", err)
	}
}
