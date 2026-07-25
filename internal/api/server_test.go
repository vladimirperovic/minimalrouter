package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

type apiTestApplyClient struct{}

func (apiTestApplyClient) Apply(_ context.Context, req apply.ApplyRequest) (*apply.ApplyResponse, error) {
	return &apply.ApplyResponse{ID: req.ID, Success: true, Verified: true}, nil
}

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
	engine := apply.NewEngineWithClient(cfg, store, apiTestApplyClient{})
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
	if strings.Contains(w.Body.String(), wizardBody["pppoe_password"]) {
		t.Fatal("setup response leaked the PPPoE password in its transaction payload")
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

func TestTransactionResponsesRedactEveryConfigSecretWithoutMutation(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WAN.Password = "pppoe-secret"
	cfg.WireGuard.PrivateKey = "wireguard-private"
	cfg.WireGuard.Peers = []config.WireGuardPeer{{
		ID: "peer-1", Name: "Peer one", PresharedKey: "wireguard-preshared",
	}}
	cfg.Cloudflare.APIToken = "cloudflare-api-secret"
	cfg.Cloudflare.TunnelToken = "cloudflare-tunnel-secret"
	cfg.SquidProxy.Password = "squid-secret"
	cfg.WiFi.Passphrase = "wifi-secret"

	tx := &apply.Transaction{ID: "secret-test", Config: cfg}
	public := redactTransaction(tx)
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		"pppoe-secret",
		"wireguard-private",
		"wireguard-preshared",
		"cloudflare-api-secret",
		"cloudflare-tunnel-secret",
		"squid-secret",
		"wifi-secret",
	} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("public transaction leaked %q", secret)
		}
	}
	if tx.Config.WireGuard.Peers[0].PresharedKey != "wireguard-preshared" {
		t.Fatal("redaction mutated canonical transaction state")
	}
}

func TestWireGuardPeerProvisioningReturnsOneTimeRealQR(t *testing.T) {
	server, mux, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	setupBody, _ := json.Marshal(map[string]string{
		"wan_interface":  "eth0",
		"pppoe_username": "test-user",
		"pppoe_password": "test-password",
		"admin_password": "longsecureadminpassword123!",
		"lan_interface":  "eth1",
		"lan_ip_address": "192.168.1.1",
	})
	setupReq := httptest.NewRequest(http.MethodPost, "/api/v1/setup/apply", bytes.NewReader(setupBody))
	setupReq.Header.Set("Content-Type", "application/json")
	setupW := httptest.NewRecorder()
	mux.ServeHTTP(setupW, setupReq)
	if setupW.Code != http.StatusOK {
		t.Fatalf("setup failed: %d %s", setupW.Code, setupW.Body.String())
	}
	var setupResponse struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(setupW.Body.Bytes(), &setupResponse); err != nil {
		t.Fatal(err)
	}
	cookies := setupW.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("setup session cookie missing")
	}

	provisionBody, _ := json.Marshal(map[string]string{
		"name":              "Test phone",
		"client_ip_address": "10.8.0.2",
		"server_endpoint":   "vpn.example.net:51820",
	})
	provisionReq := httptest.NewRequest(http.MethodPost, "/api/v1/wireguard/peers", bytes.NewReader(provisionBody))
	provisionReq.Header.Set("Content-Type", "application/json")
	provisionReq.Header.Set(auth.CSRFHeaderName, setupResponse.CSRFToken)
	provisionReq.AddCookie(cookies[0])
	provisionW := httptest.NewRecorder()
	mux.ServeHTTP(provisionW, provisionReq)
	if provisionW.Code != http.StatusOK {
		t.Fatalf("peer provisioning failed: %d %s", provisionW.Code, provisionW.Body.String())
	}
	var provisionResponse struct {
		ClientConfig string `json:"client_config"`
		QRCodeData   string `json:"qr_code_data"`
	}
	if err := json.Unmarshal(provisionW.Body.Bytes(), &provisionResponse); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(provisionResponse.ClientConfig, "PrivateKey = ") ||
		!strings.Contains(provisionResponse.ClientConfig, "Endpoint = vpn.example.net:51820") {
		t.Fatal("one-time client configuration is incomplete")
	}
	if !strings.HasPrefix(provisionResponse.QRCodeData, "data:image/png;base64,") {
		t.Fatal("provisioning did not return a real PNG QR code")
	}

	cfg := server.engine.GetCurrentConfig()
	serialized, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	privateLine := strings.Split(strings.Split(provisionResponse.ClientConfig, "PrivateKey = ")[1], "\n")[0]
	if bytes.Contains(serialized, []byte(privateLine)) {
		t.Fatal("client private key was persisted in canonical router configuration")
	}
	if !cfg.WireGuard.Enabled || len(cfg.WireGuard.Peers) != 1 || cfg.WireGuard.Peers[0].PresharedKey == "" {
		t.Fatal("public peer configuration was not committed")
	}
}

func TestReadOnlyLoginCannotMutateRouterState(t *testing.T) {
	server, mux, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)
	password := "longsecureadminpassword123!"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	server.adminHash = hash
	server.mu.Unlock()

	loginData, _ := json.Marshal(map[string]interface{}{
		"password":  password,
		"read_only": true,
	})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginData))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	mux.ServeHTTP(loginW, loginReq)
	if loginW.Code != http.StatusOK {
		t.Fatalf("read-only login failed: %d %s", loginW.Code, loginW.Body.String())
	}
	var login struct {
		CSRFToken string `json:"csrf_token"`
		ReadOnly  bool   `json:"read_only"`
	}
	if err := json.Unmarshal(loginW.Body.Bytes(), &login); err != nil || !login.ReadOnly {
		t.Fatalf("server did not issue a read-only session: %s", loginW.Body.String())
	}
	cookies := loginW.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("read-only session cookie missing")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	getReq.AddCookie(cookies[0])
	getW := httptest.NewRecorder()
	mux.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("read-only GET rejected: %d", getW.Code)
	}

	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader([]byte(`{}`)))
	putReq.Header.Set("Content-Type", "application/json")
	putReq.Header.Set(auth.CSRFHeaderName, login.CSRFToken)
	putReq.AddCookie(cookies[0])
	putW := httptest.NewRecorder()
	mux.ServeHTTP(putW, putReq)
	if putW.Code != http.StatusForbidden || !strings.Contains(putW.Body.String(), "Read-only session") {
		t.Fatalf("read-only mutation was not blocked: %d %s", putW.Code, putW.Body.String())
	}
}

func TestAdministratorCanReadPersistedAuditMetadata(t *testing.T) {
	server, mux, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)
	password := "longsecureadminpassword123!"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	server.adminHash = hash
	server.mu.Unlock()
	if err := server.engine.GetStore().AppendAuditEvent("test.event", "192.0.2.10", map[string]string{
		"status": "verified",
	}); err != nil {
		t.Fatal(err)
	}

	loginData, _ := json.Marshal(map[string]string{"password": password})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginData))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	mux.ServeHTTP(loginW, loginReq)
	if loginW.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", loginW.Code, loginW.Body.String())
	}
	cookies := loginW.Result().Cookies()

	auditReq := httptest.NewRequest(http.MethodGet, "/api/v1/audit/events?limit=10", nil)
	auditReq.AddCookie(cookies[0])
	auditW := httptest.NewRecorder()
	mux.ServeHTTP(auditW, auditReq)
	if auditW.Code != http.StatusOK || !strings.Contains(auditW.Body.String(), `"test.event"`) {
		t.Fatalf("audit endpoint failed: %d %s", auditW.Code, auditW.Body.String())
	}
	if strings.Contains(auditW.Body.String(), password) {
		t.Fatal("audit endpoint leaked an authentication secret")
	}
}
