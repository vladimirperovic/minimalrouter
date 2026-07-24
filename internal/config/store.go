package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Snapshot represents an immutable, checksummed point-in-time configuration state.
type Snapshot struct {
	ID         string       `json:"id"`
	Revision   Revision     `json:"revision"`
	CreatedAt  time.Time    `json:"created_at"`
	Checksum   string       `json:"checksum"`
	ConfigJSON string       `json:"config_json"`
}

// FileStore provides durable transactional JSON configuration and snapshot storage.
type FileStore struct {
	mu       sync.RWMutex
	dir      string
	dbFile   string
	snapDir  string
}

// NewFileStore initializes the store at the given directory path.
func NewFileStore(dirPath string) (*FileStore, error) {
	if err := os.MkdirAll(filepath.Join(dirPath, "snapshots"), 0700); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	store := &FileStore{
		dir:     dirPath,
		dbFile:  filepath.Join(dirPath, "current_config.json"),
		snapDir: filepath.Join(dirPath, "snapshots"),
	}

	// Initialize default config if no file exists
	if _, err := os.Stat(store.dbFile); os.IsNotExist(err) {
		defaultCfg := DefaultConfig()
		if err := store.SaveConfig(defaultCfg); err != nil {
			return nil, fmt.Errorf("failed to initialize default config store: %w", err)
		}
	}

	return store, nil
}

// GetLatestConfig reads the active canonical configuration.
func (s *FileStore) GetLatestConfig() (SystemConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.dbFile)
	if err != nil {
		return SystemConfig{}, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg SystemConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return SystemConfig{}, fmt.Errorf("failed to parse config JSON: %w", err)
	}

	return cfg, nil
}

// SaveConfig atomically writes updated configuration to store.
func (s *FileStore) SaveConfig(cfg SystemConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}

	tmpFile := s.dbFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write tmp config: %w", err)
	}

	if err := os.Rename(tmpFile, s.dbFile); err != nil {
		return fmt.Errorf("failed to commit atomic config file: %w", err)
	}

	return nil
}

// CreateSnapshot creates an immutable checksummed snapshot of the current configuration.
func (s *FileStore) CreateSnapshot(cfg SystemConfig) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(cfg)
	if err != nil {
		return Snapshot{}, fmt.Errorf("failed to marshal config for snapshot: %w", err)
	}

	hash := sha256.Sum256(data)
	checksum := hex.EncodeToString(hash[:])
	id := fmt.Sprintf("snap-%d", time.Now().UnixNano())

	snap := Snapshot{
		ID:         id,
		Revision:   cfg.Revision,
		CreatedAt:  time.Now(),
		Checksum:   checksum,
		ConfigJSON: string(data),
	}

	snapData, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return Snapshot{}, err
	}

	snapFile := filepath.Join(s.snapDir, id+".json")
	if err := os.WriteFile(snapFile, snapData, 0600); err != nil {
		return Snapshot{}, fmt.Errorf("failed to write snapshot file: %w", err)
	}

	return snap, nil
}

// ListSnapshots returns all stored snapshots.
func (s *FileStore) ListSnapshots() ([]Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	files, err := os.ReadDir(s.snapDir)
	if err != nil {
		return nil, err
	}

	var snapshots []Snapshot
	for _, f := range files {
		if filepath.Ext(f.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.snapDir, f.Name()))
		if err != nil {
			continue
		}
		var snap Snapshot
		if err := json.Unmarshal(data, &snap); err == nil {
			snapshots = append(snapshots, snap)
		}
	}

	return snapshots, nil
}
