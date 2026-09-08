package recovery

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func offlineFixture(t *testing.T) (migrationPaths, string, []byte) {
	t.Helper()
	root := t.TempDir()
	p := migrationPaths{data: filepath.Join(root, "data"), helper: filepath.Join(root, "helper"), journal: filepath.Join(root, "journal"), lock: filepath.Join(root, "offline.lock"), uid: uint32(os.Geteuid()), stopped: func() error { return nil }}
	p.openDB = func(path string) (migrationDB, error) { return config.OpenOfflineMigrationStore(path) }
	s, err := config.NewStore(p.data)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := s.CreateSession("old-session", "csrf", false, 0, now, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(p.data, "minimalrouter.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("{ invalid legacy JSON preserved verbatim\n")
	if _, err := db.Exec(`UPDATE config_revisions SET config_json=?`, string(raw)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := os.Mkdir(p.helper, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range helperMigrationFiles {
		if err := os.WriteFile(filepath.Join(p.helper, name), []byte("old raw "+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	input, _ := json.Marshal(config.DefaultConfig())
	file := filepath.Join(root, "candidate.json")
	if err := os.WriteFile(file, input, 0600); err != nil {
		t.Fatal(err)
	}
	return p, file, raw
}

func TestOfflineMigrationResumesEveryDurableBoundary(t *testing.T) {
	for _, boundary := range []string{"prepared", "database", "helper", "pending-cleared", "verified"} {
		t.Run(boundary, func(t *testing.T) {
			p, file, raw := offlineFixture(t)
			p.after = func(step string) error {
				if step == boundary {
					return errors.New("simulated power loss")
				}
				return nil
			}
			if _, err := migrateConfig(p, file); err == nil {
				t.Fatal("missing injected failure")
			}
			if _, err := os.Stat(filepath.Join(p.journal, "pending.json")); err != nil {
				t.Fatalf("partial migration lost fence: %v", err)
			}
			p.after = nil
			backup, err := migrateConfig(p, file)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(p.journal, "pending.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("completed migration retained fence")
			}
			info, err := os.Stat(backup)
			if err != nil || info.Mode().Perm() != 0700 {
				t.Fatalf("backup directory permissions: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(backup, "backup.json"))
			if err != nil {
				t.Fatal(err)
			}
			var j migrationJournal
			if err := json.Unmarshal(data, &j); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(j.Original.JSON, raw) {
				t.Fatal("invalid original JSON was normalized or discarded")
			}
			for _, name := range helperMigrationFiles {
				b := j.Helper[name]
				if !b.Present || string(b.Bytes) != "old raw "+name {
					t.Fatalf("lost helper backup %s", name)
				}
			}
			s, err := config.NewStore(p.data)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			current, err := s.GetLatestConfig()
			if err != nil || current.Revision != 2 {
				t.Fatalf("invalid migrated revision: %d %v", current.Revision, err)
			}
			if _, _, _, _, _, err := s.GetSession("old-session"); err == nil {
				t.Fatal("old session survived")
			}
			state, _ := s.ReadOfflineConfig()
			helper, err := os.ReadFile(filepath.Join(p.helper, "last-good.json"))
			if err != nil || !bytes.Equal(helper, state.JSON) {
				t.Fatal("helper/SQLite mismatch")
			}
			for _, name := range helperMigrationFiles[1:] {
				if _, err := os.Stat(filepath.Join(p.helper, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("stale helper transaction survived: %s", name)
				}
			}
		})
	}
}

func TestOfflineMigrationRejectsRunningDaemonAndWrongResumeInput(t *testing.T) {
	p, file, _ := offlineFixture(t)
	p.stopped = func() error { return errors.New("routerd running") }
	if _, err := migrateConfig(p, file); err == nil {
		t.Fatal("online migration accepted")
	}
	if _, err := os.Stat(p.journal); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("online attempt wrote journal")
	}
	p.stopped = func() error { return nil }
	p.after = func(string) error { return errors.New("stop") }
	if _, err := migrateConfig(p, file); err == nil {
		t.Fatal("expected failure")
	}
	data, _ := os.ReadFile(file)
	data = append(data, '\n')
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	p.after = nil
	if _, err := migrateConfig(p, file); err == nil {
		t.Fatal("different resume input accepted")
	}
	if _, err := os.Stat(filepath.Join(p.journal, "pending.json")); err != nil {
		t.Fatal("resume failure removed fence")
	}
}

func TestOfflineMigrationRejectsInvalidCandidateBeforeFence(t *testing.T) {
	p, file, _ := offlineFixture(t)
	cfg := config.DefaultConfig()
	cfg.LAN.IPAddress = "invalid"
	data, _ := json.Marshal(cfg)
	os.WriteFile(file, data, 0600)
	if _, err := migrateConfig(p, file); err == nil {
		t.Fatal("invalid candidate accepted")
	}
	if _, err := os.Stat(p.journal); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid input wrote journal")
	}
}

func TestOfflineGuardExcludesDaemonAndRejectsUnsafeLock(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "guard")
	uid := uint32(os.Geteuid())
	shared, err := acquireOfflineLock(path, false, uid)
	if err != nil {
		t.Fatal(err)
	}
	if f, err := acquireOfflineLock(path, true, uid); err == nil {
		f.Close()
		t.Fatal("exclusive migration overlapped daemon")
	}
	shared.Close()
	exclusive, err := acquireOfflineLock(path, true, uid)
	if err != nil {
		t.Fatal(err)
	}
	if f, err := acquireOfflineLock(path, false, uid); err == nil {
		f.Close()
		t.Fatal("daemon overlapped migration")
	}
	exclusive.Close()
	os.Remove(path)
	os.Symlink(filepath.Join(root, "target"), path)
	if f, err := acquireOfflineLock(path, true, uid); err == nil {
		f.Close()
		t.Fatal("symlink lock accepted")
	}
	os.Remove(path)
	os.Chmod(root, 0777)
	if f, err := acquireOfflineLock(path, true, uid); err == nil {
		f.Close()
		t.Fatal("writable guard directory accepted")
	}
}

func TestOfflineProcessCheckFindsLegacyDaemons(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "123"), 0700)
	os.WriteFile(filepath.Join(root, "123", "comm"), []byte("router-applyd\n"), 0600)
	if err := requireDaemonsStopped(root); err == nil {
		t.Fatal("unguarded legacy daemon accepted")
	}
	os.WriteFile(filepath.Join(root, "123", "comm"), []byte("unrelated\n"), 0600)
	if err := requireDaemonsStopped(root); err != nil {
		t.Fatal(err)
	}
}

func TestOfflineFenceRejectsPartialCorruptAndUnsafeState(t *testing.T) {
	root := t.TempDir()
	uid := uint32(os.Geteuid())
	if err := inspectMigrationFence(root, uid); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "pending.json")
	if err := os.WriteFile(marker, []byte("corrupt marker"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := inspectMigrationFence(root, uid); err == nil {
		t.Fatal("corrupt journal allowed daemon startup")
	}
	os.Remove(marker)
	os.Symlink(filepath.Join(root, "missing"), marker)
	if err := inspectMigrationFence(root, uid); err == nil {
		t.Fatal("dangling marker symlink allowed startup")
	}
	os.Remove(marker)
	os.Chmod(root, 0777)
	if err := inspectMigrationFence(root, uid); err == nil {
		t.Fatal("writable journal directory allowed startup")
	}
}

func TestOfflineMigrationRetainsFenceOnCorruptBackup(t *testing.T) {
	p, file, _ := offlineFixture(t)
	p.after = func(string) error { return errors.New("stop") }
	if _, err := migrateConfig(p, file); err == nil {
		t.Fatal("expected interruption")
	}
	data, err := os.ReadFile(filepath.Join(p.journal, "pending.json"))
	if err != nil {
		t.Fatal(err)
	}
	var marker migrationMarker
	json.Unmarshal(data, &marker)
	if err := os.WriteFile(filepath.Join(p.journal, marker.Attempt, "backup.json"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	p.after = nil
	if _, err := migrateConfig(p, file); err == nil {
		t.Fatal("corrupt backup resumed")
	}
	if err := inspectMigrationFence(p.journal, p.uid); err == nil {
		t.Fatal("corrupt backup removed startup fence")
	}
}

func TestOfflineMigrationEnforcesScenarioSafety(t *testing.T) {
	p, file, _ := offlineFixture(t)
	cfg := config.DefaultConfig()
	cfg.System.Domain = "a..b"
	if err := cfg.ValidateScenarioSafety(); err == nil {
		t.Fatal("fixture lacks scenario violation")
	}
	data, _ := json.Marshal(cfg)
	os.WriteFile(file, data, 0600)
	if _, err := migrateConfig(p, file); err == nil {
		t.Fatal("unsafe scenario accepted")
	}
	if _, err := os.Stat(p.journal); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unsafe scenario wrote journal")
	}
}

func TestOfflineMigrationRedactedExportCannotReplacePasswords(t *testing.T) {
	p, file, raw := offlineFixture(t)
	cfg := config.DefaultConfig()
	cfg.WAN.Password = "[REDACTED]"
	cfg.WiFi.Passphrase = "[REDACTED]"
	cfg.SquidProxy.Password = "[REDACTED]"
	data, _ := json.Marshal(cfg)
	os.WriteFile(file, data, 0600)
	if _, err := migrateConfig(p, file); err == nil {
		t.Fatal("redacted configuration accepted")
	}
	if _, err := os.Stat(p.journal); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("redacted input wrote journal")
	}
	s, err := config.NewStore(p.data)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state, err := s.ReadOfflineConfig()
	if err != nil || !bytes.Equal(state.JSON, raw) {
		t.Fatal("redacted input changed canonical configuration")
	}
}

func TestMigrationJournalRequiresPrivateOwnershipAndPermissions(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "backup.json")
	uid := uint32(os.Geteuid())
	os.WriteFile(path, []byte("private"), 0600)
	if _, err := readOwnedMigrationFile(path, 100, uid, true); err != nil {
		t.Fatal(err)
	}
	if _, err := readOwnedMigrationFile(path, 100, uid+1, true); err == nil {
		t.Fatal("foreign journal owner accepted")
	}
	os.Chmod(path, 0644)
	if _, err := readOwnedMigrationFile(path, 100, uid, true); err == nil {
		t.Fatal("public backup accepted")
	}
	if _, err := readOwnedMigrationFile(path, 100, uid, false); err != nil {
		t.Fatal("public nonsensitive marker rejected")
	}
	os.Chmod(path, 0666)
	if _, err := readOwnedMigrationFile(path, 100, uid, false); err == nil {
		t.Fatal("writable marker accepted")
	}
}
