package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/gateway"
)

type apiGatewayProber struct{}

func (apiGatewayProber) Probe(_ context.Context, target string) gateway.TargetResult {
	return gateway.TargetResult{Target: target, Reachable: true, PacketsSent: 4, PacketsReceived: 4, LatencyMS: 20, JitterMS: 2}
}

type apiGatewayLink struct{}

func (apiGatewayLink) Read(context.Context) gateway.LinkStatus {
	return gateway.LinkStatus{Connected: true, Interface: "ppp0", LocalIP: "203.0.113.10", PeerIP: "198.51.100.1"}
}

func TestGatewayEndpointsRequireAuthenticationAndReturnBoundedData(t *testing.T) {
	server, mux, handler, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)
	gatewayStore, err := gateway.OpenStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	defer gatewayStore.Close()
	monitor := gateway.NewMonitor(gatewayStore, apiGatewayProber{}, apiGatewayLink{})
	if _, err := monitor.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.ConfigureGatewayMonitor(monitor)
	server.RegisterGatewayRoutes(mux)

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/gateway/summary", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated summary returned %d", unauthorized.Code)
	}

	session := server.sessionMgr.CreateSession()
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}

	summaryResponse := get("/api/v1/gateway/summary")
	if summaryResponse.Code != http.StatusOK {
		t.Fatalf("summary returned %d: %s", summaryResponse.Code, summaryResponse.Body.String())
	}
	var summary gateway.Summary
	if err := json.NewDecoder(summaryResponse.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if summary.State != gateway.StateHealthy || len(summary.Targets) != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	historyResponse := get("/api/v1/gateway/history?window=1h")
	if historyResponse.Code != http.StatusOK {
		t.Fatalf("history returned %d: %s", historyResponse.Code, historyResponse.Body.String())
	}
	var history struct {
		Window string                 `json:"window"`
		Points []gateway.HistoryPoint `json:"points"`
	}
	if err := json.NewDecoder(historyResponse.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if history.Window != "1h" || len(history.Points) != 1 {
		t.Fatalf("unexpected history response: %+v", history)
	}

	settingsResponse := get("/api/v1/gateway/settings")
	if settingsResponse.Code != http.StatusOK {
		t.Fatalf("settings returned %d: %s", settingsResponse.Code, settingsResponse.Body.String())
	}

	payload := []byte(`{"enabled":false,"targets":["9.9.9.9","1.0.0.1"],"interval_seconds":60}`)
	withoutCSRF := httptest.NewRequest(http.MethodPut, "/api/v1/gateway/settings", bytes.NewReader(payload))
	withoutCSRF.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	withoutCSRF.Header.Set("Content-Type", "application/json")
	withoutCSRFResponse := httptest.NewRecorder()
	handler.ServeHTTP(withoutCSRFResponse, withoutCSRF)
	if withoutCSRFResponse.Code != http.StatusForbidden {
		t.Fatalf("settings mutation without CSRF returned %d", withoutCSRFResponse.Code)
	}

	update := httptest.NewRequest(http.MethodPut, "/api/v1/gateway/settings", bytes.NewReader(payload))
	update.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	update.Header.Set("Content-Type", "application/json")
	update.Header.Set(auth.CSRFHeaderName, session.CSRFToken)
	updateResponse := httptest.NewRecorder()
	handler.ServeHTTP(updateResponse, update)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("settings update returned %d: %s", updateResponse.Code, updateResponse.Body.String())
	}
	stored, err := gatewayStore.Settings()
	if err != nil || stored.Enabled || stored.IntervalSeconds != 60 || stored.Targets[0] != "9.9.9.9" {
		t.Fatalf("settings were not persisted: %+v err=%v", stored, err)
	}

	invalidResponse := get("/api/v1/gateway/history?window=30d")
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid history window returned %d", invalidResponse.Code)
	}
}
