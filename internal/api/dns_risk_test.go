package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/dnsrisk"
)

func TestDNSRiskRoutesSecurityValidationAndAvailability(t *testing.T) {
	s, handler, store, session := setupDNSActivityServer(t, true)
	risk, err := dnsrisk.Open(t.TempDir(), func() (bool, int, error) {
		settings, err := store.DNSSettings()
		return settings.Enabled, settings.RetentionDays, err
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureDNSRisk(risk)
	t.Cleanup(func() { s.ConfigureDNSRisk(nil); risk.Close() })
	readonly := s.sessionMgr.CreateSessionWithMode(true)
	request := func(method, path string, sess *auth.Session, csrf bool, remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if sess != nil {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sess.ID})
			if csrf {
				r.Header.Set(auth.CSRFHeaderName, sess.CSRFToken)
			}
		}
		if remote != "" {
			r.RemoteAddr = remote
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	reads := []string{"/api/v1/dns-activity/alerts/summary", "/api/v1/dns-activity/alerts", "/api/v1/dns-activity/alerts/exceptions"}
	writes := []struct{ method, path string }{{"POST", "/api/v1/dns-activity/alerts/1/acknowledge"}, {"POST", "/api/v1/dns-activity/alerts/1/ignore"}, {"DELETE", "/api/v1/dns-activity/alerts/exceptions/1"}, {"POST", "/api/v1/dns-activity/alerts/refresh"}}
	for _, path := range reads {
		if w := request("GET", path, nil, false, ""); w.Code != 401 {
			t.Fatalf("unauthenticated %s = %d", path, w.Code)
		}
		if w := request("GET", path, session, false, "192.168.2.10:1234"); w.Code != 403 {
			t.Fatalf("untrusted %s = %d", path, w.Code)
		}
		if w := request("GET", path, readonly, false, ""); w.Code != 200 || w.Result().Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("read %s = %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, route := range writes {
		for _, sess := range []*auth.Session{nil, readonly} {
			if w := request(route.method, route.path, sess, true, ""); w.Code != 401 && w.Code != 403 {
				t.Fatalf("mutation allowed %s = %d", route.path, w.Code)
			}
		}
		if w := request(route.method, route.path, session, false, ""); w.Code != 403 {
			t.Fatalf("no CSRF %s = %d", route.path, w.Code)
		}
		if w := request(route.method, route.path, session, true, "192.168.2.10:1234"); w.Code != 403 {
			t.Fatalf("untrusted mutation %s = %d", route.path, w.Code)
		}
	}
	for _, suffix := range []string{"?view=invalid", "?category=hacking", "?offset=-1", "?limit=101", "?limit=oops"} {
		if w := request("GET", reads[1]+suffix, session, false, ""); w.Code != 400 {
			t.Fatalf("bad query %s = %d", suffix, w.Code)
		}
	}
	if w := request("POST", "/api/v1/dns-activity/alerts/0/acknowledge", session, true, ""); w.Code != 400 {
		t.Fatalf("bad ID = %d", w.Code)
	}
	if w := request(writes[0].method, writes[0].path, session, true, ""); w.Code != 404 {
		t.Fatalf("missing alert = %d", w.Code)
	}
	refresh := writes[3]
	if w := request(refresh.method, refresh.path, session, true, ""); w.Code != 202 || w.Result().Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("refresh = %d headers=%v", w.Code, w.Result().Header)
	}
	if w := request(refresh.method, refresh.path, session, true, ""); w.Code != 429 {
		t.Fatalf("missing refresh rate limit = %d", w.Code)
	}
	s.ConfigureDNSRisk(nil)
	if w := request("GET", reads[1], session, false, ""); w.Code != 503 {
		t.Fatalf("missing service = %d", w.Code)
	}
}
