package persistent

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestPreviewCookieModeIsExplicit(t *testing.T) {
	store, err := config.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	manager := NewPersistentSessionManagerWithSecureCookies(store, false)
	recorder := httptest.NewRecorder()
	manager.SetSessionCookie(recorder, &auth.Session{ID: "preview-session"})
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Secure {
		t.Fatal("explicit preview cookie should be loopback-HTTP compatible")
	}

	production := NewPersistentSessionManager(store)
	recorder = httptest.NewRecorder()
	production.SetSessionCookie(recorder, &auth.Session{ID: "production-session"})
	if cookies = recorder.Result().Cookies(); len(cookies) != 1 || !cookies[0].Secure {
		t.Fatal("production cookie must remain Secure by default")
	}
}

func sessionRequest(sessionID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "https://router.lan/api/v1/system", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sessionID})
	return req
}

func configureSessionTestStore(t *testing.T, store *config.SQLiteStore) {
	t.Helper()
	if err := store.SetAdminHash("test-admin-hash"); err != nil {
		t.Fatalf("configure test authentication epoch: %v", err)
	}
}

func TestPersistentSessionLastSeenWritesAreThrottled(t *testing.T) {
	store, err := config.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	configureSessionTestStore(t, store)

	manager := NewPersistentSessionManager(store)
	session := manager.CreateSession()
	if session == nil {
		t.Fatal("expected session")
	}
	_, _, _, _, before, err := store.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	validated, err := manager.ValidateSession(sessionRequest(session.ID))
	if err != nil {
		t.Fatal(err)
	}
	if validated.LastSeen.Before(session.LastSeen) {
		t.Fatal("in-memory activity timestamp moved backwards")
	}

	_, _, _, _, after, err := store.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) {
		t.Fatalf("last_seen was written inside throttle window: before=%s after=%s", before, after)
	}
}

func TestPersistentSessionLastSeenFlushesAfterInterval(t *testing.T) {
	store, err := config.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	configureSessionTestStore(t, store)

	manager := NewPersistentSessionManager(store)
	session := manager.CreateSession()
	if session == nil {
		t.Fatal("expected session")
	}

	old := time.Now().Add(-2 * sessionLastSeenPersistInterval).UTC().Truncate(time.Second)
	if err := store.UpdateSessionLastSeen(session.ID, old); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.lastPersisted[session.ID] = old
	manager.mu.Unlock()

	if _, err := manager.ValidateSession(sessionRequest(session.ID)); err != nil {
		t.Fatal(err)
	}
	_, _, _, _, persisted, err := store.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.After(old) {
		t.Fatalf("last_seen was not refreshed after persistence interval: old=%s persisted=%s", old, persisted)
	}
}
