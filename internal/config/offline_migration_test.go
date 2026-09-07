package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOfflineMigrationRepairsInvalidRawAndIsIdempotent(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw := []byte(`{"revision":1,"legacy":"preserve exactly"}`)
	if _, err := s.db.Exec(`UPDATE config_revisions SET config_json=?`, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetLatestConfig(); err == nil {
		t.Fatal("fixture must be invalid")
	}
	old, err := s.ReadOfflineConfig()
	if err != nil || !bytes.Equal(old.JSON, raw) {
		t.Fatalf("raw baseline lost: %v", err)
	}
	cfg := DefaultConfig()
	cfg.Revision = 2
	cfg.UpdatedAt = time.Now().UTC()
	target, _ := json.Marshal(cfg)
	if err := s.CommitOfflineMigration(old, target); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitOfflineMigration(old, target); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetLatestConfig()
	if err != nil || current.Revision != 2 {
		t.Fatalf("unexpected migration result: %+v %v", current, err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("retry added revision: %d %v", count, err)
	}
}

func TestOfflineMigrationRejectsInvalidTargetAndStaleSource(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old, _ := s.ReadOfflineConfig()
	cfg := DefaultConfig()
	cfg.Revision = 2
	cfg.LAN.IPAddress = "invalid"
	target, _ := json.Marshal(cfg)
	if err := s.CommitOfflineMigration(old, target); err == nil {
		t.Fatal("invalid target accepted")
	}
	cfg = DefaultConfig()
	cfg.Revision = 3
	target, _ = json.Marshal(cfg)
	if err := s.CommitOfflineMigration(old, target); err == nil {
		t.Fatal("revision skip accepted")
	}
	cfg.Revision = 2
	target, _ = json.Marshal(cfg)
	if _, err := s.db.Exec(`UPDATE config_revisions SET config_json='changed'`); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitOfflineMigration(old, target); err == nil {
		t.Fatal("stale raw source accepted")
	}
}

func TestOfflineMigrationSessionFailureRollsBackRevision(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old, _ := s.ReadOfflineConfig()
	now := time.Now()
	if err := s.CreateSession("test-session", "csrf", false, 0, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER deny_delete BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT, 'denied'); END`); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Revision = 2
	target, _ := json.Marshal(cfg)
	if err := s.CommitOfflineMigration(old, target); err == nil {
		t.Fatal("session failure ignored")
	}
	current, _ := s.ReadOfflineConfig()
	if current.Revision != old.Revision || !bytes.Equal(current.JSON, old.JSON) {
		t.Fatal("partial SQLite migration committed")
	}
}

func TestOfflineCandidateRejectsEveryRedactedSecret(t *testing.T) {
	mutations := map[string]func(*SystemConfig){
		"wan":               func(c *SystemConfig) { c.WAN.Password = "[REDACTED]" },
		"wifi":              func(c *SystemConfig) { c.WiFi.Passphrase = "[REDACTED]" },
		"squid":             func(c *SystemConfig) { c.SquidProxy.Password = "[REDACTED]" },
		"wg-server":         func(c *SystemConfig) { c.WireGuard.PrivateKey = "[REDACTED]" },
		"wg-client":         func(c *SystemConfig) { c.WGClient.PrivateKey = "[REDACTED]" },
		"wg-client-psk":     func(c *SystemConfig) { c.WGClient.PresharedKey = "[REDACTED]" },
		"wg-peer-psk":       func(c *SystemConfig) { c.WireGuard.Peers = []WireGuardPeer{{PresharedKey: "[REDACTED]"}} },
		"cloudflare-api":    func(c *SystemConfig) { c.Cloudflare.APIToken = "[REDACTED]" },
		"cloudflare-tunnel": func(c *SystemConfig) { c.Cloudflare.TunnelToken = " [REDACTED] " },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig()
			mutate(&cfg)
			if err := ValidateOfflineCandidate(cfg); err == nil || !strings.Contains(err.Error(), "redaction placeholder") {
				t.Fatalf("placeholder not explicitly rejected: %v", err)
			}
		})
	}
}

func TestOfflineStoreNeverInitializesMissingOrEmptyDatabase(t *testing.T) {
	root := t.TempDir()
	if s, err := OpenOfflineMigrationStore(root); err == nil {
		s.Close()
		t.Fatal("missing database initialized")
	}
	path := filepath.Join(root, "minimalrouter.db")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("missing database created")
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenOfflineMigrationStore(root); err == nil {
		s.Close()
		t.Fatal("empty database initialized")
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != 0 {
		t.Fatal("empty database mutated")
	}
}
