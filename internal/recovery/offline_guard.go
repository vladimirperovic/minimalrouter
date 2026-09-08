package recovery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const offlineLockPath = "/run/minimalrouter-offline.lock"
const migrationDirectory = "/var/lib/minimalrouter-migration"

// AcquireDaemonGuard holds a shared lock for the daemon lifetime. The caller
// must retain the returned file until exit and call this before ANY runtime or
// store initialization. Non-Linux developer/preview builds need no host guard.
func AcquireDaemonGuard() (*os.File, error) {
	if runtime.GOOS != "linux" {
		return nil, nil
	}
	f, err := acquireOfflineLock(offlineLockPath, false, 0)
	if err != nil {
		return nil, err
	}
	if err := checkMigrationFence(migrationDirectory); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func checkMigrationFence(dir string) error {
	return inspectMigrationFence(dir, 0)
}

func inspectMigrationFence(dir string, uid uint32) error {
	if err := secureDirectory(dir, uid, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("unsafe offline migration fence directory: %w", err)
	}
	_, err := os.Lstat(filepath.Join(dir, "pending.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot inspect offline migration fence: %w", err)
	}
	return errors.New("offline configuration migration is incomplete; resume router-recovery migrate-config before starting services")
}

// Check the parent and the opened inode, never following a lock-file symlink.
// uid is parameterized only for unprivileged unit-test fixtures.
func acquireOfflineLock(path string, exclusive bool, uid uint32) (*os.File, error) {
	if err := secureDirectory(filepath.Dir(path), uid, false); err != nil {
		return nil, err
	}
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW
	if uint32(os.Geteuid()) == uid {
		flags |= unix.O_CREAT
	}
	fd, err := unix.Open(path, flags, 0644)
	if err != nil {
		return nil, fmt.Errorf("open offline guard: %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Uid != uid || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0022 != 0 || stat.Nlink != 1 {
		f.Close()
		return nil, errors.New("offline guard must be an exclusively owned, non-writable regular file")
	}
	if uint32(os.Geteuid()) == uid {
		if err := f.Chmod(0644); err != nil {
			f.Close()
			return nil, err
		}
	}
	op := unix.LOCK_SH
	if exclusive {
		op = unix.LOCK_EX
	}
	if err := unix.Flock(fd, op|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("offline guard busy; stop both routerd and router-applyd before migration: %w", err)
	}
	return f, nil
}

func secureDirectory(path string, uid uint32, private bool) error {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return err
	}
	mask := uint32(0022)
	if private {
		mask = 0077
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Uid != uid || uint32(stat.Mode)&mask != 0 {
		return fmt.Errorf("unsafe ownership or permissions on directory %s", path)
	}
	return nil
}

// Old releases did not take the guard. Reject their processes too, including
// direct launches; unreadable /proc entries fail closed unless already exited.
func requireDaemonsStopped(proc string) error {
	entries, err := os.ReadDir(proc)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(proc, entry.Name(), "comm"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect daemon processes: %w", err)
		}
		name := strings.TrimSpace(string(data))
		if name == "routerd" || name == "router-applyd" {
			return fmt.Errorf("%s is still running; stop both daemons", name)
		}
	}
	return nil
}
