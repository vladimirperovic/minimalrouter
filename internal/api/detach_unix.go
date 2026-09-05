//go:build unix

package api

import (
	"os/exec"
	"syscall"
)

// detachFromSession puts the privileged activation in its own session. The
// activation restarts routerd, so the command must not die with the process
// that started it, and must not be reachable through routerd's terminal.
func detachFromSession(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
