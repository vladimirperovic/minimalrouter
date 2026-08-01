package gateway

import (
	"fmt"
	"path/filepath"

	"github.com/vladimirperovic/minimalrouter/internal/storage"
)

func (s *Store) storageDirectory() (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("gateway store is unavailable")
	}
	rows, err := s.db.Query(`PRAGMA database_list`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var sequence int
		var name, path string
		if err := rows.Scan(&sequence, &name, &path); err != nil {
			return "", err
		}
		if name == "main" && path != "" {
			return filepath.Dir(path), nil
		}
	}
	return "", rows.Err()
}

func (s *Store) StorageStatus() storage.Status {
	dir, err := s.storageDirectory()
	if err != nil {
		return storage.Evaluate(0, 0)
	}
	return storage.Inspect(dir)
}

func (s *Store) nonessentialWritesAllowed() bool {
	status := s.StorageStatus()
	return !status.Available || status.NonessentialWritesAllowed
}

func (s *Store) requireDurableWrite() error {
	status := s.StorageStatus()
	if status.Available && !status.DurableWritesAllowed {
		return fmt.Errorf("%w: gateway settings", storage.ErrCriticalPressure)
	}
	return nil
}

func (s *Store) checkpointIfPressured() {
	status := s.StorageStatus()
	if !status.Available || status.Level == storage.PressureNormal {
		return
	}
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(PASSIVE)`)
}
