//go:build windows

package firmware

import (
	"os"
)

func lockFile(f *os.File) error {
	// Dummy implementation for Windows testing.
	return nil
}

func unlockFile(f *os.File) error {
	return nil
}
