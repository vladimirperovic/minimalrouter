package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
)

func TestLogsFeedRequiresAuthenticationAndReturnsAuditMetadata(t *testing.T) {
	server, _, handler, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?limit=250", nil)
	unauthenticatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticatedResponse, unauthenticated)
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
	handler.ServeHTTP(response, request)
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

func TestConfigRequestLinksActorToSanitizedOutcome(t *testing.T) {
	server, _, handler, dir := setupTestServer(t)
	defer os.RemoveAll(dir)
	cfg := server.engine.GetCurrentConfig()
	body, _ := json.Marshal(cfg)
	session := server.sessionMgr.CreateSession()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	r.Header.Set(auth.CSRFHeaderName, session.CSRFToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("apply failed: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	events, err := server.engine.GetStore().ListAuditEvents(100)
	if err != nil {
		t.Fatal(err)
	}
	linked, outcome := false, false
	for _, event := range events {
		if event.Details["transaction_id"] != result.ID {
			continue
		}
		if event.EventType == "config.request" && event.Actor == "192.168.1.2" {
			linked = true
		}
		if event.EventType == "config.transaction" && event.Details["state"] == "Committed" {
			outcome = true
		}
	}
	if !linked || !outcome {
		t.Fatalf("missing request/outcome correlation: %+v", events)
	}
}

func TestAuditQueryValidationAndReadOnlyFilters(t *testing.T) {
	server, _, handler, dir := setupTestServer(t)
	defer os.RemoveAll(dir)
	server.appendAudit("firmware.activation_start_failed", "local", nil)
	server.appendAudit("dns_filter.update_requested", "local", nil)
	session := server.sessionMgr.CreateSessionWithMode(true)
	for _, query := range []string{"limit=0", "limit=501", "category=invalid", "cursor=bad", "since=yesterday", "since=2026-10-10T00:00:00Z&until=2026-10-09T00:00:00Z"} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?"+query, nil)
		r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("accepted %s: %d", query, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?category=recovery&q=activation&limit=1", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "firmware.activation_start_failed") || strings.Contains(w.Body.String(), "dns_filter.update_requested") || strings.Contains(w.Body.String(), `"details":null`) {
		t.Fatalf("bad filtered response: %d %s", w.Code, w.Body.String())
	}
}

func TestReadOnlySessionReadsAuditLogButCannotMutate(t *testing.T) {
	server, _, handler, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)
	server.appendAudit("test.read_only_visible", "127.0.0.1", nil)
	session := server.sessionMgr.CreateSessionWithMode(true)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?limit=10", nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("read-only audit read returned %d: %s", response.Code, response.Body.String())
	}

	mutation := httptest.NewRequest(http.MethodPost, "/api/v1/snapshots", nil)
	mutation.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	mutation.Header.Set(auth.CSRFHeaderName, session.CSRFToken)
	mutationResponse := httptest.NewRecorder()
	handler.ServeHTTP(mutationResponse, mutation)
	if mutationResponse.Code != http.StatusForbidden {
		t.Fatalf("read-only session mutation returned %d, want 403", mutationResponse.Code)
	}
}
