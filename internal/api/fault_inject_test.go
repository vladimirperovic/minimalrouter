package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/auth/persistent"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestLoginRejectsWhenTOTPSecretReadFails(t *testing.T) {
	store, err := config.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	hash, _ := auth.HashPassword("password1234567")
	store.SetAdminHash(hash)

	sessionMgr := persistent.NewPersistentSessionManagerWithSecureCookies(store, false)
	rateLimiter := auth.NewRateLimiter(5, 60*time.Second)

	server := NewServerWithAuth(nil, sessionMgr, rateLimiter, hash, store)
	
	// Inject fault by closing the store DB
	store.Close()

	payload := map[string]interface{}{
		"password": "password1234567",
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.handleLogin(w, req)

	res := w.Result()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable, got %d", res.StatusCode)
	}
}
