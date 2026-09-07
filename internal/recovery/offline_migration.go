package recovery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"golang.org/x/sys/unix"
)

const migrationLimit = 4 << 20

var helperMigrationFiles = []string{"last-good.json", "pending-confirmation.json", "last-transaction.json"}

type migrationBackup struct {
	Present bool   `json:"present"`
	Bytes   []byte `json:"bytes"`
}
type migrationJournal struct {
	Version   int                        `json:"version"`
	DataDir   string                     `json:"data_dir"`
	InputHash string                     `json:"input_hash"`
	Original  config.OfflineConfig       `json:"original"`
	Target    []byte                     `json:"target"`
	Helper    map[string]migrationBackup `json:"helper"`
}
type migrationMarker struct {
	Attempt  string `json:"attempt"`
	Checksum string `json:"checksum"`
}
type migrationPaths struct {
	data, helper, journal, lock string
	uid                         uint32
	stopped                     func() error
	openDB                      func(string) (migrationDB, error)
	// Fault injection at durable boundaries, never exposed by the CLI.
	after func(string) error
}

// MigrateConfig is the explicit local-root, offline configuration replacement.
// An interruption leaves a durable fence; rerun with the identical input file
// to finish forward. It never activates networking or rolls back invalid state.
func MigrateConfig(dataDir, file string) (string, error) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return "", errors.New("configuration migration requires root on the Linux appliance")
	}
	return migrateConfig(migrationPaths{
		data: dataDir, helper: "/var/lib/minimalrouter-applyd", journal: migrationDirectory,
		lock: offlineLockPath, uid: 0, openDB: openMigrationDBWorker, stopped: func() error {
			if err := requireDaemonsStopped("/proc"); err != nil {
				return err
			}
			for _, name := range []string{"routerd", "router-applyd"} {
				_, err := os.Lstat(filepath.Join("/run/openrc/started", name))
				if !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("stop OpenRC service %s before migration", name)
				}
			}
			return nil
		},
	}, file)
}

func migrateConfig(p migrationPaths, file string) (string, error) {
	lock, err := acquireOfflineLock(p.lock, true, p.uid)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	if p.stopped == nil {
		return "", errors.New("missing offline process check")
	}
	if err := p.stopped(); err != nil {
		return "", err
	}
	input, err := readMigrationFile(file, migrationLimit)
	if err != nil {
		return "", err
	}
	var candidate config.SystemConfig
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&candidate); err != nil {
		return "", fmt.Errorf("decode full migration configuration: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "", errors.New("migration file must contain one JSON configuration")
	}
	candidate.MigrateLegacyFields()
	if err := config.ValidateOfflineCandidate(candidate); err != nil {
		return "", fmt.Errorf("migration candidate: %w", err)
	}
	p.data, err = filepath.Abs(p.data)
	if err != nil {
		return "", err
	}
	// Early diagnostics only: SQLite path resolution runs exclusively in the
	// unprivileged worker, so replacing this pathname cannot redirect root I/O.
	if err := requireRegular(filepath.Join(p.data, "minimalrouter.db")); err != nil {
		return "", err
	}
	for _, dir := range []string{p.helper, p.journal} {
		if err := secureDirectory(filepath.Dir(dir), p.uid, false); err != nil {
			return "", err
		}
		if err := os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
		if err := secureDirectory(dir, p.uid, false); err != nil {
			return "", err
		}
	}
	// The fence directory must be searchable by the unprivileged daemon. Only
	// the marker is public; configuration backups live in a private child.
	if err := os.Chmod(p.journal, 0755); err != nil {
		return "", err
	}
	if err := syncMigrationDir(p.journal); err != nil {
		return "", err
	}
	if err := syncMigrationDir(filepath.Dir(p.journal)); err != nil {
		return "", err
	}
	if p.openDB == nil {
		return "", errors.New("missing unprivileged migration database worker")
	}
	store, err := p.openDB(p.data)
	if err != nil {
		return "", err
	}
	defer store.Close()
	markerPath := filepath.Join(p.journal, "pending.json")
	var journal migrationJournal
	var attempt string
	markerBytes, err := readOwnedMigrationFile(markerPath, 4096, p.uid, false)
	if errors.Is(err, os.ErrNotExist) {
		original, err := store.ReadOfflineConfig()
		if err != nil {
			return "", err
		}
		if original.Revision < 1 || original.Revision == math.MaxInt64 {
			return "", errors.New("canonical revision cannot advance")
		}
		if len(original.JSON) > migrationLimit {
			return "", errors.New("original configuration exceeds migration backup limit")
		}
		candidate.Revision = config.Revision(original.Revision + 1)
		candidate.UpdatedAt = time.Now().UTC()
		target, err := json.Marshal(candidate)
		if err != nil {
			return "", err
		}
		if len(target) > migrationLimit {
			return "", errors.New("replacement configuration exceeds migration limit")
		}
		journal = migrationJournal{Version: 1, DataDir: p.data, InputHash: migrationHash(input), Original: original, Target: target, Helper: make(map[string]migrationBackup)}
		for _, name := range helperMigrationFiles {
			data, err := readMigrationFile(filepath.Join(p.helper, name), migrationLimit)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
			journal.Helper[name] = migrationBackup{Present: err == nil, Bytes: data}
		}
		attempt, err = os.MkdirTemp(p.journal, "attempt-")
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(journal)
		if err != nil {
			return "", err
		}
		if err := writeMigrationFile(filepath.Join(attempt, "backup.json"), data, 0600); err != nil {
			return "", err
		}
		if err := syncMigrationDir(p.journal); err != nil {
			return "", err
		}
		markerBytes, _ = json.Marshal(migrationMarker{Attempt: filepath.Base(attempt), Checksum: migrationHash(data)})
		if err := writeMigrationFile(markerPath, markerBytes, 0644); err != nil {
			return "", err
		}
	} else {
		if err != nil {
			return "", err
		}
		var marker migrationMarker
		if err := json.Unmarshal(markerBytes, &marker); err != nil {
			return "", fmt.Errorf("invalid migration fence: %w", err)
		}
		if marker.Attempt == "." || marker.Attempt == "" || filepath.Base(marker.Attempt) != marker.Attempt {
			return "", errors.New("invalid migration backup path")
		}
		attempt = filepath.Join(p.journal, marker.Attempt)
		if err := secureDirectory(attempt, p.uid, true); err != nil {
			return "", err
		}
		data, err := readOwnedMigrationFile(filepath.Join(attempt, "backup.json"), 8*migrationLimit, p.uid, true)
		if err != nil {
			return "", err
		}
		if migrationHash(data) != marker.Checksum {
			return "", errors.New("migration backup checksum mismatch; fence retained")
		}
		if err := json.Unmarshal(data, &journal); err != nil {
			return "", err
		}
		if journal.Version != 1 || journal.DataDir != p.data || journal.InputHash != migrationHash(input) {
			return "", errors.New("resume requires the original migration input and data directory; fence retained")
		}
	}
	step := func(name string) error {
		if p.after != nil {
			return p.after(name)
		}
		return nil
	}
	if err := step("prepared"); err != nil {
		return "", err
	}
	// Idempotent CAS recognizes an already committed target after power loss.
	if err := store.CommitOfflineMigration(journal.Original, journal.Target); err != nil {
		return "", err
	}
	if err := step("database"); err != nil {
		return "", err
	}
	if err := writeMigrationFile(filepath.Join(p.helper, "last-good.json"), journal.Target, 0600); err != nil {
		return "", err
	}
	if err := step("helper"); err != nil {
		return "", err
	}
	for _, name := range helperMigrationFiles[1:] {
		if err := os.Remove(filepath.Join(p.helper, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if err := syncMigrationDir(p.helper); err != nil {
		return "", err
	}
	if err := step("pending-cleared"); err != nil {
		return "", err
	}
	current, err := store.ReadOfflineConfig()
	if err != nil {
		return "", err
	}
	helper, err := readMigrationFile(filepath.Join(p.helper, "last-good.json"), migrationLimit)
	if err != nil {
		return "", err
	}
	if current.Revision != journal.Original.Revision+1 || !bytes.Equal(current.JSON, journal.Target) || !bytes.Equal(helper, journal.Target) {
		return "", errors.New("migration state verification failed; fence retained")
	}
	// Close flushes/checkpoints SQLite before allowing daemon users to reopen it.
	if err := store.Close(); err != nil {
		return "", err
	}
	if err := syncMigrationDir(p.data); err != nil {
		return "", err
	}
	if err := step("verified"); err != nil {
		return "", err
	}
	if err := os.Remove(markerPath); err != nil {
		return "", err
	}
	if err := syncMigrationDir(p.journal); err != nil {
		return "", err
	}
	return attempt, nil
}

func migrationHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func requireRegular(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("migration requires regular file: %s", path)
	}
	return nil
}

func readMigrationFile(path string, limit int64) ([]byte, error) {
	return readCheckedMigrationFile(path, limit, nil, false)
}

func readOwnedMigrationFile(path string, limit int64, uid uint32, private bool) ([]byte, error) {
	return readCheckedMigrationFile(path, limit, &uid, private)
}

func readCheckedMigrationFile(path string, limit int64, uid *uint32, private bool) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	if uid != nil {
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil {
			return nil, err
		}
		mask := uint32(0022)
		if private {
			mask = 0077
		}
		if stat.Uid != *uid || uint32(stat.Mode)&mask != 0 || stat.Nlink != 1 {
			return nil, errors.New("unsafe migration journal file ownership or permissions")
		}
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("migration file must be a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("migration file exceeds size limit")
	}
	return data, err
}

func writeMigrationFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".migration-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncMigrationDir(dir)
}

func syncMigrationDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
