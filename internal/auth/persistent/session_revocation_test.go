package persistent

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func revocationTestStore(t *testing.T) (*config.SQLiteStore, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	store, err := config.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	configureSessionTestStore(t, store)
	db, err := sql.Open("sqlite", filepath.Join(dir, "minimalrouter.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store, db
}

func requireSessionRevoked(t *testing.T, manager *PersistentSessionManager, store *config.SQLiteStore, id string) {
	t.Helper()
	manager.mu.RLock()
	_, cached := manager.sessions[id]
	_, persisted := manager.lastPersisted[id]
	manager.mu.RUnlock()
	if cached || persisted {
		t.Fatal("revoked session remains cached")
	}
	if _, _, _, _, _, err := store.GetSession(id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoked session remains durable: %v", err)
	}
	for _, candidate := range []*PersistentSessionManager{manager, NewPersistentSessionManager(store)} {
		if _, err := candidate.ValidateSession(sessionRequest(id)); !errors.Is(err, auth.ErrUnauthorized) {
			t.Fatalf("revoked cookie accepted by current or restarted manager: %v", err)
		}
	}
}

func TestPersistentDestroySessionSQLiteFailureCanRetry(t *testing.T) {
	for _, cached := range []bool{true, false} {
		t.Run(map[bool]string{true: "cached", false: "restarted"}[cached], func(t *testing.T) {
			store, db := revocationTestStore(t)
			manager := NewPersistentSessionManager(store)
			session := manager.CreateSession()
			if session == nil {
				t.Fatal("create session")
			}
			if !cached {
				manager = NewPersistentSessionManager(store)
			}
			// A real SQLite failure that leaves reads and the original row intact.
			if _, err := db.Exec(`CREATE TRIGGER fail_session_delete BEFORE DELETE ON sessions BEGIN SELECT RAISE(FAIL, 'injected session delete failure'); END`); err != nil {
				t.Fatal(err)
			}
			request := sessionRequest(session.ID)
			recorder := httptest.NewRecorder()
			if err := manager.DestroySession(request, recorder); err == nil {
				t.Fatal("SQLite delete failure reported as successful logout")
			}
			if len(recorder.Header().Values("Set-Cookie")) != 0 {
				t.Fatal("failed logout discarded the cookie needed to retry")
			}
			manager.mu.RLock()
			_, retained := manager.sessions[session.ID]
			_, marker := manager.lastPersisted[session.ID]
			manager.mu.RUnlock()
			if retained != cached || marker != cached {
				t.Fatal("failed logout changed cache state")
			}
			if _, _, _, _, _, err := store.GetSession(session.ID); err != nil {
				t.Fatalf("failure fixture did not retain durable session: %v", err)
			}
			if _, err := NewPersistentSessionManager(store).ValidateSession(request); err != nil {
				t.Fatalf("failure fixture is not restart-replayable: %v", err)
			}
			if _, err := db.Exec(`DROP TRIGGER fail_session_delete`); err != nil {
				t.Fatal(err)
			}
			recorder = httptest.NewRecorder()
			if err := manager.DestroySession(request, recorder); err != nil {
				t.Fatalf("logout retry failed: %v", err)
			}
			cookies := recorder.Result().Cookies()
			if len(cookies) != 1 || cookies[0].Name != auth.SessionCookieName || cookies[0].Value != "" || cookies[0].MaxAge != -1 || cookies[0].Path != "/" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
				t.Fatalf("successful retry did not securely expire cookie: %v", cookies)
			}
			requireSessionRevoked(t, manager, store, session.ID)
			if err := manager.DestroySession(request, nil); err != nil {
				t.Fatalf("repeated revocation: %v", err)
			}
		})
	}
}

func TestPersistentDestroySessionWithoutResponseWriter(t *testing.T) {
	store, _ := revocationTestStore(t)
	manager := NewPersistentSessionManager(store)
	session := manager.CreateSession()
	if session == nil {
		t.Fatal("create session")
	}
	if err := manager.DestroySession(sessionRequest(session.ID), nil); err != nil {
		t.Fatal(err)
	}
	requireSessionRevoked(t, manager, store, session.ID)
}

func TestPersistentDestroyAllSessionsSQLiteFailureCanRetry(t *testing.T) {
	store, db := revocationTestStore(t)
	manager := NewPersistentSessionManager(store)
	generation, err := store.GetAuthGeneration()
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.CreateSessionWithGeneration(false, generation)
	if err != nil || session == nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_session_delete BEFORE DELETE ON sessions BEGIN SELECT RAISE(FAIL, 'injected session delete failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := manager.DestroyAllSessions(); err == nil {
		t.Fatal("SQLite delete failure reported as successful revocation")
	}
	if _, err := manager.ValidateSession(sessionRequest(session.ID)); err != nil {
		t.Fatalf("failed bulk revoke changed session: %v", err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_session_delete`); err != nil {
		t.Fatal(err)
	}
	if err := manager.DestroyAllSessions(); err != nil {
		t.Fatal(err)
	}
	requireSessionRevoked(t, manager, store, session.ID)
}

func TestPersistentRevocationConcurrentWithCacheReload(t *testing.T) {
	store, _ := revocationTestStore(t)
	manager := NewPersistentSessionManager(store)
	for attempt := 0; attempt < 50; attempt++ {
		session := manager.CreateSession()
		if session == nil {
			t.Fatal("create session")
		}
		manager.mu.Lock()
		delete(manager.sessions, session.ID)
		delete(manager.lastPersisted, session.ID)
		manager.mu.Unlock()
		start := make(chan struct{})
		var workers sync.WaitGroup
		for i := 0; i < 8; i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				_, err := manager.ValidateSession(sessionRequest(session.ID))
				if err != nil && !errors.Is(err, auth.ErrUnauthorized) {
					t.Errorf("concurrent validation: %v", err)
				}
			}()
		}
		close(start)
		if err := manager.DestroySession(sessionRequest(session.ID), nil); err != nil {
			t.Fatal(err)
		}
		workers.Wait()
		requireSessionRevoked(t, manager, store, session.ID)
	}
}
