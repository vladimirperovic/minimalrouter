package persistent

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestOldSessionRejectedAfterAuthGenerationChanges(t *testing.T) {
	store, err := config.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetAdminHash("first-hash"); err != nil {
		t.Fatal(err)
	}
	manager := NewPersistentSessionManagerWithSecureCookies(store, false)
	session := manager.CreateSession()
	if session == nil {
		t.Fatal("failed to create session")
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	if _, err := manager.ValidateSession(request); err != nil {
		t.Fatalf("fresh session rejected: %v", err)
	}
	if err := store.SetAdminHash("second-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ValidateSession(request); err == nil {
		t.Fatal("session from older auth generation was accepted")
	}
}

func TestSessionReloadCannotRestoreOlderGeneration(t *testing.T) {
	store, err := config.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetAdminHash("first-hash"); err != nil {
		t.Fatal(err)
	}
	manager := NewPersistentSessionManagerWithSecureCookies(store, false)
	session := manager.CreateSession()
	if session == nil {
		t.Fatal("failed to create session")
	}
	if err := store.SetAdminHash("second-hash"); err != nil {
		t.Fatal(err)
	}
	// Reinsert a stale row to model incomplete cleanup or restored storage.
	if err := store.CreateSession(session.ID, session.CSRFToken, false, session.AuthGeneration, session.CreatedAt, session.LastSeen); err != nil {
		t.Fatal(err)
	}
	restarted := NewPersistentSessionManagerWithSecureCookies(store, false)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.ID})
	if _, err := restarted.ValidateSession(request); err == nil {
		t.Fatal("stale persisted session was accepted after restart")
	}
}
