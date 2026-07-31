package persistent

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// PersistentSessionManager handles server-side session lifecycle with SQLite persistence.
type PersistentSessionManager struct {
	mu            sync.RWMutex
	store         *config.SQLiteStore
	sessions      map[string]*auth.Session // in-memory cache for performance
	secureCookies bool
}

// NewPersistentSessionManager initializes a new persistent session manager.
func NewPersistentSessionManager(store *config.SQLiteStore) *PersistentSessionManager {
	return NewPersistentSessionManagerWithSecureCookies(store, true)
}

// NewPersistentSessionManagerWithSecureCookies exists only so the loopback-only
// macOS preview can use plain HTTP. Appliance callers must keep secure=true.
func NewPersistentSessionManagerWithSecureCookies(store *config.SQLiteStore, secure bool) *PersistentSessionManager {
	psm := &PersistentSessionManager{
		store:         store,
		sessions:      make(map[string]*auth.Session),
		secureCookies: secure,
	}
	// Load existing sessions from SQLite on startup
	psm.loadSessions()
	// Start cleanup loop
	go psm.cleanLoop()
	return psm
}

func (psm *PersistentSessionManager) loadSessions() {
	// Note: For simplicity, we don't load all sessions at startup
	// They will be loaded on-demand and cached
	// A production system might load recent sessions
}

func generateRandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateSession allocates a new random 256-bit session ID and CSRF token, persists to SQLite.
func (psm *PersistentSessionManager) CreateSession() *auth.Session {
	return psm.CreateSessionWithMode(false)
}

// CreateSessionWithMode persists the server-enforced privilege level with the
// session so a restart cannot turn an observer session into an administrator.
func (psm *PersistentSessionManager) CreateSessionWithMode(readOnly bool) *auth.Session {
	psm.mu.Lock()
	defer psm.mu.Unlock()

	sessionID, err := generateRandomHex(32)
	if err != nil {
		return nil
	}
	csrfToken, err := generateRandomHex(32)
	if err != nil {
		return nil
	}
	now := time.Now()
	session := &auth.Session{
		ID:        sessionID,
		CSRFToken: csrfToken,
		ReadOnly:  readOnly,
		CreatedAt: now,
		LastSeen:  now,
	}

	if err := psm.store.CreateSession(session.ID, session.CSRFToken, session.ReadOnly, session.CreatedAt, session.LastSeen); err != nil {
		return nil
	}
	psm.sessions[session.ID] = session

	return session
}

// ValidateSession verifies if the given session cookie ID is valid and active.
func (psm *PersistentSessionManager) ValidateSession(r *http.Request) (*auth.Session, error) {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, auth.ErrUnauthorized
	}

	sessionID := cookie.Value

	// Check in-memory cache first
	psm.mu.RLock()
	session, exists := psm.sessions[sessionID]
	psm.mu.RUnlock()

	if exists {
		psm.mu.Lock()
		session, exists = psm.sessions[sessionID]
		if !exists {
			psm.mu.Unlock()
			return nil, auth.ErrUnauthorized
		}
		now := time.Now()
		if now.Sub(session.CreatedAt) > auth.AbsoluteTimeout || now.Sub(session.LastSeen) > auth.IdleTimeout {
			delete(psm.sessions, sessionID)
			psm.mu.Unlock()
			_ = psm.store.DeleteSession(sessionID)
			return nil, auth.ErrUnauthorized
		}
		session.LastSeen = now
		copy := *session
		psm.mu.Unlock()
		_ = psm.store.UpdateSessionLastSeen(sessionID, now)
		return &copy, nil
	}

	// Not in cache - load from SQLite
	csrfToken, readOnly, createdAt, lastSeen, err := psm.store.GetSession(sessionID)
	if err != nil {
		return nil, auth.ErrUnauthorized
	}

	now := time.Now()
	if now.Sub(createdAt) > auth.AbsoluteTimeout || now.Sub(lastSeen) > auth.IdleTimeout {
		_ = psm.store.DeleteSession(sessionID)
		return nil, auth.ErrUnauthorized
	}

	// Add to cache
	session = &auth.Session{
		ID:        sessionID,
		CSRFToken: csrfToken,
		ReadOnly:  readOnly,
		CreatedAt: createdAt,
		LastSeen:  now,
	}
	psm.mu.Lock()
	psm.sessions[sessionID] = session
	copy := *session
	psm.mu.Unlock()

	_ = psm.store.UpdateSessionLastSeen(sessionID, now)
	return &copy, nil
}

// DestroySession invalidates the active session (both memory and SQLite).
func (psm *PersistentSessionManager) DestroySession(r *http.Request, w http.ResponseWriter) {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err == nil && cookie.Value != "" {
		sessionID := cookie.Value

		psm.mu.Lock()
		delete(psm.sessions, sessionID)
		psm.mu.Unlock()

		_ = psm.store.DeleteSession(sessionID)
	}

	// Expire cookie
	if w == nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   psm.secureCookies,
		SameSite: http.SameSiteStrictMode,
	})
}

// DestroyAllSessions synchronously clears the cache and persistent store.
func (psm *PersistentSessionManager) DestroyAllSessions() error {
	psm.mu.Lock()
	clear(psm.sessions)
	psm.mu.Unlock()
	return psm.store.DeleteAllSessions()
}

// SetSessionCookie attaches HTTP-only, Secure, SameSite=Strict cookie to response.
func (psm *PersistentSessionManager) SetSessionCookie(w http.ResponseWriter, session *auth.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    session.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   psm.secureCookies,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(auth.AbsoluteTimeout.Seconds()),
	})
}

func (psm *PersistentSessionManager) cleanLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		// Clean in-memory cache
		psm.mu.Lock()
		now := time.Now()
		for id, sess := range psm.sessions {
			if now.Sub(sess.CreatedAt) > auth.AbsoluteTimeout || now.Sub(sess.LastSeen) > auth.IdleTimeout {
				delete(psm.sessions, id)
			}
		}
		psm.mu.Unlock()

		// Clean SQLite
		_ = psm.store.CleanExpiredSessions(auth.IdleTimeout, auth.AbsoluteTimeout)
	}
}
