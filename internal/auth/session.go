package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"
)

const (
	SessionCookieName = "mr_session"
	CSRFHeaderName    = "X-CSRF-Token"
	IdleTimeout       = 30 * time.Minute
	AbsoluteTimeout   = 8 * time.Hour
)

var ErrUnauthorized = errors.New("unauthorized or expired session")

// Session tracks an authenticated administrator session.
type Session struct {
	ID        string    `json:"id"`
	CSRFToken string    `json:"csrf_token"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
}

// SessionManager handles server-side session lifecycle in memory/store.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionManager initializes a new session manager.
func NewSessionManager() *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*Session),
	}
	go sm.cleanLoop()
	return sm
}

func generateRandomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// CreateSession allocates a new random 256-bit session ID and CSRF token.
func (sm *SessionManager) CreateSession() *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	session := &Session{
		ID:        generateRandomHex(32), // 256 bits
		CSRFToken: generateRandomHex(16),
		CreatedAt: now,
		LastSeen:  now,
	}

	sm.sessions[session.ID] = session
	return session
}

// ValidateSession verifies if the given session cookie ID is valid and active.
func (sm *SessionManager) ValidateSession(r *http.Request) (*Session, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, ErrUnauthorized
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, exists := sm.sessions[cookie.Value]
	if !exists {
		return nil, ErrUnauthorized
	}

	now := time.Now()
	// Check Absolute & Idle timeouts per SECURITY.md §6
	if now.Sub(session.CreatedAt) > AbsoluteTimeout || now.Sub(session.LastSeen) > IdleTimeout {
		delete(sm.sessions, session.ID)
		return nil, ErrUnauthorized
	}

	session.LastSeen = now
	return session, nil
}

// DestroySession invalidates the active session.
func (sm *SessionManager) DestroySession(r *http.Request, w http.ResponseWriter) {
	cookie, err := r.Cookie(SessionCookieName)
	if err == nil && cookie.Value != "" {
		sm.mu.Lock()
		delete(sm.sessions, cookie.Value)
		sm.mu.Unlock()
	}

	// Expire cookie
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// SetSessionCookie attaches HTTP-only, Secure, SameSite=Strict cookie to response.
func (sm *SessionManager) SetSessionCookie(w http.ResponseWriter, session *Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    session.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true, // Require HTTPS in production
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(AbsoluteTimeout.Seconds()),
	})
}

func (sm *SessionManager) cleanLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		sm.mu.Lock()
		now := time.Now()
		for id, sess := range sm.sessions {
			if now.Sub(sess.CreatedAt) > AbsoluteTimeout || now.Sub(sess.LastSeen) > IdleTimeout {
				delete(sm.sessions, id)
			}
		}
		sm.mu.Unlock()
	}
}
