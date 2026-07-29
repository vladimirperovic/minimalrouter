package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
)

func TestLogsFeedRequiresAuthenticationAndReturnsAuditMetadata(t *testing.T) {
	server, mux, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?limit=250", nil)
	unauthenticatedResponse := httptest.NewRecorder()
	mux.ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated logs request returned %d, want 401", unauthenticatedResponse.Code)
	}

	server.appendAudit("test.logs_visible", "127.0.0.1", map[string]string{
		"result": "recorded",
	})
	session := server.sessionMgr.CreateSession()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?limit=250", nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated logs request returned %d: %s", response.Code, response.Body.String())
	}

	var payload struct {
		Events []struct {
			EventType string            `json:"event_type"`
			Actor     string            `json:"actor"`
			Details   map[string]string `json:"details"`
		} `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) == 0 {
		t.Fatal("authenticated logs feed returned no audit metadata")
	}
	found := false
	for _, event := range payload.Events {
		if event.EventType == "test.logs_visible" && event.Actor == "127.0.0.1" && event.Details["result"] == "recorded" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("authenticated logs feed did not return the recorded audit event")
	}
}
