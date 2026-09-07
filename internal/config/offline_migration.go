package config

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"strings"
)

// OfflineConfig preserves the exact canonical bytes, including invalid legacy
// JSON. Only the root offline recovery protocol may use this validation bypass.
type OfflineConfig struct {
	Revision int64  `json:"revision"`
	JSON     []byte `json:"json"`
}

// OpenOfflineMigrationStore opens an existing database without schema migration,
// default initialization, or permission changes before the durable fence exists.
func OpenOfflineMigrationStore(dir string) (*SQLiteStore, error) {
	path, err := filepath.Abs(filepath.Join(dir, "minimalrouter.db"))
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite", u.String()+"?mode=rw&_pragma=synchronous(FULL)&_pragma=foreign_keys(ON)&_pragma=trusted_schema(OFF)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var integrity string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&integrity); err != nil || integrity != "ok" {
		db.Close()
		return nil, fmt.Errorf("offline database integrity check failed")
	}
	store := &SQLiteStore{db: db}
	if _, err := store.ReadOfflineConfig(); err != nil {
		db.Close()
		return nil, fmt.Errorf("offline migration requires an existing canonical revision: %w", err)
	}
	return store, nil
}

// ValidateOfflineCandidate requires real private values even for disabled
// features. API redaction sentinels are never interpreted as literal secrets or
// silently restored from a potentially invalid legacy configuration.
func ValidateOfflineCandidate(cfg SystemConfig) error {
	secrets := map[string]string{
		"wan.password":            cfg.WAN.Password,
		"wireguard.private_key":   cfg.WireGuard.PrivateKey,
		"wg_client.private_key":   cfg.WGClient.PrivateKey,
		"wg_client.preshared_key": cfg.WGClient.PresharedKey,
		"cloudflare.api_token":    cfg.Cloudflare.APIToken,
		"cloudflare.tunnel_token": cfg.Cloudflare.TunnelToken,
		"squid_proxy.password":    cfg.SquidProxy.Password,
		"wifi.passphrase":         cfg.WiFi.Passphrase,
	}
	for i, peer := range cfg.WireGuard.Peers {
		secrets[fmt.Sprintf("wireguard.peers[%d].preshared_key", i)] = peer.PresharedKey
	}
	for field, value := range secrets {
		if strings.TrimSpace(value) == "[REDACTED]" {
			return fmt.Errorf("%s contains a redaction placeholder; supply a complete private configuration", field)
		}
	}
	return ValidateLiveCandidate(cfg, nil)
}

func (s *SQLiteStore) ReadOfflineConfig() (OfflineConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var state OfflineConfig
	err := s.db.QueryRow(`SELECT revision, config_json FROM config_revisions ORDER BY revision DESC LIMIT 1`).Scan(&state.Revision, &state.JSON)
	return state, err
}

// CommitOfflineMigration is an idempotent compare-and-swap for a previously
// journaled, fully validated replacement. It never re-admits the invalid source.
// The caller must hold the offline lock and durable startup fence throughout.
func (s *SQLiteStore) CommitOfflineMigration(old OfflineConfig, target []byte) error {
	var cfg SystemConfig
	if err := json.Unmarshal(target, &cfg); err != nil {
		return err
	}
	if err := ValidateOfflineCandidate(cfg); err != nil {
		return err
	}
	if old.Revision < 1 || old.Revision == math.MaxInt64 || cfg.Revision != Revision(old.Revision+1) {
		return fmt.Errorf("offline migration revision must advance exactly once")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current OfflineConfig
	if err := tx.QueryRow(`SELECT revision, config_json FROM config_revisions ORDER BY revision DESC LIMIT 1`).Scan(&current.Revision, &current.JSON); err != nil {
		return err
	}
	if current.Revision == old.Revision+1 && bytes.Equal(current.JSON, target) {
		return nil // Prior attempt committed before its caller observed success.
	}
	if current.Revision != old.Revision || !bytes.Equal(current.JSON, old.JSON) {
		return fmt.Errorf("canonical configuration changed since offline migration was prepared")
	}
	// Explicit row IDs keep the JSON revision and SQLite revision identical.
	if _, err := tx.Exec(`INSERT INTO config_revisions (revision, updated_at, config_json) VALUES (?, ?, ?)`, old.Revision+1, cfg.UpdatedAt, string(target)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions`); err != nil {
		return err
	}
	return tx.Commit()
}
