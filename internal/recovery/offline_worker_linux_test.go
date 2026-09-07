//go:build linux

package recovery

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"golang.org/x/sys/unix"
)

func TestMigrationRestrictedWorkerChild(t *testing.T) {
	if os.Getenv("MINIMALROUTER_RESTRICTED_TEST_WORKER") != "1" {
		return
	}
	uid, _ := strconv.ParseUint(os.Getenv("MINIMALROUTER_TEST_UID"), 10, 32)
	gid, _ := strconv.ParseUint(os.Getenv("MINIMALROUTER_TEST_GID"), 10, 32)
	if err := restrictMigrationDBWorker(uint32(uid), uint32(gid)); err != nil {
		os.Exit(2)
	}
	if unix.Setuid(0) == nil {
		os.Exit(3)
	}
	if _, err := os.ReadFile(os.Getenv("MINIMALROUTER_TEST_PRIVATE_FILE")); !errors.Is(err, os.ErrPermission) {
		os.Exit(4)
	}
	if err := serveMigrationDBWorker(os.Stdin, os.Stdout); err != nil {
		os.Exit(5)
	}
	os.Exit(0)
}

func TestMigrationWorkerLinuxPrivilegeBoundary(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires Linux root to exercise a permanent UID/GID drop")
	}
	const uid, gid = uint32(65534), uint32(65534)
	root, err := os.MkdirTemp("/tmp", "minimalrouter-worker-boundary-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	childPath := filepath.Join(root, "worker-test")
	child, err := os.OpenFile(childPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(child, source); err != nil {
		t.Fatal(err)
	}
	child.Close()
	privateDir := filepath.Join(root, "private")
	if err := os.Mkdir(privateDir, 0700); err != nil {
		t.Fatal(err)
	}
	privateFile := filepath.Join(privateDir, "candidate.json")
	if err := os.WriteFile(privateFile, []byte("root-private-input-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	start := func(dir string, drop bool) (*migrationDBWorker, error) {
		cmd := exec.Command(childPath, "-test.run=^TestMigrationRestrictedWorkerChild$")
		cmd.Env = []string{"MINIMALROUTER_RESTRICTED_TEST_WORKER=1", "MINIMALROUTER_TEST_UID=65534", "MINIMALROUTER_TEST_GID=65534", "MINIMALROUTER_TEST_PRIVATE_FILE=" + privateFile}
		if drop {
			cmd.SysProcAttr = migrationWorkerCredentials(uid, gid)
		}
		return openWorkerProcess(cmd, dir)
	}
	data := filepath.Join(root, "data")
	s, err := config.NewStore(data)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := os.Chown(data, int(uid), int(gid)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(filepath.Join(data, "minimalrouter.db"), int(uid), int(gid)); err != nil {
		t.Fatal(err)
	}
	w, err := start(data, true)
	if err != nil {
		t.Fatalf("dropped worker failed credential/private-file checks: %v", err)
	}
	if _, err := w.ReadOfflineConfig(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if w, err := start(data, false); err == nil {
		w.Close()
		t.Fatal("root worker served SQLite")
	}
	// Model the exact checked-regular-file -> symlink substitution window.
	victim := filepath.Join(privateDir, "victim")
	s, err = config.NewStore(victim)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	victimDB := filepath.Join(victim, "minimalrouter.db")
	before, err := os.ReadFile(victimDB)
	if err != nil {
		t.Fatal(err)
	}
	redirect := filepath.Join(root, "redirect")
	os.Mkdir(redirect, 0700)
	os.Chown(redirect, int(uid), int(gid))
	link := filepath.Join(redirect, "minimalrouter.db")
	os.WriteFile(link, []byte("checked regular fixture"), 0600)
	if err := requireRegular(link); err != nil {
		t.Fatal(err)
	}
	os.Remove(link)
	if err := os.Symlink(victimDB, link); err != nil {
		t.Fatal(err)
	}
	if w, err := start(redirect, true); err == nil {
		w.Close()
		t.Fatal("worker followed redirect into root-only SQLite")
	}
	after, err := os.ReadFile(victimDB)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("root-only victim changed")
	}
}
