package config

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // Pure-Go SQLite driver (no CGO required)
)

// Snapshot represents an immutable, checksummed point-in-time configuration state.
type Snapshot struct {
	ID         string   `json:"id"`
	Revision   Revision `json:"revision"`
	CreatedAt  string   `json:"created_at"`
	Checksum   string   `json:"checksum"`
	ConfigJSON string   `json:"config_json,omitempty"`
}

type AuditEvent struct {
	ID        string            `json:"id"`
	EventType string            `json:"event_type"`
	Actor     string            `json:"actor"`
	Timestamp time.Time         `json:"timestamp"`
	Details   map[string]string `json:"details"`
}

// SQLiteStore provides canonical SQLite persistence and snapshot management
// per ARCHITECTURE.md §4.4 and migrations/0001_initial_schema.sql.
type SQLiteStore struct {
	mu sync.RWMutex
	db *sql.DB
}

// FileStore alias for backwards compatibility with routerd main.go.
type FileStore = SQLiteStore

// NewFileStore alias for NewStore.
func NewFileStore(dirPath string) (*FileStore, error) {
	return NewStore(dirPath)
}

// NewStore initializes canonical SQLite configuration persistence.
// Creates the database file and runs schema migrations on first use.
func NewStore(dirPath string) (*SQLiteStore, error) {
	if err := os.MkdirAll(dirPath, 0700); err != nil {
		return nil, fmt.Errorf("failed to create private data directory: %w", err)
	}
	if err := os.Chmod(dirPath, 0700); err != nil {
		return nil, fmt.Errorf("failed to secure data directory: %w", err)
	}
	dbPath := filepath.Join(dirPath, "minimalrouter.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database at %s: %w", dbPath, err)
	}

	// Cap connection pool to limit memory usage on embedded appliance.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)

	// Prefer durability over throughput: configuration commits are rare and
	// must survive abrupt power loss.
	if _, err := db.Exec(`
		PRAGMA journal_mode=WAL;
		PRAGMA synchronous=FULL;
		PRAGMA foreign_keys=ON;
		PRAGMA trusted_schema=OFF;
		PRAGMA secure_delete=ON;
		PRAGMA busy_timeout=5000;
		PRAGMA cache_size=-2000;
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to harden SQLite connection: %w", err)
	}
	if err := os.Chmod(dbPath, 0600); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to secure SQLite database: %w", err)
	}
	var integrity string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil || integrity != "ok" {
		db.Close()
		return nil, fmt.Errorf("SQLite integrity check failed")
	}

	// Run schema migrations (idempotent with IF NOT EXISTS)
	if err := runMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	store := &SQLiteStore{db: db}

	// Initialize default config if no revisions exist
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM config_revisions").Scan(&count); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to query config_revisions: %w", err)
	}

	if count == 0 {
		defaultCfg := DefaultConfig()
		if err := store.SaveConfig(defaultCfg); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to initialize default config: %w", err)
		}
	}

	return store, nil
}

// runMigrations executes the schema creation SQL (idempotent).
func runMigrations(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS config_revisions (
		revision INTEGER PRIMARY KEY AUTOINCREMENT,
		updated_at DATETIME NOT NULL,
		config_json TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS snapshots (
		id TEXT PRIMARY KEY,
		revision INTEGER NOT NULL,
		created_at DATETIME NOT NULL,
		checksum TEXT NOT NULL,
		config_json TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS admin_credentials (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		password_hash TEXT NOT NULL,
		totp_secret TEXT,
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		csrf_token TEXT NOT NULL,
		read_only INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL,
		last_seen DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS rate_limit_buckets (
		ip TEXT PRIMARY KEY,
		attempts INTEGER NOT NULL DEFAULT 0,
		window_start DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS audit_events (
		id TEXT PRIMARY KEY,
		event_type TEXT NOT NULL,
		actor TEXT NOT NULL,
		timestamp DATETIME NOT NULL,
		details_json TEXT NOT NULL
	);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}

	// Migrate databases created before observer sessions were introduced.
	rows, err := db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		return err
	}
	hasReadOnly := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == "read_only" {
			hasReadOnly = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if !hasReadOnly {
		if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN read_only INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	return nil
}

// GetLatestConfig reads the most recent canonical configuration from SQLite.
func (s *SQLiteStore) GetLatestConfig() (SystemConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var configJSON string
	err := s.db.QueryRow(
		"SELECT config_json FROM config_revisions ORDER BY revision DESC LIMIT 1",
	).Scan(&configJSON)
	if err != nil {
		return SystemConfig{}, fmt.Errorf("failed to read latest config: %w", err)
	}

	var cfg SystemConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return SystemConfig{}, fmt.Errorf("failed to parse config JSON: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return SystemConfig{}, fmt.Errorf("stored configuration failed validation: %w", err)
	}

	return cfg, nil
}

// SaveConfig saves updated configuration to the SQLite config_revisions table.
// Uses an atomic INSERT within a transaction for crash safety.
func (s *SQLiteStore) SaveConfig(cfg SystemConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("refusing to persist invalid configuration: %w", err)
	}

	var currentJSON string
	err := s.db.QueryRow(
		"SELECT config_json FROM config_revisions ORDER BY revision DESC LIMIT 1",
	).Scan(&currentJSON)
	if err == nil {
		var current SystemConfig
		if json.Unmarshal([]byte(currentJSON), &current) != nil ||
			cfg.Revision != current.Revision+1 {
			return fmt.Errorf("configuration revision must advance exactly once")
		}
	} else if err != sql.ErrNoRows {
		return fmt.Errorf("failed to inspect current revision: %w", err)
	} else if cfg.Revision != 1 {
		return fmt.Errorf("initial configuration revision must be 1")
	}

	cfg.UpdatedAt = time.Now()
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	_, err = tx.Exec(
		"INSERT INTO config_revisions (updated_at, config_json) VALUES (?, ?)",
		cfg.UpdatedAt.Format(time.RFC3339), string(data),
	)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to insert config revision: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit config transaction: %w", err)
	}

	return nil
}

// CreateSnapshot creates an immutable checksummed snapshot per ARCHITECTURE.md §8.
func (s *SQLiteStore) CreateSnapshot(cfg SystemConfig) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(cfg)
	if err != nil {
		return Snapshot{}, fmt.Errorf("failed to marshal config for snapshot: %w", err)
	}

	hash := sha256.Sum256(data)
	checksum := hex.EncodeToString(hash[:])
	id := fmt.Sprintf("snap-%d", time.Now().UnixNano())
	createdAt := time.Now().Format(time.RFC3339)

	tx, err := s.db.Begin()
	if err != nil {
		return Snapshot{}, fmt.Errorf("failed to begin snapshot transaction: %w", err)
	}

	_, err = tx.Exec(
		"INSERT INTO snapshots (id, revision, created_at, checksum, config_json) VALUES (?, ?, ?, ?, ?)",
		id, int64(cfg.Revision), createdAt, checksum, string(data),
	)
	if err != nil {
		tx.Rollback()
		return Snapshot{}, fmt.Errorf("failed to insert snapshot: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Snapshot{}, fmt.Errorf("failed to commit snapshot: %w", err)
	}

	snap := Snapshot{
		ID:        id,
		Revision:  cfg.Revision,
		CreatedAt: createdAt,
		Checksum:  checksum,
	}

	return snap, nil
}

// ListSnapshots returns all stored snapshots ordered by creation time (newest first).
func (s *SQLiteStore) ListSnapshots() ([]Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		"SELECT id, revision, created_at, checksum FROM snapshots ORDER BY created_at DESC",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query snapshots: %w", err)
	}
	defer rows.Close()

	var snapshots []Snapshot
	for rows.Next() {
		var snap Snapshot
		var rev int64
		if err := rows.Scan(&snap.ID, &rev, &snap.CreatedAt, &snap.Checksum); err != nil {
			continue
		}
		snap.Revision = Revision(rev)
		snapshots = append(snapshots, snap)
	}

	return snapshots, nil
}

// GetSnapshot retrieves a specific snapshot by ID including its full config JSON.
func (s *SQLiteStore) GetSnapshot(id string) (Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var snap Snapshot
	var rev int64
	err := s.db.QueryRow(
		"SELECT id, revision, created_at, checksum, config_json FROM snapshots WHERE id = ?", id,
	).Scan(&snap.ID, &rev, &snap.CreatedAt, &snap.Checksum, &snap.ConfigJSON)
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot not found: %s", id)
	}
	snap.Revision = Revision(rev)
	actual := sha256.Sum256([]byte(snap.ConfigJSON))
	expected, decodeErr := hex.DecodeString(snap.Checksum)
	if decodeErr != nil || len(expected) != sha256.Size ||
		subtle.ConstantTimeCompare(actual[:], expected) != 1 {
		return Snapshot{}, fmt.Errorf("snapshot integrity check failed: %s", id)
	}

	return snap, nil
}

// AppendAuditEvent stores metadata only. Callers must never include request
// bodies, credentials, keys, tokens, or generated configuration in details.
func (s *SQLiteStore) AppendAuditEvent(eventType, actor string, details map[string]string) error {
	if eventType == "" || len(eventType) > 96 || actor == "" || len(actor) > 255 {
		return fmt.Errorf("invalid audit event metadata")
	}
	detailsJSON, err := json.Marshal(details)
	if err != nil || len(detailsJSON) > 4096 {
		return fmt.Errorf("invalid audit event details")
	}
	idBytes := make([]byte, 18)
	if _, err := rand.Read(idBytes); err != nil {
		return fmt.Errorf("generate audit event ID: %w", err)
	}
	id := "audit-" + base64.RawURLEncoding.EncodeToString(idBytes)

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin audit event: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO audit_events (id, event_type, actor, timestamp, details_json) VALUES (?, ?, ?, ?, ?)`,
		id, eventType, actor, time.Now().UTC(), string(detailsJSON),
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("insert audit event: %w", err)
	}
	// Bound local metadata growth without weakening recent incident history.
	if _, err := tx.Exec(`
		DELETE FROM audit_events
		WHERE id NOT IN (
			SELECT id FROM audit_events ORDER BY timestamp DESC, id DESC LIMIT 5000
		)
	`); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("prune audit events: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit audit event: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListAuditEvents(limit int) ([]AuditEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, event_type, actor, timestamp, details_json
		 FROM audit_events ORDER BY timestamp DESC, id DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	events := make([]AuditEvent, 0, limit)
	for rows.Next() {
		var event AuditEvent
		var detailsJSON string
		if err := rows.Scan(&event.ID, &event.EventType, &event.Actor, &event.Timestamp, &detailsJSON); err != nil {
			return nil, fmt.Errorf("read audit event: %w", err)
		}
		if err := json.Unmarshal([]byte(detailsJSON), &event.Details); err != nil {
			return nil, fmt.Errorf("decode audit event details: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}
	return events, nil
}

// DeleteAllSessions invalidates every authenticated session after credential
// or second-factor changes.
func (s *SQLiteStore) DeleteAllSessions() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM sessions`)
	return err
}

// GetAdminHash retrieves the stored Argon2id admin password hash.
func (s *SQLiteStore) GetAdminHash() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var hash string
	err := s.db.QueryRow("SELECT password_hash FROM admin_credentials WHERE id = 1").Scan(&hash)
	if err != nil {
		return "", err
	}
	return hash, nil
}

// SetAdminHash stores the Argon2id admin password hash.
func (s *SQLiteStore) SetAdminHash(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`INSERT INTO admin_credentials (id, password_hash, updated_at) VALUES (1, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET password_hash = excluded.password_hash, updated_at = excluded.updated_at`,
		hash, time.Now().Format(time.RFC3339),
	)
	return err
}

// GetAdminTOTPSecret retrieves the TOTP secret for admin.
func (s *SQLiteStore) GetAdminTOTPSecret() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var secret sql.NullString
	err := s.db.QueryRow("SELECT totp_secret FROM admin_credentials WHERE id = 1").Scan(&secret)
	if err != nil {
		return "", err
	}
	if !secret.Valid {
		return "", nil
	}
	return secret.String, nil
}

// SetAdminTOTPSecret stores the TOTP secret for admin.
func (s *SQLiteStore) SetAdminTOTPSecret(secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`INSERT INTO admin_credentials (id, password_hash, totp_secret, updated_at) VALUES (1, '', ?, ?)
		 ON CONFLICT(id) DO UPDATE SET totp_secret = excluded.totp_secret, updated_at = excluded.updated_at`,
		secret, time.Now().Format(time.RFC3339),
	)
	return err
}

// ClearAdminTOTPSecret removes the TOTP secret for admin.
func (s *SQLiteStore) ClearAdminTOTPSecret() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`UPDATE admin_credentials SET totp_secret = NULL, updated_at = ? WHERE id = 1`,
		time.Now().Format(time.RFC3339),
	)
	return err
}

// Close closes the underlying SQLite database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// === Session Persistence ===

// CreateSession stores a new session in SQLite.
func (s *SQLiteStore) CreateSession(sessionID, csrfToken string, readOnly bool, createdAt, lastSeen time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`INSERT INTO sessions (id, csrf_token, read_only, created_at, last_seen) VALUES (?, ?, ?, ?, ?)`,
		sessionID, csrfToken, readOnly, createdAt.Format(time.RFC3339), lastSeen.Format(time.RFC3339),
	)
	return err
}

// GetSession retrieves a session by ID.
func (s *SQLiteStore) GetSession(sessionID string) (csrfToken string, readOnly bool, createdAt, lastSeen time.Time, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var csrf, created, lastSeenStr string
	err = s.db.QueryRow(
		`SELECT csrf_token, read_only, created_at, last_seen FROM sessions WHERE id = ?`, sessionID,
	).Scan(&csrf, &readOnly, &created, &lastSeenStr)
	if err != nil {
		return "", false, time.Time{}, time.Time{}, err
	}

	createdAt, _ = time.Parse(time.RFC3339, created)
	lastSeen, _ = time.Parse(time.RFC3339, lastSeenStr)
	return csrf, readOnly, createdAt, lastSeen, nil
}

// UpdateSessionLastSeen updates the last_seen timestamp for a session.
func (s *SQLiteStore) UpdateSessionLastSeen(sessionID string, lastSeen time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`UPDATE sessions SET last_seen = ? WHERE id = ?`,
		lastSeen.Format(time.RFC3339), sessionID,
	)
	return err
}

// DeleteSession removes a session from SQLite.
func (s *SQLiteStore) DeleteSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, sessionID)
	return err
}

// CleanExpiredSessions removes expired sessions (called periodically).
func (s *SQLiteStore) CleanExpiredSessions(idleTimeout, absoluteTimeout time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	idleCutoff := now.Add(-idleTimeout).Format(time.RFC3339)
	absCutoff := now.Add(-absoluteTimeout).Format(time.RFC3339)

	_, err := s.db.Exec(
		`DELETE FROM sessions WHERE last_seen < ? OR created_at < ?`,
		idleCutoff, absCutoff,
	)
	return err
}

// === Rate Limiter Persistence ===

// GetRateLimitBucket retrieves the rate limit bucket for an IP.
func (s *SQLiteStore) GetRateLimitBucket(ip string) (attempts int, windowStart time.Time, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var attemptsVal int
	var windowStartStr string
	err = s.db.QueryRow(
		`SELECT attempts, window_start FROM rate_limit_buckets WHERE ip = ?`, ip,
	).Scan(&attemptsVal, &windowStartStr)
	if err != nil {
		return 0, time.Time{}, err
	}

	windowStart, _ = time.Parse(time.RFC3339, windowStartStr)
	return attemptsVal, windowStart, nil
}

// SetRateLimitBucket creates or updates a rate limit bucket.
func (s *SQLiteStore) SetRateLimitBucket(ip string, attempts int, windowStart time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`INSERT INTO rate_limit_buckets (ip, attempts, window_start) VALUES (?, ?, ?)
		 ON CONFLICT(ip) DO UPDATE SET attempts = excluded.attempts, window_start = excluded.window_start`,
		ip, attempts, windowStart.Format(time.RFC3339),
	)
	return err
}

// CleanExpiredRateLimitBuckets removes old rate limit entries.
func (s *SQLiteStore) CleanExpiredRateLimitBuckets(window time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-window).Format(time.RFC3339)
	_, err := s.db.Exec(`DELETE FROM rate_limit_buckets WHERE window_start < ?`, cutoff)
	return err
}
