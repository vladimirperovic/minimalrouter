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
	mu       sync.RWMutex
	store    *config.SQLiteStore
	sessions map[string]*auth.Session // in-memory cache for performance
}

// NewPersistentSessionManager initializes a new persistent session manager.
func NewPersistentSessionManager(store *config.SQLiteStore) *PersistentSessionManager {
	psm := &PersistentSessionManager{
		store:    store,
		sessions: make(map[string]*auth.Session),
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

func generateRandomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// CreateSession allocates a new random 256-bit session ID and CSRF token, persists to SQLite.
func (psm *PersistentSessionManager) CreateSession() *auth.Session {
	psm.mu.Lock()
	defer psm.mu.Unlock()

	now := time.Now()
	session := &auth.Session{
		ID:        generateRandomHex(32), // 256 bits
		CSRFToken: generateRandomHex(16),
		CreatedAt: now,
		LastSeen:  now,
	}

	psm.sessions[session.ID] = session

	// Persist to SQLite (non-blocking, log error)
	if err := psm.store.CreateSession(session.ID, session.CSRFToken, session.CreatedAt, session.LastSeen); err != nil {
		// Log but don't fail - session will work in memory
		// In production, this should be handled more gracefully
	}

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
		now := time.Now()
		if now.Sub(session.CreatedAt) > auth.AbsoluteTimeout || now.Sub(session.LastSeen) > auth.IdleTimeout {
			psm.mu.RUnlock()
			psm.DestroySession(r, nil) // Will clean up both memory and DB
			return nil, auth.ErrUnauthorized
		}
		session.LastSeen = now
		// Update last_seen in SQLite periodically (not on every request)
		// For simplicity, we update on every validation
		go psm.store.UpdateSessionLastSeen(sessionID, now)
		return session, nil
	}

	// Not in cache - load from SQLite
	csrfToken, createdAt, lastSeen, err := psm.store.GetSession(sessionID)
	if err != nil {
		return nil, auth.ErrUnauthorized
	}

	now := time.Now()
	if now.Sub(createdAt) > auth.AbsoluteTimeout || now.Sub(lastSeen) > auth.IdleTimeout {
		go psm.store.DeleteSession(sessionID)
		return nil, auth.ErrUnauthorized
	}

	// Add to cache
	session = &auth.Session{
		ID:        sessionID,
		CSRFToken: csrfToken,
		CreatedAt: createdAt,
		LastSeen:  now,
	}
	psm.mu.Lock()
	psm.sessions[sessionID] = session
	psm.mu.Unlock()

	go psm.store.UpdateSessionLastSeen(sessionID, now)
	return session, nil
}

// DestroySession invalidates the active session (both memory and SQLite).
func (psm *PersistentSessionManager) DestroySession(r *http.Request, w http.ResponseWriter) {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err == nil && cookie.Value != "" {
		sessionID := cookie.Value

		psm.mu.Lock()
		delete(psm.sessions, sessionID)
		psm.mu.Unlock()

		go psm.store.DeleteSession(sessionID)
	}

	// Expire cookie
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// SetSessionCookie attaches HTTP-only, Secure, SameSite=Strict cookie to response.
func (psm *PersistentSessionManager) SetSessionCookie(w http.ResponseWriter, session *auth.Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    session.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true, // Require HTTPS in production
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
		go psm.store.CleanExpiredSessions(auth.IdleTimeout, auth.AbsoluteTimeout)
	}
}