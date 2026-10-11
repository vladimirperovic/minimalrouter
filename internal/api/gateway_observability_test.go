package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/gateway"
)

func TestGatewayObservabilityAuthProbeAndRecoveryHistory(t *testing.T) {
	server, mux, handler, dir := setupTestServer(t)
	defer os.RemoveAll(dir)
	store, err := gateway.OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	monitor := gateway.NewMonitor(store, apiGatewayProber{}, apiGatewayLink{})
	server.ConfigureGatewayMonitor(monitor)
	defer server.ConfigureGatewayMonitor(nil)
	server.RegisterGatewayRoutes(mux)
	session := server.sessionMgr.CreateSession()
	send := func(method, path string, logged, csrf bool, remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if logged {
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
		}
		if csrf {
			req.Header.Set(auth.CSRFHeaderName, session.CSRFToken)
		}
		if remote != "" {
			req.RemoteAddr = remote
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	for _, path := range []string{"events", "recovery", "probe"} {
		method := "GET"
		if path == "probe" {
			method = "POST"
		}
		if got := send(method, "/api/v1/gateway/"+path, false, false, ""); got.Code != 401 {
			t.Fatalf("%s auth: %d", path, got.Code)
		}
		if got := send(method, "/api/v1/gateway/"+path, true, true, "192.168.2.1:1234"); got.Code != 403 {
			t.Fatalf("%s trusted network: %d", path, got.Code)
		}
	}
	if got := send("POST", "/api/v1/gateway/probe", true, false, ""); got.Code != 403 {
		t.Fatalf("CSRF: %d", got.Code)
	}
	if got := send("POST", "/api/v1/gateway/probe", true, true, ""); got.Code != 200 {
		t.Fatalf("probe: %d %s", got.Code, got.Body.String())
	}
	if got := send("POST", "/api/v1/gateway/probe", true, true, ""); got.Code != 409 {
		t.Fatalf("probe cooldown: %d", got.Code)
	}
	server.appendAudit("gateway.auto_recovery_completed", "local", map[string]string{"duration_seconds": "8"})
	server.appendAudit("service.recovery_failed", "local", map[string]string{"action": "wan-reconnect", "duration_seconds": "12"})
	server.appendAudit("service.recovery", "local", map[string]string{"action": "dns-dhcp-restart"})
	got := send("GET", "/api/v1/gateway/events", true, false, "")
	var body struct {
		Recoveries []struct {
			EventType string            `json:"event_type"`
			Details   map[string]string `json:"details"`
		} `json:"recoveries"`
		Available bool `json:"recovery_history_available"`
	}
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &body) != nil || !body.Available || len(body.Recoveries) != 2 {
		t.Fatalf("events: %s", got.Body.String())
	}
	if body.Recoveries[0].Details["duration_seconds"] == "" {
		t.Fatal("missing action duration")
	}
	if got = send("GET", "/api/v1/gateway/recovery", true, false, ""); got.Code != 200 {
		t.Fatalf("recovery: %s", got.Body.String())
	}
}
