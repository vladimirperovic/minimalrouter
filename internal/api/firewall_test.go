package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/health"
	"github.com/vladimirperovic/minimalrouter/internal/services"
)

func TestFirewallRoutesRequireTrustedAuthenticatedReadAndRejectInvalidFlow(t *testing.T) {
	s, mux, handler, dir := setupTestServer(t)
	defer os.RemoveAll(dir)
	s.RegisterAccountingRoutes(mux)
	session := s.sessionMgr.CreateSession()
	get := func(path, remote string, logged bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = remote
		if logged {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/v1/firewall/status", "/api/v1/firewall/explain?source_ip=192.168.1.50&destination_ip=1.1.1.1&protocol=tcp&port=443"} {
		if r := get(path, "192.168.1.50:1234", false); r.Code != 401 {
			t.Fatal("unauthenticated", r.Code)
		}
		if r := get(path, "192.168.2.50:1234", true); r.Code != 403 {
			t.Fatal("untrusted", r.Code)
		}
		if r := get(path, "192.168.1.50:1234", true); r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	for _, query := range []string{"port=oops", "source_ip=::1&destination_ip=1.1.1.1&protocol=tcp&port=443", "source_ip=192.168.1.50&destination_ip=1.1.1.1&protocol=icmp&port=1"} {
		if r := get("/api/v1/firewall/explain?"+query, "192.168.1.50:1234", true); r.Code != 400 {
			t.Fatal("invalid accepted", r.Code)
		}
	}
}

func TestFirewallStatusDoesNotConfuseConfigurationWithRuntime(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Revision = 42
	cfg.WAN.Password = "TEST-SECRET"
	rule := config.FirewallRule{ID: "deny", Name: "Example", Enabled: true, Action: "deny", Direction: "forward", Protocol: "tcp", DstPort: 53, Scope: "wan"}
	cfg.Firewall.CustomRules = []config.FirewallRule{rule}
	s := &Server{engine: apply.NewEngineWithClient(cfg, nil, nil)}
	defer s.PublishFirewallObservation(nil)
	if c := s.firewallHealth(cfg.Revision); c.State != health.StateUnknown {
		t.Fatal(c)
	}
	counter := services.FirewallRuleCounter(rule)
	sample := func(generation string, count uint64, at time.Time, rev config.Revision) {
		s.PublishFirewallObservation(&apply.FirewallCounters{ObservedAt: at, Generation: generation, LoadedRevision: &rev, Policies: map[string]string{"input": "drop", "forward": "drop", "output": "drop"}, RulePackets: map[string]uint64{counter: count}})
	}
	type reply struct {
		Runtime firewallRuntime `json:"runtime"`
		Rules   []struct {
			Packets *uint64    `json:"packets"`
			Last    *time.Time `json:"last_increase_observed_at"`
		} `json:"rules"`
	}
	read := func() reply {
		w := httptest.NewRecorder()
		s.handleFirewallStatus(w, httptest.NewRequest("GET", "/", nil))
		if strings.Contains(w.Body.String(), "TEST-SECRET") {
			t.Fatal("secret leaked")
		}
		var v reply
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	sample("g1", 12, time.Now().Add(-time.Minute), 42)
	v := read()
	if !v.Runtime.DefaultDeny || !v.Runtime.RevisionMatches || v.Runtime.ConfigurationMatch != "unverified" || v.Rules[0].Packets == nil || v.Rules[0].Last != nil {
		t.Fatalf("bad initial observation %+v", v)
	}
	sample("g1", 13, time.Now().Add(-time.Second), 42)
	if read().Rules[0].Last == nil {
		t.Fatal("increase not recorded")
	}
	sample("g2", 2, time.Now().Add(-time.Second), 42)
	if read().Rules[0].Last != nil {
		t.Fatal("old generation match time carried")
	}
	sample("g2", 3, time.Now().Add(-time.Second), 41)
	if read().Rules[0].Packets != nil || s.firewallHealth(42).State != health.StateWarning {
		t.Fatal("old revision presented as current")
	}
	sample("g2", 4, time.Now().Add(-4*time.Minute), 42)
	if read().Runtime.Available || read().Rules[0].Packets != nil || s.firewallHealth(42).State != health.StateUnknown {
		t.Fatal("stale telemetry presented as current")
	}
	s.PublishFirewallObservation(&apply.FirewallCounters{ObservedAt: time.Now().Add(-time.Second), Policies: map[string]string{"input": "accept"}})
	if s.firewallHealth(42).State != health.StateDegraded {
		t.Fatal("missing drop chains healthy")
	}
}
