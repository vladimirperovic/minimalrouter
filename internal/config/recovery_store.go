package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// RecoveryResetAuthentication changes administrator credentials and revokes all
// sessions in one SQLite transaction. A failure leaves the old credentials and
// sessions intact instead of creating a partially recovered state.
func (s *SQLiteStore) RecoveryResetAuthentication(passwordHash string, disableTOTP bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin authentication recovery: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	now := time.Now().UTC().Format(time.RFC3339)
	if disableTOTP {
		_, err = tx.Exec(`
			INSERT INTO admin_credentials (id, password_hash, totp_secret, updated_at)
			VALUES (1, ?, NULL, ?)
			ON CONFLICT(id) DO UPDATE SET
				password_hash = excluded.password_hash,
				totp_secret = NULL,
				updated_at = excluded.updated_at`, passwordHash, now)
	} else {
		_, err = tx.Exec(`
			INSERT INTO admin_credentials (id, password_hash, updated_at)
			VALUES (1, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				password_hash = excluded.password_hash,
				updated_at = excluded.updated_at`, passwordHash, now)
	}
	if err != nil {
		return fmt.Errorf("store recovered administrator credentials: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM sessions`); err != nil {
		return fmt.Errorf("revoke sessions during authentication recovery: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit authentication recovery: %w", err)
	}
	return nil
}

// RecoverySaveConfig atomically creates an undo snapshot, stores one validated
// configuration revision, optionally resets administrator credentials/TOTP,
// and revokes all sessions. It is used only by the local recovery console.
func (s *SQLiteStore) RecoverySaveConfig(current, next SystemConfig, passwordHash *string, clearTOTP bool) (Snapshot, error) {
	if err := current.Validate(); err != nil {
		return Snapshot{}, fmt.Errorf("current recovery configuration is invalid: %w", err)
	}
	if err := next.Validate(); err != nil {
		return Snapshot{}, fmt.Errorf("recovery configuration is invalid: %w", err)
	}
	if next.Revision != current.Revision+1 {
		return Snapshot{}, fmt.Errorf("recovery configuration revision must advance exactly once")
	}

	currentData, err := json.Marshal(current)
	if err != nil {
		return Snapshot{}, fmt.Errorf("marshal recovery snapshot: %w", err)
	}
	next.UpdatedAt = time.Now().UTC()
	nextData, err := json.Marshal(next)
	if err != nil {
		return Snapshot{}, fmt.Errorf("marshal recovered configuration: %w", err)
	}
	hash := sha256.Sum256(currentData)
	snapshot := Snapshot{
		ID:         fmt.Sprintf("snap-%d", time.Now().UnixNano()),
		Revision:   current.Revision,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		Checksum:   hex.EncodeToString(hash[:]),
		ConfigJSON: string(currentData),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return Snapshot{}, fmt.Errorf("begin recovery transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var storedJSON string
	if err := tx.QueryRow(`SELECT config_json FROM config_revisions ORDER BY revision DESC LIMIT 1`).Scan(&storedJSON); err != nil {
		return Snapshot{}, fmt.Errorf("inspect current recovery revision: %w", err)
	}
	var stored SystemConfig
	if err := json.Unmarshal([]byte(storedJSON), &stored); err != nil || stored.Revision != current.Revision {
		return Snapshot{}, fmt.Errorf("canonical configuration changed during recovery")
	}

	if _, err := tx.Exec(
		`INSERT INTO snapshots (id, revision, created_at, checksum, config_json) VALUES (?, ?, ?, ?, ?)`,
		snapshot.ID, int64(snapshot.Revision), snapshot.CreatedAt, snapshot.Checksum, snapshot.ConfigJSON,
	); err != nil {
		return Snapshot{}, fmt.Errorf("insert recovery snapshot: %w", err)
	}
	if _, err := tx.Exec(`
		DELETE FROM snapshots
		WHERE id NOT IN (
			SELECT id FROM snapshots ORDER BY created_at DESC, id DESC LIMIT 20
		)`); err != nil {
		return Snapshot{}, fmt.Errorf("prune recovery snapshots: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO config_revisions (updated_at, config_json) VALUES (?, ?)`,
		next.UpdatedAt.Format(time.RFC3339), string(nextData),
	); err != nil {
		return Snapshot{}, fmt.Errorf("insert recovered configuration: %w", err)
	}
	if _, err := tx.Exec(`
		DELETE FROM config_revisions
		WHERE revision NOT IN (
			SELECT revision FROM config_revisions ORDER BY revision DESC LIMIT 100
		)`); err != nil {
		return Snapshot{}, fmt.Errorf("prune recovered configuration revisions: %w", err)
	}

	if passwordHash != nil {
		now := time.Now().UTC().Format(time.RFC3339)
		if clearTOTP {
			_, err = tx.Exec(`
				INSERT INTO admin_credentials (id, password_hash, totp_secret, updated_at)
				VALUES (1, ?, NULL, ?)
				ON CONFLICT(id) DO UPDATE SET
					password_hash = excluded.password_hash,
					totp_secret = NULL,
					updated_at = excluded.updated_at`, *passwordHash, now)
		} else {
			_, err = tx.Exec(`
				INSERT INTO admin_credentials (id, password_hash, updated_at)
				VALUES (1, ?, ?)
				ON CONFLICT(id) DO UPDATE SET
					password_hash = excluded.password_hash,
					updated_at = excluded.updated_at`, *passwordHash, now)
		}
		if err != nil {
			return Snapshot{}, fmt.Errorf("store factory-reset administrator credentials: %w", err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM sessions`); err != nil {
		return Snapshot{}, fmt.Errorf("revoke sessions during recovery: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Snapshot{}, fmt.Errorf("commit recovery transaction: %w", err)
	}
	return snapshot, nil
}
