package recovery

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// Real child/anonymous-pipe coverage on developer hosts. Production credential
// enforcement is exercised separately by the Linux root integration tests.
func TestMigrationWorkerChild(t *testing.T) {
	if os.Getenv("MINIMALROUTER_TEST_WORKER") != "1" {
		return
	}
	if os.Getenv("MINIMALROUTER_TEST_HOLD_PIPES") == "1" {
		var req workerRequest
		if err := readWorkerFrame(os.Stdin, &req); err != nil {
			os.Exit(2)
		}
		if err := writeWorkerFrame(os.Stdout, workerResponse{}); err != nil {
			os.Exit(3)
		}
		descendant := exec.Command("/bin/sleep", "2")
		descendant.Stdin = os.Stdin
		descendant.Stdout = os.Stdout
		descendant.Stderr = os.Stderr
		if err := descendant.Start(); err != nil {
			os.Exit(4)
		}
		os.Exit(0)
	}
	if err := serveMigrationDBWorker(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func testMigrationWorker(t *testing.T, dir string) (*migrationDBWorker, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestMigrationWorkerChild$")
	cmd.Env = []string{"MINIMALROUTER_TEST_WORKER=1"}
	return openWorkerProcess(cmd, dir)
}

func TestMigrationWorkerPrivatePipeReadCASVerify(t *testing.T) {
	dir := t.TempDir()
	s, err := config.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.ReadOfflineConfig()
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	w, err := testMigrationWorker(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	got, err := w.ReadOfflineConfig()
	if err != nil || !bytes.Equal(got.JSON, old.JSON) {
		t.Fatalf("worker read: %v", err)
	}
	cfg := config.DefaultConfig()
	cfg.Revision = 2
	cfg.UpdatedAt = time.Now().UTC()
	cfg.WAN.Password = "private-pipe-fixture"
	target, _ := json.Marshal(cfg)
	if err := w.CommitOfflineMigration(old, target); err != nil {
		t.Fatal(err)
	}
	if err := w.CommitOfflineMigration(old, target); err != nil {
		t.Fatalf("retry not idempotent: %v", err)
	}
	got, err = w.ReadOfflineConfig()
	if err != nil || !bytes.Equal(got.JSON, target) {
		t.Fatalf("worker verification: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for _, arg := range append(w.cmd.Args, w.cmd.Env...) {
		if bytes.Contains([]byte(arg), []byte(cfg.WAN.Password)) {
			t.Fatal("private candidate leaked into args/env")
		}
	}
}

func TestMigrationWorkerRejectsConcurrentCanonicalWriter(t *testing.T) {
	dir := t.TempDir()
	s, err := config.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w, err := testMigrationWorker(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	old, err := w.ReadOfflineConfig()
	if err != nil {
		t.Fatal(err)
	}
	other := config.DefaultConfig()
	other.Revision = 2
	other.System.Hostname = "other-writer"
	if err := s.SaveConfig(other); err != nil {
		t.Fatal(err)
	}
	target := config.DefaultConfig()
	target.Revision = 2
	data, _ := json.Marshal(target)
	if err := w.CommitOfflineMigration(old, data); err == nil {
		t.Fatal("concurrent writer was overwritten")
	}
}

func TestMigrationWorkerCrashRetainsFenceAndCanResume(t *testing.T) {
	p, file, _ := offlineFixture(t)
	var worker *migrationDBWorker
	p.openDB = func(dir string) (migrationDB, error) {
		var err error
		worker, err = testMigrationWorker(t, dir)
		return worker, err
	}
	p.after = func(step string) error {
		if step == "database" {
			worker.cmd.Process.Kill()
			return errors.New("worker crash after commit")
		}
		return nil
	}
	if _, err := migrateConfig(p, file); err == nil {
		t.Fatal("worker crash ignored")
	}
	if err := inspectMigrationFence(p.journal, p.uid); err == nil {
		t.Fatal("worker crash removed fence")
	}
	p.after = nil
	if _, err := migrateConfig(p, file); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationWorkerFrameBounds(t *testing.T) {
	for _, size := range []uint32{0, workerFrameLimit + 1} {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], size)
		if err := readWorkerFrame(bytes.NewReader(header[:]), new(workerRequest)); err == nil {
			t.Fatal("invalid frame allocation accepted")
		}
	}
	var data bytes.Buffer
	if err := writeWorkerFrame(&data, workerRequest{Operation: "read"}); err != nil {
		t.Fatal(err)
	}
	truncated := data.Bytes()[:data.Len()-1]
	if err := readWorkerFrame(bytes.NewReader(truncated), new(workerRequest)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated frame accepted: %v", err)
	}
}

func TestMigrationWorkerDoesNotInitializeMissingDatabase(t *testing.T) {
	dir := t.TempDir()
	if w, err := testMigrationWorker(t, dir); err == nil {
		w.Close()
		t.Fatal("missing database initialized")
	}
	if _, err := os.Stat(filepath.Join(dir, "minimalrouter.db")); !os.IsNotExist(err) {
		t.Fatal("worker created database")
	}
}

func TestMigrationWorkerTimeoutClosesInheritedPipes(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestMigrationWorkerChild$")
	cmd.Env = []string{"MINIMALROUTER_TEST_WORKER=1", "MINIMALROUTER_TEST_HOLD_PIPES=1"}
	w, err := openWorkerProcess(cmd, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w.timeout = 100 * time.Millisecond
	start := time.Now()
	if _, err := w.ReadOfflineConfig(); err == nil {
		t.Fatal("dead worker replied")
	}
	_ = w.Close()
	if time.Since(start) > time.Second {
		t.Fatal("inherited descriptors blocked timeout/reaping")
	}
}
