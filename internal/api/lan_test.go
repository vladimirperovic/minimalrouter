package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/telemetry"
)

func TestLANAddressAssessmentCoversWholeSubnetAndKnownConflicts(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.LAN.IPAddress = "192.168.1.254"
	cfg.LAN.CIDR = "192.168.1.254/24"
	snapshot := telemetry.DHCPLeaseSnapshot{DHCPLeaseStatus: telemetry.DHCPLeaseStatus{Available: true, CollectedAt: time.Now()}, Leases: []telemetry.DHCPLease{{IPAddress: "192.168.1.1", MAC: "02:00:00:00:00:01"}}}
	got := assessLANAddress(cfg, snapshot, "192.168.1.2", "02:00:00:00:00:02", "")
	if got.State != "no_known_conflict" || got.SuggestedAddress != "192.168.1.2" {
		t.Fatalf("gateway above pool: %+v", got)
	}
	for _, address := range []string{"192.168.1.0", "192.168.1.255", "192.168.1.254", "192.168.1.150", "192.168.2.2", "192.168.1.1"} {
		got := assessLANAddress(cfg, snapshot, address, "02:00:00:00:00:02", "")
		if got.State != "conflict" || len(got.Conflicts) == 0 {
			t.Fatalf("accepted %s", address)
		}
	}
	snapshot.Available = false
	if got := assessLANAddress(cfg, snapshot, "192.168.1.3", "", ""); got.State != "unknown" {
		t.Fatal(got)
	}
	if pool := poolSummary(cfg, snapshot); pool.Free != nil {
		t.Fatal("unavailable became zero leases")
	}
	snapshot.Available = true
	snapshot.Truncated = true
	if pool := poolSummary(cfg, snapshot); pool.Free != nil {
		t.Fatal("partial observation became full allocation")
	}
}

func TestLANAddressSuggestionSkipsPoolAndDNSAddresses(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DHCP.RangeStart = "192.168.1.2"
	cfg.DHCP.RangeEnd = "192.168.1.250"
	got := assessLANAddress(cfg, telemetry.DHCPLeaseSnapshot{}, "", "", "")
	if got.SuggestedAddress != "192.168.1.251" {
		t.Fatal(got)
	}
	cfg.DHCP.StaticLeases = []config.StaticLease{{ID: "one", MAC: "02:00:00:00:00:01", IPAddress: "192.168.1.251"}}
	got = assessLANAddress(cfg, telemetry.DHCPLeaseSnapshot{}, "", "", "")
	if got.SuggestedAddress != "192.168.1.252" {
		t.Fatal(got)
	}
}

func TestLANAddressAssessmentUsesAddressIdentity(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DHCP.StaticLeases = []config.StaticLease{{ID: "reserved", IPAddress: "::ffff:192.168.1.30", MAC: "02-00-00-00-00-30"}}
	snapshot := telemetry.DHCPLeaseSnapshot{DHCPLeaseStatus: telemetry.DHCPLeaseStatus{Available: true}, Leases: []telemetry.DHCPLease{{IPAddress: "192.168.1.20", MAC: "02:00:00:00:00:20"}}}
	for _, tc := range []struct{ ip, mac, state string }{
		{"::ffff:192.168.1.1", "", "conflict"},
		{"::ffff:192.168.1.20", "02:00:00:00:00:21", "conflict"},
		{"::ffff:192.168.1.20", "02-00-00-00-00-20", "no_known_conflict"},
		{"192.168.1.30", "", "conflict"},
		{"192.168.1.40", "0200.0000.0030", "conflict"},
	} {
		got := assessLANAddress(cfg, snapshot, tc.ip, tc.mac, "")
		if got.State != tc.state {
			t.Fatalf("ip %q mac %q: got %+v, want %s", tc.ip, tc.mac, got, tc.state)
		}
	}
}

func TestLANReadRoutesRequireAuthentication(t *testing.T) {
	_, _, handler, dir := setupTestServer(t)
	defer os.RemoveAll(dir)
	for _, path := range []string{"/api/v1/network/lan", "/api/v1/network/devices", "/api/v1/network/leases", "/api/v1/network/address-check"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = "192.168.1.2:1111"
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s returned %d", path, w.Code)
		}
	}
}

func TestLANHandlersBoundArgumentsAndReportMissingObservation(t *testing.T) {
	s := &Server{engine: apply.NewEngineWithClient(config.DefaultConfig(), nil, nil)}
	for _, query := range []string{"?limit=0", "?limit=501", "?offset=-1", "?sort=arbitrary"} {
		w := httptest.NewRecorder()
		s.handleDHCPLeases(w, httptest.NewRequest("GET", "/"+query, nil))
		if w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	w := httptest.NewRecorder()
	s.handleLANStatus(w, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(w.Body.String(), "lease_status") || strings.Contains(w.Body.String(), "password") {
		t.Fatal(w.Body.String())
	}
}
