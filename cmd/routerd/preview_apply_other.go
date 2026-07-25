//go:build !darwin

package main

import "github.com/vladimirperovic/minimalrouter/internal/apply"

// The runtime guard in main refuses preview mode outside macOS. This stub
// keeps production cross-builds explicit and compile-time complete.
func newPreviewApplyClient() apply.Client {
	return nil
}
