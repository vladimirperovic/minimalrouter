package api

import (
	"encoding/json"
	"github.com/vladimirperovic/minimalrouter/internal/accounting"
	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestInsightsRoutesAuthValidationAndDisabledHistory(t *testing.T) {
	server, mux, handler, dir := setupTestServer(t)
	defer os.RemoveAll(dir)
	store, err := accounting.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server.ConfigureAccountingStore(store)
	defer server.ConfigureAccountingStore(nil)
	server.RegisterAccountingRoutes(mux)
	now := time.Now().UTC()
	if err := store.Record(now.Add(-time.Minute), []accounting.Counter{{Address: "192.0.2.1", Bytes: 1000}}, nil, 13, 1); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(now, []accounting.Counter{{Address: "192.0.2.1", Bytes: 2000}}, nil, 13, 1); err != nil {
		t.Fatal(err)
	}
	session := server.sessionMgr.CreateSession()
	get := func(path string, logged bool, remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if logged {
			r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
		}
		if remote != "" {
			r.RemoteAddr = remote
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/v1/accounting/insights", "/api/v1/firewall/activity"} {
		if r := get(path, false, ""); r.Code != 401 {
			t.Fatalf("unauthenticated %s=%d", path, r.Code)
		}
		if r := get(path, true, "192.168.2.10:1234"); r.Code != 403 {
			t.Fatalf("untrusted %s=%d", path, r.Code)
		}
		if r := get(path, true, ""); r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("authenticated %s=%d %s", path, r.Code, r.Body.String())
		}
	}
	if r := get("/api/v1/accounting/insights?period=all", true, ""); r.Code != 400 {
		t.Fatal("unbounded period accepted")
	}
	if server.engine.GetCurrentConfig().Accounting.Enabled {
		t.Fatal("test requires default disabled accounting")
	}
	var v accounting.Insights
	r := get("/api/v1/accounting/insights", true, "")
	if err := json.Unmarshal(r.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Enabled || v.TotalBytes != 0 || len(v.Devices) != 0 {
		t.Fatal("disabled accounting exposed retained history")
	}
}
