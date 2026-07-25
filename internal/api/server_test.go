package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func setupTestServer(t *testing.T) (*Server, *http.ServeMux, string) {
	tempDir, err := os.MkdirTemp("", "router-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	store, err := config.NewStore(tempDir)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("Failed to create store: %v", err)
	}

	cfg := config.DefaultConfig()
	engine := apply.NewEngine(cfg, store)
	server := NewServer(engine)

	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	return server, mux, tempDir
}

func TestSetupStatusEndpoint(t *testing.T) {
	_, mux, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	req := httptest.NewRequest("GET", "/api/v1/setup/status", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for setup status, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["is_configured"] != false {
		t.Errorf("Expected is_configured to be false for fresh install, got %v", resp["is_configured"])
	}
}

func TestUnauthenticatedProtectedEndpoints(t *testing.T) {
	_, mux, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	endpoints := []string{
		"/api/v1/config",
		"/api/v1/system",
		"/api/v1/snapshots",
		"/api/v1/system/diagnostics",
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest("GET", ep, nil)
		w := httptest.NewRecorder()

		mux.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected 401 Unauthorized for endpoint %s, got %d", ep, w.Code)
		}
	}
}

func TestSetupApplyAndLoginFlow(t *testing.T) {
	_, mux, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	// 1. Run Setup Wizard
	wizardBody := map[string]string{
		"wan_interface":  "eth0",
		"pppoe_username": "user@isp.com",
		"pppoe_password": "supersecretpassword",
		"admin_password": "longsecureadminpassword123!",
		"lan_interface":  "eth1",
		"lan_ip_address": "192.168.1.1",
	}
	data, _ := json.Marshal(wizardBody)

	req := httptest.NewRequest("POST", "/api/v1/setup/apply", bytes.NewBuffer(data))
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for setup apply, got %d: %s", w.Code, w.Body.String())
	}

	// 2. Try Login with correct password
	loginBody := map[string]string{
		"password": "longsecureadminpassword123!",
	}
	loginData, _ := json.Marshal(loginBody)

	loginReq := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewBuffer(loginData))
	loginW := httptest.NewRecorder()

	mux.ServeHTTP(loginW, loginReq)

	if loginW.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for login, got %d: %s", loginW.Code, loginW.Body.String())
	}

	// 3. Verify session cookie set
	cookies := loginW.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("Expected session cookie to be set after login")
	}
}
