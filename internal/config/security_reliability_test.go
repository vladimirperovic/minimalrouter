package config

import (
	"database/sql"
	"testing"
	"time"
)

func TestSQLitePragmasApplyToEveryPooledConnection(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	store.db.SetMaxOpenConns(4)
	connections := make([]*sql.Conn, 0, 4)
	for i := 0; i < 4; i++ {
		conn, err := store.db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
	}
	defer func() {
		for _, conn := range connections {
			_ = conn.Close()
		}
	}()

	for i, conn := range connections {
		var foreignKeys, trustedSchema, busyTimeout int
		if err := conn.QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatalf("connection %d foreign_keys: %v", i, err)
		}
		if err := conn.QueryRowContext(t.Context(), `PRAGMA trusted_schema`).Scan(&trustedSchema); err != nil {
			t.Fatalf("connection %d trusted_schema: %v", i, err)
		}
		if err := conn.QueryRowContext(t.Context(), `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			t.Fatalf("connection %d busy_timeout: %v", i, err)
		}
		if foreignKeys != 1 || trustedSchema != 0 || busyTimeout != 5000 {
			t.Fatalf("connection %d has unsafe pragmas: foreign_keys=%d trusted_schema=%d busy_timeout=%d", i, foreignKeys, trustedSchema, busyTimeout)
		}
	}
}

func TestCredentialRotationAdvancesGenerationAtomically(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.SetAdminHash("hash-one"); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GetAuthGeneration()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := store.CreateSession("old-session", "csrf", false, generation, now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAdminHash("hash-two"); err != nil {
		t.Fatal(err)
	}
	nextGeneration, err := store.GetAuthGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if nextGeneration != generation+1 {
		t.Fatalf("generation=%d, want %d", nextGeneration, generation+1)
	}
	if _, _, _, _, _, err := store.GetSession("old-session"); err == nil {
		t.Fatal("old session row survived credential rotation")
	}
}

func TestReadOnlySQLiteCredentialChangeFailsClosed(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetAdminHash("old-hash"); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetAuthGeneration()
	if err != nil {
		t.Fatal(err)
	}
	store.db.SetMaxOpenConns(1)
	if _, err := store.db.Exec(`PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAdminHash("new-hash"); err == nil {
		t.Fatal("credential update unexpectedly succeeded on query-only SQLite")
	}
	generation, err := store.GetAuthGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if generation != before {
		t.Fatalf("failed update advanced generation from %d to %d", before, generation)
	}
	hash, err := store.GetAdminHash()
	if err != nil {
		t.Fatal(err)
	}
	if hash != "old-hash" {
		t.Fatalf("failed update changed credential to %q", hash)
	}
}
