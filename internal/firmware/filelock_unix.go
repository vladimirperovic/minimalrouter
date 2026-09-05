//go:build unix

package firmware

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockExclusive takes the advisory lock that serializes slot mutations across
// routerd, router-update and router-recovery. It is the real guarantee on the
// appliance; see filelock_other.go for why other platforms only build.
func lockExclusive(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX)
}

func unlock(file *os.File) {
	_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
