package config

import (
	"os"
	"testing"
	"time"
)

// A login verifies a password against the hash and generation read together,
// then issues a session. If a password change lands in between, the session
// must not be created: it would otherwise be stamped with the new generation
// and pass validation despite proving only the revoked password.
func TestCreateSessionRefusesAStaleCredentialGeneration(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "session-generation-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.SetAdminHash("$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGE"); err != nil {
		t.Fatal(err)
	}
	hash, generation, err := store.GetAdminAuthState()
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" || generation == 0 {
		t.Fatalf("expected a credential snapshot, got hash=%q generation=%d", hash, generation)
	}

	// The password is rotated while the login is between verification and
	// session issuance.
	if err := store.SetAdminHash("$argon2id$v=19$m=65536,t=3,p=2$bmV3c2FsdG5ld3NhbHRuZXc$bmV3aGFzaG5ld2hhc2huZXdoYXNobmV3"); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	ok, err := store.CreateSessionIfGenerationCurrent("stale-session", "csrf", false, generation, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("a session must not be issued for a credential generation that has already been superseded")
	}
	if _, _, _, _, _, err := store.GetSession("stale-session"); err == nil {
		t.Fatal("no session row may exist for the refused login")
	}

	// The same call against the current generation still succeeds.
	_, current, err := store.GetAdminAuthState()
	if err != nil {
		t.Fatal(err)
	}
	ok, err = store.CreateSessionIfGenerationCurrent("fresh-session", "csrf", false, current, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("a login verified against the current credential must be able to create its session")
	}
}
