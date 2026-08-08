package main

import (
	"fmt"
	"os"
)

const routerdReadyFileEnv = "MINIMALROUTER_READY_FILE"

// signalRouterdReady is intentionally called only after canonical runtime
// reconciliation, TLS preparation, and TCP listener binding succeed. OpenRC
// waits for this marker before reporting routerd started, so A/B activation
// cannot mistake a live supervise-daemon parent for a healthy management
// process.
func signalRouterdReady(revision any) error {
	path := os.Getenv(routerdReadyFileEnv)
	if path == "" {
		return nil
	}
	payload := []byte(fmt.Sprintf("%v\n", revision))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return fmt.Errorf("open readiness marker: %w", err)
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return fmt.Errorf("write readiness marker: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync readiness marker: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close readiness marker: %w", err)
	}
	return nil
}
