package config

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	dbPath := dirPath + "/minimalrouter.db"

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database at %s: %w", dbPath, err)
	}

	// Enable WAL mode for concurrent readers + single writer
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to set WAL mode: %w", err)
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
	_, err := db.Exec(schema)
	return err
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

	return cfg, nil
}

// SaveConfig saves updated configuration to the SQLite config_revisions table.
// Uses an atomic INSERT within a transaction for crash safety.
func (s *SQLiteStore) SaveConfig(cfg SystemConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	return snap, nil
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
func (s *SQLiteStore) CreateSession(sessionID, csrfToken string, createdAt, lastSeen time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`INSERT INTO sessions (id, csrf_token, created_at, last_seen) VALUES (?, ?, ?, ?)`,
		sessionID, csrfToken, createdAt.Format(time.RFC3339), lastSeen.Format(time.RFC3339),
	)
	return err
}

// GetSession retrieves a session by ID.
func (s *SQLiteStore) GetSession(sessionID string) (csrfToken string, createdAt, lastSeen time.Time, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var csrf, created, lastSeenStr string
	err = s.db.QueryRow(
		`SELECT csrf_token, created_at, last_seen FROM sessions WHERE id = ?`, sessionID,
	).Scan(&csrf, &created, &lastSeenStr)
	if err != nil {
		return "", time.Time{}, time.Time{}, err
	}

	createdAt, _ = time.Parse(time.RFC3339, created)
	lastSeen, _ = time.Parse(time.RFC3339, lastSeenStr)
	return csrf, createdAt, lastSeen, nil
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
