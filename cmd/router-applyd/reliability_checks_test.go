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

func TestRequiresDDNSVerification(t *testing.T) {
	base := config.DefaultConfig()
	base.WAN.Enabled = true
	base.WAN.Username = "user"
	base.WAN.Password = "password"
	base.Cloudflare.DDNSEnabled = true
	base.Cloudflare.Domain = "router.example.net"
	base.Cloudflare.DDNSProvider = "noip"
	base.Cloudflare.DDNSUser = "user"
	base.Cloudflare.APIToken = "01234567890123456789"

	unrelated := base
	unrelated.QoS.Enabled = !base.QoS.Enabled
	if requiresDDNSVerification(&base, unrelated) {
		t.Fatal("unrelated config should not trigger a provider update")
	}

	credentialChanged := base
	credentialChanged.Cloudflare.APIToken = "98765432109876543210"
	if !requiresDDNSVerification(&base, credentialChanged) {
		t.Fatal("DDNS credential change must trigger provider verification")
	}

	hostnameChanged := base
	hostnameChanged.Cloudflare.Domain = "new-router.example.net"
	if !requiresDDNSVerification(&base, hostnameChanged) {
		t.Fatal("DDNS hostname change must trigger provider verification")
	}

	disabled := base
	disabled.Cloudflare.DDNSEnabled = false
	if requiresDDNSVerification(&base, disabled) {
		t.Fatal("disabled DDNS should not trigger provider verification")
	}
}

func TestRunCommandOutputTimesOut(t *testing.T) {
	_, err := runCommandOutput(25*time.Millisecond, "/bin/sh", "-c", "sleep 1")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected bounded command timeout, got %v", err)
	}
}

func TestDDNSOutputFailureMarker(t *testing.T) {
	failing := []string{
		"inadyn[3395]: Fatal error in DDNS server response: Authentication failure",
		"inadyn[1]: Error response from DDNS server, exiting!",
		"inadyn[1]: Error code 50: Authentication failure",
		"inadyn[1]: FATAL ERROR: cannot contact provider",
	}
	for _, output := range failing {
		if ddnsOutputFailureMarker(output) == "" {
			t.Fatalf("output %q should be flagged as a DDNS failure", output)
		}
	}
	passing := []string{
		"inadyn[1]: Update forced for alias mr-test.lab.test, new IP# 178.222.79.86",
		"inadyn[1]: no update needed",
		"inadyn[1]: Update successful",
		"",
	}
	for _, output := range passing {
		if ddnsOutputFailureMarker(output) != "" {
			t.Fatalf("output %q should not be flagged as a DDNS failure", output)
		}
	}
}

func TestDDNSOutputFailureMarkerExtended(t *testing.T) {
	failing := []string{
		"inadyn[1]: Failed connecting to dynupdate.no-ip.com: Operation in progress",
		"inadyn[1]: Failed to update alias mr-test.lab.test",
		"inadyn[1]: Failed sending checkip request to 10.250.0.10:443",
		"inadyn[1]: Failed resolving dynupdate.no-ip.com",
		"inadyn[1]: Operation timed out after 45 seconds",
		"inadyn[1]: Unable to connect to DDNS server",
		"inadyn[1]: Provider unreachable",
		"inadyn[1]: Cannot contact provider",
	}
	for _, output := range failing {
		if ddnsOutputFailureMarker(output) == "" {
			t.Fatalf("output %q should be flagged as a DDNS failure", output)
		}
	}
	passing := []string{
		"inadyn[1]: Successful alias table update for mr-test.lab.test => new IP# 1.2.3.4",
		"inadyn[1]: Update forced for alias mr-test.lab.test, new IP# 1.2.3.4",
		"inadyn[1]: no update needed",
		"",
	}
	for _, output := range passing {
		if ddnsOutputFailureMarker(output) != "" {
			t.Fatalf("output %q should not be flagged as a DDNS failure", output)
		}
	}
}
