package config

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"
)

func ValidateSnapshotLabel(label string) error {
	if !utf8.ValidString(label) || utf8.RuneCountInString(label) > 80 {
		return fmt.Errorf("snapshot name must be at most 80 characters")
	}
	for _, char := range label {
		if unicode.IsControl(char) {
			return fmt.Errorf("snapshot name cannot contain control characters")
		}
	}
	return nil
}

func (s *SQLiteStore) LastBackupExport() (*time.Time, error) {
	var at time.Time
	err := s.db.QueryRow(`SELECT exported_at FROM backup_export_status WHERE id=1`).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &at, nil
}

// Recovery state contains operation metadata and a DNS category policy only.
// Never store candidate configurations, import tokens, credentials or sessions.
func (s *SQLiteStore) SaveRecoveryOperation(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > 128<<10 {
		return fmt.Errorf("recovery operation too large")
	}
	_, err = s.db.Exec(`INSERT INTO recovery_state(key,value) VALUES('operation',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(data))
	return err
}

func (s *SQLiteStore) ReadRecoveryOperation(value any) error {
	var data string
	err := s.db.QueryRow(`SELECT value FROM recovery_state WHERE key='operation'`).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(data), value)
}
