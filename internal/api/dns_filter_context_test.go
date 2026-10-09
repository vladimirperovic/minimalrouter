package api

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/dnsfilter"
	"github.com/vladimirperovic/minimalrouter/internal/services"
	"github.com/vladimirperovic/minimalrouter/internal/telemetry"
)

func TestDNSContextRoutesAvailableToTrustedReadOnlySessions(t *testing.T) {
	s, handler, _, session := setupDNSActivityServer(t, true)
	filter, err := dnsfilter.Open(t.TempDir(), apiFilterHelper{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer filter.Close()
	s.ConfigureDNSFilter(filter)
	readonly := s.sessionMgr.CreateSessionWithMode(true)
	for _, path := range []string{"/api/v1/dns-filter/profiles", "/api/v1/dns-filter/operations", "/api/v1/dns-filter/check?domain=example.com&device_ip=192.168.1.50"} {
		if got := regressionRequest(t, handler, "GET", path, "192.168.1.10:1234", nil, nil); got.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", path, got.Code)
		}
		if got := regressionRequest(t, handler, "GET", path, "192.168.2.10:1234", nil, session); got.Code != 403 {
			t.Fatalf("untrusted %s: %d", path, got.Code)
		}
		got := regressionRequest(t, handler, "GET", path, "192.168.1.10:1234", nil, readonly)
		if got.Code != 200 || got.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("read-only %s: %d %s", path, got.Code, got.Body.String())
		}
		var result map[string]any
		if err := json.Unmarshal(got.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	for _, ip := range []string{"::1", "192.0.2.1&admin=true", "garbage"} {
		got := regressionRequest(t, handler, "GET", "/api/v1/dns-filter/check?domain=example.com&device_ip="+url.QueryEscape(ip), "192.168.1.10:1234", nil, readonly)
		if got.Code != 422 {
			t.Fatalf("invalid device context accepted: %s %d", ip, got.Code)
		}
	}
}

func TestDNSExplanationRespectsLocalOverrideExceptionAndDeviceSchedule(t *testing.T) {
	cfg := config.SystemConfig{Revision: 42}
	cfg.AdGuard.Enabled = true
	domain := services.ServiceDomains["youtube"][0]
	cfg.AdGuard.DeviceProfiles = []config.DeviceProfile{{ID: "kids", Name: "Kids", Enabled: true, IPAddresses: []string{"192.0.2.5"}, Services: []string{"youtube"}}}
	check := dnsfilter.Check{Domain: domain, Action: "Allow exception", Exception: true, PolicyRevision: 7, Matches: []dnsfilter.Match{}}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	result := explainDNSDomain(cfg, nil, check, "192.0.2.5", now)
	if result.Action != "Scheduled block" || result.Profile == nil || result.Profile.EnforcementVerified || result.ConfigRevision != 42 || result.PolicyRevision != 7 {
		t.Fatalf("schedule after exception: %+v", result)
	}
	cfg.DNS.Records = []config.DNSRecord{{Name: domain, IP: "192.0.2.20"}}
	result = explainDNSDomain(cfg, nil, check, "192.0.2.5", now)
	if result.Action != "Local DNS record" {
		t.Fatalf("local precedence: %+v", result)
	}
	cfg.DHCP.Enabled = true
	cfg.System.Domain = "lan"
	cfg.DHCP.StaticLeases = []config.StaticLease{{Hostname: "reserved", IPAddress: "192.0.2.20"}}
	if kind, _ := localDNSMatch(cfg, nil, "reserved.lan"); kind != "" {
		t.Fatal("reservation alone claimed an active DNS name")
	}
	if kind, ip := localDNSMatch(cfg, []telemetry.DHCPLease{{Hostname: "active", IPAddress: "192.0.2.30"}}, "active.lan"); kind != "dhcp_name" || ip != "192.0.2.30" {
		t.Fatalf("active lease: %s %s", kind, ip)
	}
}
