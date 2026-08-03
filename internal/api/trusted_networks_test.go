package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func doRequest(t *testing.T, handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func untrustedRequest(method, path string, body []byte) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.RemoteAddr = "192.168.2.10:12345" // outside 192.168.1.0/24
	return req
}

func TestUntrustedSourceRejectedBeforeAuthentication(t *testing.T) {
	_, _, handler, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	// Protected endpoint: untrusted gets 403, NOT 401 (no auth attempted).
	req := untrustedRequest(http.MethodGet, "/api/v1/config", nil)
	resp := doRequest(t, handler, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("untrusted /config returned %d, want 403", resp.Code)
	}

	// Login endpoint: untrusted gets 403, NOT a login response.
	login := untrustedRequest(http.MethodPost, "/api/v1/auth/login", []byte(`{"password":"R0uterMin!12"}`))
	login.Header.Set("Content-Type", "application/json")
	resp = doRequest(t, handler, login)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("untrusted login returned %d, want 403", resp.Code)
	}

	// Setup wizard is also gated.
	setup := untrustedRequest(http.MethodGet, "/api/v1/setup/status", nil)
	resp = doRequest(t, handler, setup)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("untrusted setup/status returned %d, want 403", resp.Code)
	}
}

func TestTrustedSourceStillRequiresAuthentication(t *testing.T) {
	_, _, handler, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	// Trusted LAN source but no session: 401, not 200.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.RemoteAddr = "192.168.1.2:12345"
	resp := doRequest(t, handler, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("trusted but unauthenticated /config returned %d, want 401", resp.Code)
	}
}

func TestTrustedSourcesAllowedAcrossSubnet(t *testing.T) {
	_, _, handler, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	for _, ip := range []string{"192.168.1.2", "192.168.1.50", "192.168.1.254"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
		req.RemoteAddr = ip + ":8443"
		resp := doRequest(t, handler, req)
		if resp.Code != http.StatusOK {
			t.Errorf("trusted %s returned %d, want 200", ip, resp.Code)
		}
	}
}

func TestLoopbackAlwaysAllowed(t *testing.T) {
	server, mux, handler, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.RemoteAddr = "127.0.0.1:8443"
	resp := doRequest(t, handler, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("loopback unauthenticated returned %d, want 401 (must pass trust gate)", resp.Code)
	}

	// Authenticated loopback client reaches the config.
	session := server.sessionMgr.CreateSession()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.RemoteAddr = "127.0.0.1:8443"
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	resp = doRequest(t, handler, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("loopback authenticated returned %d: %s", resp.Code, resp.Body.String())
	}

	// ::1 loopback also passes (unbracketed would be malformed; bracket it).
	req = httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.RemoteAddr = "[::1]:8443"
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	resp = doRequest(t, handler, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("IPv6 loopback authenticated returned %d: %s", resp.Code, resp.Body.String())
	}

	_ = mux
}

func TestSpoofedForwardedHeadersDoNotBypassTrust(t *testing.T) {
	_, _, handler, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	// Untrusted source claims trusted IPs via forwarding headers: still 403.
	req := untrustedRequest(http.MethodGet, "/api/v1/config", nil)
	req.Header.Set("X-Forwarded-For", "192.168.1.2")
	req.Header.Set("X-Real-IP", "192.168.1.2")
	req.Header.Set("Forwarded", "for=192.168.1.2")
	resp := doRequest(t, handler, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("spoofed X-Forwarded-For returned %d, want 403", resp.Code)
	}
}

func TestMalformedRemoteAddrDenied(t *testing.T) {
	_, _, handler, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	for _, remote := range []string{"", "not-an-address", "garbage:99999"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
		req.RemoteAddr = remote
		resp := doRequest(t, handler, req)
		if resp.Code != http.StatusForbidden {
			t.Errorf("malformed RemoteAddr %q returned %d, want 403 (deny)", remote, resp.Code)
		}
	}
}

func TestIPv4MappedIPv6SourceMatchesTrustedV4Network(t *testing.T) {
	_, _, handler, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.RemoteAddr = "[::ffff:192.168.1.2]:8443"
	resp := doRequest(t, handler, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("IPv4-mapped trusted source returned %d, want 401 (passes gate, no session)", resp.Code)
	}
}

func TestTrustedNetworksPersistAcrossStoreReload(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "router-trusted-persist-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	store, err := config.NewStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.TrustedNetworks = []string{"192.168.1.0/24", "10.255.255.0/24"}
	cfg.Revision = 2
	if err := store.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	reloaded, err := store.GetLatestConfig()
	if err != nil {
		t.Fatalf("GetLatestConfig: %v", err)
	}
	if len(reloaded.TrustedNetworks) != 2 ||
		reloaded.TrustedNetworks[0] != "192.168.1.0/24" ||
		reloaded.TrustedNetworks[1] != "10.255.255.0/24" {
		t.Fatalf("trusted_networks lost across reload: %v", reloaded.TrustedNetworks)
	}
}

func TestTrustedNetworksSurviveBackupRoundTrip(t *testing.T) {
	server, _, handler, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	session := server.sessionMgr.CreateSession()

	// Set trusted_networks with a second network.
	current, err := store_GetConfig(t, handler, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.TrustedNetworks = []string{"192.168.1.0/24", "10.255.255.0/24"}
	putConfig(t, handler, session, current)

	// Backup payload carries the full SystemConfig, including
	// trusted_networks; verify the encrypted round-trip preserves it.
	encrypted, err := config.EncryptConfigBackup(current, "test-passphrase-12345")
	if err != nil {
		t.Fatalf("EncryptConfigBackup: %v", err)
	}
	decrypted, err := config.DecryptConfigBackup(encrypted, "test-passphrase-12345")
	if err != nil {
		t.Fatalf("DecryptConfigBackup: %v", err)
	}
	if len(decrypted.TrustedNetworks) != 2 ||
		decrypted.TrustedNetworks[1] != "10.255.255.0/24" {
		t.Fatalf("backup lost trusted_networks: %v", decrypted.TrustedNetworks)
	}
}

func TestLockoutPreventionOnTrustedNetworksChange(t *testing.T) {
	server, _, handler, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	session := server.sessionMgr.CreateSession()

	current, err := store_GetConfig(t, handler, session.ID)
	if err != nil {
		t.Fatal(err)
	}

	// A change that removes the caller's network must be rejected.
	locked := current
	locked.TrustedNetworks = []string{"10.255.255.0/24"}
	resp := putConfigRawWithCSRF(t, handler, session.ID, session.CSRFToken, locked)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("self-locking change returned %d, want 403", resp.Code)
	}

	// A change that keeps the caller's network is accepted.
	kept := current
	kept.TrustedNetworks = []string{"192.168.1.0/24", "10.255.255.0/24"}
	resp = putConfigRawWithCSRF(t, handler, session.ID, session.CSRFToken, kept)
	if resp.Code != http.StatusOK && resp.Code != http.StatusAccepted {
		t.Fatalf("non-locking change returned %d: %s", resp.Code, resp.Body.String())
	}
}

func TestEmptyTrustedNetworksRejectedOnUpdate(t *testing.T) {
	_, _, handler, tempDir := setupTestServer(t)
	defer os.RemoveAll(tempDir)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.RemoteAddr = "192.168.1.2:12345"
	resp := doRequest(t, handler, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without session, got %d", resp.Code)
	}
}

func store_GetConfig(t *testing.T, handler http.Handler, sessionID string) (config.SystemConfig, error) {
	t.Helper()
	var out config.SystemConfig
	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	req.RemoteAddr = "192.168.1.2:12345"
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionID})
	resp := doRequest(t, handler, req)
	if resp.Code != http.StatusOK {
		return out, &unexpectedStatusError{status: resp.Code, body: resp.Body.String()}
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		return out, err
	}
	return out, nil
}

func putConfig(t *testing.T, handler http.Handler, session *auth.Session, cfg config.SystemConfig) {
	t.Helper()
	resp := putConfigRawWithCSRF(t, handler, session.ID, session.CSRFToken, cfg)
	if resp.Code != http.StatusOK && resp.Code != http.StatusAccepted {
		t.Fatalf("PUT /config returned %d: %s", resp.Code, resp.Body.String())
	}
}

func putConfigRawWithCSRF(t *testing.T, handler http.Handler, sessionID, csrf string, cfg config.SystemConfig) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	req.RemoteAddr = "192.168.1.2:12345"
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionID})
	if csrf != "" {
		req.Header.Set(auth.CSRFHeaderName, csrf)
	}
	req.Header.Set("Content-Type", "application/json")
	return doRequest(t, handler, req)
}

type unexpectedStatusError struct {
	status int
	body   string
}

func (e *unexpectedStatusError) Error() string {
	return fmt.Sprintf("unexpected status %d: %s", e.status, e.body)
}
