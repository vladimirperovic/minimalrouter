// Package kdf bounds how many memory-hard key derivations may run at once.
//
// Every Argon2id derivation in this project — login, password change, recovery
// credential reset and encrypted backups — reserves 64 MiB of live working
// memory, while routerd runs under a 128 MiB heap budget. Login rate limits
// bound attempts per minute, not attempts in flight, so several simultaneous
// requests from one allowed source could otherwise reserve hundreds of MiB at
// once and push the management plane into the kernel OOM killer.
// debug.SetMemoryLimit is a soft GC target and cannot reclaim live Argon2
// buffers, so admission control is the only bound that actually holds.
package kdf

import (
	"errors"
	"time"
)

const (
	// maxConcurrent is deliberately one: a single derivation already reserves
	// half the process heap budget on the smallest supported appliance.
	maxConcurrent = 1
	// queueWait bounds how long a caller queues before being told to retry,
	// so a burst degrades into rejections rather than unbounded goroutines.
	queueWait = 15 * time.Second
)

// ErrBusy reports that no derivation slot became available in time. Callers
// should surface it as a retryable service condition, never as a credential
// rejection.
var ErrBusy = errors.New("key derivation capacity is exhausted; retry shortly")

var slots = make(chan struct{}, maxConcurrent)

// Acquire reserves a derivation slot, waiting at most queueWait. Every
// successful Acquire must be paired with a Release.
func Acquire() error {
	select {
	case slots <- struct{}{}:
		return nil
	default:
	}
	timer := time.NewTimer(queueWait)
	defer timer.Stop()
	select {
	case slots <- struct{}{}:
		return nil
	case <-timer.C:
		return ErrBusy
	}
}

// Release returns a slot reserved by Acquire.
func Release() {
	select {
	case <-slots:
	default:
	}
}
