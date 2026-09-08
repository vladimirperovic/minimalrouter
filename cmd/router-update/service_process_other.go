//go:build !unix

package main

import (
	"os/exec"
	"time"
)

// Non-appliance build support; OpenRC execution requires Unix.
func configureServiceProcess(cmd *exec.Cmd) { cmd.WaitDelay = 2 * time.Second }
