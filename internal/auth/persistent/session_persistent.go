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

const sessionLastSeenPersistInterval = time.Minute

// PersistentSessionManager handles server-side session lifecycle with SQLite persistence.
type PersistentSessionManager struct {
	mu            sync.RWMutex
	store         *config.SQLiteStore
	sessions      map[string]*auth.Session // in-memory cache for performance
	lastPersisted map[string]time.Time     // last durable last_seen write per cached session
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
		lastPersisted: make(map[string]time.Time),
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

// reserveLastSeenPersistenceLocked records one writer for a durable last_seen
// refresh. Authorization always uses the in-memory LastSeen value on cache hits;
// SQLite is only the restart fallback, so writing it on every dashboard request
// adds WAL churn without improving the live idle-timeout guarantee.
func (psm *PersistentSessionManager) reserveLastSeenPersistenceLocked(sessionID string, now time.Time) bool {
	last, ok := psm.lastPersisted[sessionID]
	if ok && !now.Before(last) && now.Sub(last) < sessionLastSeenPersistInterval {
		return false
	}
	psm.lastPersisted[sessionID] = now
	return true
}

func (psm *PersistentSessionManager) persistLastSeen(sessionID string, lastSeen time.Time) {
	if err := psm.store.UpdateSessionLastSeen(sessionID, lastSeen); err == nil {
		return
	}

	// Let the next request retry a failed durability refresh. Do not roll the
	// marker back if another request has already reserved a newer write.
	psm.mu.Lock()
	if reserved, ok := psm.lastPersisted[sessionID]; ok && reserved.Equal(lastSeen) {
		delete(psm.lastPersisted, sessionID)
	}
	psm.mu.Unlock()
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
	generation, err := psm.store.GetAuthGeneration()
	if err != nil {
		return nil
	}
	now := time.Now()
	session := &auth.Session{
		ID:             sessionID,
		CSRFToken:      csrfToken,
		ReadOnly:       readOnly,
		AuthGeneration: generation,
		CreatedAt:      now,
		LastSeen:       now,
	}

	if err := psm.store.CreateSession(session.ID, session.CSRFToken, session.ReadOnly, session.AuthGeneration, session.CreatedAt, session.LastSeen); err != nil {
		return nil
	}
	psm.sessions[session.ID] = session
	psm.lastPersisted[session.ID] = now

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
		currentGeneration, err := psm.store.GetAuthGeneration()
		if err != nil {
			return nil, auth.ErrUnauthorized
		}
		psm.mu.Lock()
		session, exists = psm.sessions[sessionID]
		if !exists || session.AuthGeneration != currentGeneration {
			delete(psm.sessions, sessionID)
			delete(psm.lastPersisted, sessionID)
			psm.mu.Unlock()
			_ = psm.store.DeleteSession(sessionID)
			return nil, auth.ErrUnauthorized
		}
		now := time.Now()
		if now.Sub(session.CreatedAt) > auth.AbsoluteTimeout || now.Sub(session.LastSeen) > auth.IdleTimeout {
			delete(psm.sessions, sessionID)
			delete(psm.lastPersisted, sessionID)
			psm.mu.Unlock()
			_ = psm.store.DeleteSession(sessionID)
			return nil, auth.ErrUnauthorized
		}
		session.LastSeen = now
		copy := *session
		persistDue := psm.reserveLastSeenPersistenceLocked(sessionID, now)
		psm.mu.Unlock()
		if persistDue {
			psm.persistLastSeen(sessionID, now)
		}
		return &copy, nil
	}

	// Not in cache - load from SQLite
	csrfToken, readOnly, sessionGeneration, createdAt, lastSeen, err := psm.store.GetSession(sessionID)
	if err != nil {
		return nil, auth.ErrUnauthorized
	}
	currentGeneration, err := psm.store.GetAuthGeneration()
	if err != nil || sessionGeneration != currentGeneration {
		_ = psm.store.DeleteSession(sessionID)
		return nil, auth.ErrUnauthorized
	}

	now := time.Now()
	if now.Sub(createdAt) > auth.AbsoluteTimeout || now.Sub(lastSeen) > auth.IdleTimeout {
		_ = psm.store.DeleteSession(sessionID)
		return nil, auth.ErrUnauthorized
	}

	// Add to cache. The durable timestamp may be up to one persistence interval
	// behind an actively used session after a crash; that can only expire a
	// restarted session slightly early, never extend its authorization lifetime.
	session = &auth.Session{
		ID:             sessionID,
		CSRFToken:      csrfToken,
		ReadOnly:       readOnly,
		AuthGeneration: sessionGeneration,
		CreatedAt:      createdAt,
		LastSeen:       now,
	}
	psm.mu.Lock()
	psm.sessions[sessionID] = session
	psm.lastPersisted[sessionID] = lastSeen
	persistDue := psm.reserveLastSeenPersistenceLocked(sessionID, now)
	psm.mu.Unlock()

	if persistDue {
		psm.persistLastSeen(sessionID, now)
	}
	copy := *session
	return &copy, nil
}

// DestroySession invalidates the active session (both memory and SQLite).
func (psm *PersistentSessionManager) DestroySession(r *http.Request, w http.ResponseWriter) {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err == nil && cookie.Value != "" {
		sessionID := cookie.Value

		psm.mu.Lock()
		delete(psm.sessions, sessionID)
		delete(psm.lastPersisted, sessionID)
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
	clear(psm.lastPersisted)
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
				delete(psm.lastPersisted, id)
			}
		}
		psm.mu.Unlock()

		// Clean SQLite
		_ = psm.store.CleanExpiredSessions(auth.IdleTimeout, auth.AbsoluteTimeout)
	}
}
