package config

import (
	"testing"
	"time"
)

func TestRecoveryResetAuthenticationRollsBackOnSessionFailure(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetAdminHash("old-hash"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAdminTOTPSecret("OLD-TOTP"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.CreateSession("session", "csrf", false, 1, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER block_session_delete BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}

	if err := store.RecoveryResetAuthentication("new-hash", true); err == nil {
		t.Fatal("expected transactional session deletion failure")
	}
	hash, err := store.GetAdminHash()
	if err != nil || hash != "old-hash" {
		t.Fatalf("password changed despite rollback: hash=%q err=%v", hash, err)
	}
	secret, err := store.GetAdminTOTPSecret()
	if err != nil || secret != "OLD-TOTP" {
		t.Fatalf("TOTP changed despite rollback: secret=%q err=%v", secret, err)
	}
	if _, _, _, _, _, err := store.GetSession("session"); err != nil {
		t.Fatalf("session disappeared despite rollback: %v", err)
	}
}

func TestRecoverySaveConfigRollsBackSnapshotConfigAndCredentialsTogether(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetAdminHash("old-hash"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.CreateSession("session", "csrf", false, 1, now, now); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetLatestConfig()
	if err != nil {
		t.Fatal(err)
	}
	next := current
	next.Revision++
	next.System.Hostname = "recovered-router"
	if _, err := store.db.Exec(`CREATE TRIGGER block_session_delete BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	newHash := "new-hash"
	if _, err := store.RecoverySaveConfig(current, next, &newHash, true); err == nil {
		t.Fatal("expected transactional recovery failure")
	}

	latest, err := store.GetLatestConfig()
	if err != nil {
		t.Fatal(err)
	}
	if latest.Revision != current.Revision || latest.System.Hostname != current.System.Hostname {
		t.Fatalf("configuration changed despite rollback: revision=%d hostname=%q", latest.Revision, latest.System.Hostname)
	}
	snapshots, err := store.ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 0 {
		t.Fatalf("undo snapshot survived rolled-back recovery: %+v", snapshots)
	}
	hash, err := store.GetAdminHash()
	if err != nil || hash != "old-hash" {
		t.Fatalf("credentials changed despite rollback: hash=%q err=%v", hash, err)
	}
	if _, _, _, _, _, err := store.GetSession("session"); err != nil {
		t.Fatalf("session disappeared despite rollback: %v", err)
	}
}
