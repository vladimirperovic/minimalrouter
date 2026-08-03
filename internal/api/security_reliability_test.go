package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/auth/persistent"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestLoginFailsClosedWhenTOTPStateCannotBeRead(t *testing.T) {
	store, err := config.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword("a-strong-administrator-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAdminHash(hash); err != nil {
		t.Fatal(err)
	}
	manager := persistent.NewPersistentSessionManagerWithSecureCookies(store, false)
	server := NewServerWithAuth(nil, manager, auth.NewRateLimiter(5, time.Minute), hash, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{"password": "a-strong-administrator-password"})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.handleLogin(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503: %s", response.Code, response.Body.String())
	}
}

func TestOpenAPIDocumentsEveryRegisteredRoute(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	spec := string(data)
	for _, route := range []string{
		"/auth/login", "/auth/logout", "/auth/session", "/auth/change-password",
		"/auth/totp/enable", "/auth/totp/enroll", "/auth/totp/disable",
		"/setup/status", "/setup/interfaces", "/setup/apply",
		"/system", "/system/interfaces", "/system/diagnostics", "/audit/events",
		"/config", "/wireguard/peers", "/wireguard/provisioning-preview", "/transactions/pending", "/transactions/{id}/confirm",
		"/recovery/reconcile", "/snapshots", "/snapshots/{id}/restore",
		"/import/pfsense/preview", "/import/pfsense/{id}/apply", "/backup/export",
		"/backup/import/preview", "/import/backup/{id}/apply", "/firmware/verify",
		"/network/wol", "/qos/speedtest",
	} {
		if !strings.Contains(spec, "  "+route+":") {
			t.Errorf("OpenAPI is missing registered route %s", route)
		}
	}
	if strings.Contains(spec, "/config/confirm:") || strings.Contains(spec, "/auth/password:") {
		t.Fatal("OpenAPI contains a legacy route that routerd does not register")
	}
}
