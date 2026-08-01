package config

import (
	"fmt"
	"path/filepath"

	"github.com/vladimirperovic/minimalrouter/internal/storage"
)

func (s *SQLiteStore) StorageStatus() storage.Status {
	if s == nil || s.db == nil {
		return storage.Evaluate(0, 0)
	}
	rows, err := s.db.Query(`PRAGMA database_list`)
	if err != nil {
		return storage.Evaluate(0, 0)
	}
	defer rows.Close()
	for rows.Next() {
		var sequence int
		var name, path string
		if err := rows.Scan(&sequence, &name, &path); err != nil {
			return storage.Evaluate(0, 0)
		}
		if name == "main" && path != "" {
			return storage.Inspect(filepath.Dir(path))
		}
	}
	return storage.Evaluate(0, 0)
}

// MaintainStorage reapplies all bounded-retention guarantees and checkpoints
// the WAL without vacuuming or taking the router offline.
func (s *SQLiteStore) MaintainStorage() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("configuration store is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin storage maintenance: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	statements := []string{
		`DELETE FROM config_revisions WHERE revision NOT IN (SELECT revision FROM config_revisions ORDER BY revision DESC LIMIT 100)`,
		`DELETE FROM snapshots WHERE id NOT IN (SELECT id FROM snapshots ORDER BY created_at DESC, id DESC LIMIT 20)`,
		`DELETE FROM audit_events WHERE id NOT IN (SELECT id FROM audit_events ORDER BY timestamp DESC, id DESC LIMIT 5000)`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("prune bounded local state: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit storage maintenance: %w", err)
	}
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(PASSIVE)`); err != nil {
		return fmt.Errorf("checkpoint SQLite WAL: %w", err)
	}
	return nil
}
