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

// CreateSessionWithGeneration issues a session only if expectedGeneration —
// the authentication epoch read alongside the hash the caller just verified
// (see config.SQLiteStore.GetAdminAuthState) — is still current. This ties
// session issuance atomically to the exact credential that was checked: a
// password/TOTP change racing between verification and this call advances the
// epoch, so the insert is refused instead of silently adopting the new epoch.
func (psm *PersistentSessionManager) CreateSessionWithGeneration(readOnly bool, expectedGeneration uint64) (*auth.Session, error) {
	psm.mu.Lock()
	defer psm.mu.Unlock()

	sessionID, err := generateRandomHex(32)
	if err != nil {
		return nil, err
	}
	csrfToken, err := generateRandomHex(32)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	ok, err := psm.store.CreateSessionIfGenerationCurrent(sessionID, csrfToken, readOnly, expectedGeneration, now, now)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, auth.ErrGenerationChanged
	}
	session := &auth.Session{
		ID:             sessionID,
		CSRFToken:      csrfToken,
		ReadOnly:       readOnly,
		AuthGeneration: expectedGeneration,
		CreatedAt:      now,
		LastSeen:       now,
	}
	psm.sessions[session.ID] = session
	psm.lastPersisted[session.ID] = now
	return session, nil
}

// ValidateSession verifies if the given session cookie ID is valid and active.
func (psm *PersistentSessionManager) ValidateSession(r *http.Request) (*auth.Session, error) {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, auth.ErrUnauthorized
	}

	sessionID := cookie.Value
	// Keep durable loads and cache insertion in the same critical section as
	// revocation, so a concurrent cache miss cannot restore a deleted session.
	psm.mu.Lock()
	session, persistDue, err := psm.validateSessionLocked(sessionID)
	psm.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if persistDue {
		// UPDATE cannot recreate a row deleted after validation released mu.
		psm.persistLastSeen(sessionID, session.LastSeen)
	}
	return session, nil
}

func (psm *PersistentSessionManager) validateSessionLocked(sessionID string) (*auth.Session, bool, error) {
	// Check in-memory cache first
	session, exists := psm.sessions[sessionID]

	if exists {
		currentGeneration, err := psm.store.GetAuthGeneration()
		if err != nil {
			return nil, false, auth.ErrUnauthorized
		}
		if session.AuthGeneration != currentGeneration {
			delete(psm.sessions, sessionID)
			delete(psm.lastPersisted, sessionID)
			_ = psm.store.DeleteSession(sessionID)
			return nil, false, auth.ErrUnauthorized
		}
		now := time.Now()
		if now.Sub(session.CreatedAt) > auth.AbsoluteTimeout || now.Sub(session.LastSeen) > auth.IdleTimeout {
			delete(psm.sessions, sessionID)
			delete(psm.lastPersisted, sessionID)
			_ = psm.store.DeleteSession(sessionID)
			return nil, false, auth.ErrUnauthorized
		}
		session.LastSeen = now
		copy := *session
		persistDue := psm.reserveLastSeenPersistenceLocked(sessionID, now)
		return &copy, persistDue, nil
	}

	// Not in cache - load from SQLite
	csrfToken, readOnly, sessionGeneration, createdAt, lastSeen, err := psm.store.GetSession(sessionID)
	if err != nil {
		return nil, false, auth.ErrUnauthorized
	}
	currentGeneration, err := psm.store.GetAuthGeneration()
	if err != nil || sessionGeneration != currentGeneration {
		_ = psm.store.DeleteSession(sessionID)
		return nil, false, auth.ErrUnauthorized
	}

	now := time.Now()
	if now.Sub(createdAt) > auth.AbsoluteTimeout || now.Sub(lastSeen) > auth.IdleTimeout {
		_ = psm.store.DeleteSession(sessionID)
		return nil, false, auth.ErrUnauthorized
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
	psm.sessions[sessionID] = session
	psm.lastPersisted[sessionID] = lastSeen
	persistDue := psm.reserveLastSeenPersistenceLocked(sessionID, now)
	copy := *session
	return &copy, persistDue, nil
}

// DestroySession invalidates the active session (both memory and SQLite).
// A failed durable delete leaves the cookie and cache intact for a logout retry.
func (psm *PersistentSessionManager) DestroySession(r *http.Request, w http.ResponseWriter) error {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err == nil && cookie.Value != "" {
		sessionID := cookie.Value

		psm.mu.Lock()
		if err := psm.store.DeleteSession(sessionID); err != nil {
			psm.mu.Unlock()
			return err
		}
		delete(psm.sessions, sessionID)
		delete(psm.lastPersisted, sessionID)
		psm.mu.Unlock()
	}

	// Expire cookie
	if w == nil {
		return nil
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
	return nil
}

// DestroyAllSessions synchronously clears the cache and persistent store.
func (psm *PersistentSessionManager) DestroyAllSessions() error {
	psm.mu.Lock()
	defer psm.mu.Unlock()
	if err := psm.store.DeleteAllSessions(); err != nil {
		return err
	}
	clear(psm.sessions)
	clear(psm.lastPersisted)
	return nil
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
