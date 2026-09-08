//go:build unix

package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// OpenRC uses shell children. Killing only rc-service can leave start_post
// changing service state during rollback, or keep CombinedOutput waiting on a
// pipe inherited by a descendant. Bound both cancellation and pipe draining.
func configureServiceProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := unix.Kill(-cmd.Process.Pid, unix.SIGKILL)
		if errors.Is(err, unix.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
}
