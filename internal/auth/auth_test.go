package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestArgon2PasswordHashing(t *testing.T) {
	password := "Correct-Horse-Battery-Staple-15"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	match, err := VerifyPassword(password, hash)
	if err != nil {
		t.Fatalf("VerifyPassword failed: %v", err)
	}
	if !match {
		t.Errorf("Expected password to match hash")
	}

	wrongMatch, _ := VerifyPassword("Wrong-Password-12345", hash)
	if wrongMatch {
		t.Errorf("Expected wrong password to fail verification")
	}
}

func TestPasswordMinLength(t *testing.T) {
	shortPassword := "short"
	_, err := HashPassword(shortPassword)
	if err != ErrPasswordTooShort {
		t.Errorf("Expected ErrPasswordTooShort for short password, got: %v", err)
	}
}

func TestSessionManager(t *testing.T) {
	sm := NewSessionManager()
	session := sm.CreateSession()

	if len(session.ID) != 64 { // 32 bytes hex encoded = 64 chars
		t.Errorf("Expected 64 char hex session ID, got length %d", len(session.ID))
	}

	rec := httptest.NewRequest("GET", "/api/v1/config", nil)
	rec.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: session.ID,
	})

	valSession, err := sm.ValidateSession(rec)
	if err != nil {
		t.Fatalf("Expected valid session, got: %v", err)
	}
	if valSession.ID != session.ID {
		t.Errorf("Session ID mismatch")
	}
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(3, 1*time.Minute)
	ip := "192.168.1.50"

	for i := 0; i < 3; i++ {
		if !rl.Allow(ip) {
			t.Errorf("Attempt %d should have been allowed", i+1)
		}
	}

	if rl.Allow(ip) {
		t.Errorf("4th attempt should have been rate limited")
	}
}
