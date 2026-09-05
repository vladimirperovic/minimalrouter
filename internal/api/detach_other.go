//go:build !unix

package api

import "os/exec"

// Minimal Router runs only on Linux appliances. This stub exists so the
// package builds on a developer machine and its tests can run there; a build
// without session detachment must never be shipped.
func detachFromSession(*exec.Cmd) {}
