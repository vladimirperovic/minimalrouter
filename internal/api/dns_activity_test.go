package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/accounting"
	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func setupDNSActivityServer(t *testing.T, enabled bool) (*Server, http.Handler, *accounting.Store, *auth.Session) {
	t.Helper()
	dir, err := os.MkdirTemp("", "router-dns-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	configStore, err := config.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(apply.NewEngineWithClient(config.DefaultConfig(), configStore, apiTestApplyClient{}))
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	server.RegisterDNSActivityRoutes(mux)
	store, err := accounting.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	server.ConfigureAccountingStore(store)
	if err := store.SetDNSSettings(accounting.DNSSettings{Enabled: enabled, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	collector := accounting.NewDNSCollector(store, nil, func() accounting.DNSSettings {
		settings, _ := store.DNSSettings()
		return settings
	}, nil)
	server.ConfigureDNSActivity(collector)
	t.Cleanup(func() {
		server.ConfigureDNSActivity(nil)
		server.ConfigureAccountingStore(nil)
	})
	now := time.Now().UTC()
	day := now.Truncate(24 * time.Hour).Unix()
	if err := store.RecordDNS(now, accounting.DNSFlush{
		Daily: map[accounting.DNSDailyKey]accounting.DNSDailyCount{
			{Day: day, Address: "192.168.1.50", Site: "pornhub.com"}: {Lookups: 3, FirstSeen: now.Unix(), LastSeen: now.Unix()},
			{Day: day, Address: "192.168.1.50", Site: "example.com"}: {Lookups: 9, FirstSeen: now.Unix(), LastSeen: now.Unix()},
		},
		Hourly: map[accounting.DNSHourlyKey]uint64{{Hour: now.Truncate(time.Hour).Unix(), Address: "192.168.1.50"}: 12},
	}); err != nil {
		t.Fatal(err)
	}
	return server, trustedMux(mux), store, server.sessionMgr.CreateSession()
}

func TestDNSActivityRoutesRequireAuthAndValidateInput(t *testing.T) {
	_, handler, _, session := setupDNSActivityServer(t, true)
	request := func(method, path string, logged bool, csrf bool, remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if logged {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
		}
		if csrf {
			r.Header.Set(auth.CSRFHeaderName, session.CSRFToken)
		}
		if remote != "" {
			r.RemoteAddr = remote
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/v1/dns-activity", "/api/v1/dns-activity/recent"} {
		if r := request("GET", path, false, false, ""); r.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s = %d", path, r.Code)
		}
		if r := request("GET", path, true, false, "192.168.2.10:1234"); r.Code != http.StatusForbidden {
			t.Fatalf("untrusted %s = %d", path, r.Code)
		}
		if r := request("GET", path, true, false, ""); r.Code != http.StatusOK || r.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("authenticated %s = %d %s", path, r.Code, r.Body.String())
		}
	}
	for _, path := range []string{
		"/api/v1/dns-activity?period=all",
		"/api/v1/dns-activity?device=not-an-ip",
		"/api/v1/dns-activity?q=%25",
		"/api/v1/dns-activity/recent?limit=0",
	} {
		if r := request("GET", path, true, false, ""); r.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", path, r.Code)
		}
	}
	if r := request("POST", "/api/v1/dns-activity/clear", true, false, ""); r.Code == http.StatusOK {
		t.Fatal("clear accepted a request without a CSRF token")
	}

	var activity accounting.DNSActivity
	r := request("GET", "/api/v1/dns-activity", true, false, "")
	if err := json.Unmarshal(r.Body.Bytes(), &activity); err != nil {
		t.Fatal(err)
	}
	if !activity.Enabled || activity.TotalLookups != 12 || len(activity.Flagged) != 1 {
		t.Fatalf("activity = %+v", activity)
	}
	if activity.Flagged[0].Site != "pornhub.com" || activity.Flagged[0].Category != "adult" {
		t.Fatalf("adult site was not categorized: %+v", activity.Flagged[0])
	}
	for _, site := range activity.Sites {
		if site.Site == "example.com" && site.Category != "" {
			t.Fatalf("uncategorized site got category %q", site.Category)
		}
	}

	if r := request("POST", "/api/v1/dns-activity/clear", true, true, ""); r.Code != http.StatusOK {
		t.Fatalf("clear = %d %s", r.Code, r.Body.String())
	}
	r = request("GET", "/api/v1/dns-activity", true, false, "")
	if err := json.Unmarshal(r.Body.Bytes(), &activity); err != nil {
		t.Fatal(err)
	}
	if activity.TotalLookups != 0 || len(activity.Sites) != 0 {
		t.Fatalf("history survived clear: %+v", activity)
	}
}

func TestDNSActivityDisabledHidesRetainedHistory(t *testing.T) {
	_, handler, _, session := setupDNSActivityServer(t, false)
	r := httptest.NewRequest("GET", "/api/v1/dns-activity", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var activity accounting.DNSActivity
	if err := json.Unmarshal(w.Body.Bytes(), &activity); err != nil {
		t.Fatal(err)
	}
	if activity.Enabled || activity.TotalLookups != 0 || len(activity.Sites) != 0 || len(activity.Flagged) != 0 {
		t.Fatalf("disabled DNS activity exposed retained history: %+v", activity)
	}
}

func TestDNSActivitySettingsRoundTripAndValidation(t *testing.T) {
	_, handler, store, session := setupDNSActivityServer(t, false)
	put := func(body string, csrf bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/api/v1/dns-activity/settings", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
		if csrf {
			r.Header.Set(auth.CSRFHeaderName, session.CSRFToken)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if r := put(`{"enabled":true,"retention_days":14}`, false); r.Code == http.StatusOK {
		t.Fatal("settings changed without a CSRF token")
	}
	for _, body := range []string{`{"enabled":true,"retention_days":0}`, `{"enabled":true,"retention_days":91}`, `{"enabled":true,"retention_days":14,"extra":1}`} {
		if r := put(body, true); r.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", body, r.Code)
		}
	}
	if r := put(`{"enabled":true,"retention_days":14}`, true); r.Code != http.StatusOK {
		t.Fatalf("valid settings = %d %s", r.Code, r.Body.String())
	}
	settings, err := store.DNSSettings()
	if err != nil || !settings.Enabled || settings.RetentionDays != 14 {
		t.Fatalf("stored settings = %+v, %v", settings, err)
	}
	r := httptest.NewRequest("GET", "/api/v1/dns-activity/settings", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"retention_days":14`) {
		t.Fatalf("GET settings = %d %s", w.Code, w.Body.String())
	}
}
