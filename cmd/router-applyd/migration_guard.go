package main

import (
	"os"
	"sync"

	"github.com/vladimirperovic/minimalrouter/internal/recovery"
)

var (
	guardOnce             sync.Once
	daemonGuard           *os.File
	daemonGuardError      error
	runtimeAdmissionReady = make(chan struct{})
)

// The file remains open for the process lifetime, excluding offline migration
// before startup reconciliation or either command listener can mutate runtime.
func acquireDaemonGuard() error {
	guardOnce.Do(func() {
		daemonGuard, daemonGuardError = recovery.AcquireDaemonGuard()
	})
	return daemonGuardError
}
