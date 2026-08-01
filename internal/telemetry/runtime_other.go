//go:build !linux

package telemetry

import (
	"runtime"

	"github.com/vladimirperovic/minimalrouter/internal/storage"
)

func RuntimeSnapshot(_, _ string) RuntimeStatus {
	return RuntimeStatus{
		Available:    false,
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		CPUCount:     runtime.NumCPU(),
		Storage:      storage.Inspect(""),
		DHCPLeases:   []DHCPLease{},
	}
}
