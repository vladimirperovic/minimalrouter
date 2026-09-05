//go:build !unix

package api

import "errors"

// The appliance is Linux. On other platforms the free-space precondition
// cannot be evaluated, and the caller treats an error as "unknown" rather than
// as "enough space": this build exists only so the package's tests can run on
// a developer machine.
func availableBytes(string) (uint64, error) {
	return 0, errors.New("free space is not measurable on this platform")
}
