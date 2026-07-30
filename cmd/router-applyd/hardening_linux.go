//go:build linux

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// hardenProcess narrows ambient process behavior before the privileged helper
// accepts any request. The helper remains root because it owns network and
// service transitions, but it cannot gain additional privilege through exec.
func hardenProcess() error {
	unix.Umask(0o077)
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("disable core dumps: %w", err)
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("enable no_new_privs: %w", err)
	}
	for resource, limit := range map[int]uint64{
		unix.RLIMIT_CORE:   0,
		unix.RLIMIT_NOFILE: 1024,
		unix.RLIMIT_NPROC:  128,
	} {
		if err := unix.Setrlimit(resource, &unix.Rlimit{Cur: limit, Max: limit}); err != nil {
			return fmt.Errorf("set resource limit %d: %w", resource, err)
		}
	}
	// Child processes use only project-selected absolute binaries. A fixed PATH
	// removes inherited user-controlled lookup locations from diagnostics.
	if err := os.Setenv("PATH", "/usr/sbin:/usr/bin:/sbin:/bin"); err != nil {
		return err
	}
	_ = os.Unsetenv("LD_PRELOAD")
	_ = os.Unsetenv("LD_LIBRARY_PATH")
	_ = os.Unsetenv("ENV")
	_ = os.Unsetenv("BASH_ENV")
	return nil
}
