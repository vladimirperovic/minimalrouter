//go:build !unix

package faultinject

import (
	"fmt"
	"os"
)

// Lab hooks exist only for the Linux appliance; elsewhere ownership cannot be
// proven, so a hook is never run.
func ownedByRootOrSelf(path string, _ os.FileInfo) error {
	return fmt.Errorf("%s ownership cannot be verified on this platform", path)
}
