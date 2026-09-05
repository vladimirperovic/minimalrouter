//go:build !unix

package firmware

import "os"

// Minimal Router runs only on Linux appliances. This stub exists so the
// package still builds — and therefore so its tests, and the tests of every
// package that imports it, can run on a developer machine. Cross-process
// serialization of slot mutations is not provided here, so this build must
// never be shipped or used to make a claim about locking behaviour.
func lockExclusive(*os.File) error { return nil }

func unlock(*os.File) {}
