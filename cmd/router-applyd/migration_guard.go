package main

import (
	"os"
	"sync"

	"github.com/vladimirperovic/minimalrouter/internal/recovery"
)

var (
	guardOnce sync.Once
	// daemonGuard is never read, but it must stay referenced: dropping the
	// *os.File would let its finalizer close the descriptor and release the
	// offline-migration lock while the helper is still running.
	//lint:ignore U1000 the reference itself holds the lock for the process lifetime
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
