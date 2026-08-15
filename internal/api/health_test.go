package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestHealthEndpointEnforcesTrustedNetworkBeforeAuthentication(t *testing.T) {
	server, mux, handler, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)
	server.RegisterHealthRoutes(mux)

	untrusted := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	untrusted.RemoteAddr = "192.168.2.10:12345"
	untrustedResponse := httptest.NewRecorder()
	handler.ServeHTTP(untrustedResponse, untrusted)
	if untrustedResponse.Code != http.StatusForbidden {
		t.Fatalf("untrusted health returned %d, want 403", untrustedResponse.Code)
	}

	trusted := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	trusted.RemoteAddr = "192.168.1.10:12345"
	trustedResponse := httptest.NewRecorder()
	handler.ServeHTTP(trustedResponse, trusted)
	if trustedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("trusted unauthenticated health returned %d, want 401", trustedResponse.Code)
	}
}
