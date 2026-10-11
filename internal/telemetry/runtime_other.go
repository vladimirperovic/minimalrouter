//go:build !linux

package telemetry

import (
	"runtime"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/storage"
)

func RuntimeSnapshot(_, _, _ string) RuntimeStatus {
	return RuntimeStatus{
		Available:       false,
		OS:              runtime.GOOS,
		Architecture:    runtime.GOARCH,
		CPUCount:        runtime.NumCPU(),
		Storage:         storage.Inspect(""),
		DHCPLeases:      []DHCPLease{},
		DHCPLeaseStatus: CurrentDHCPLeaseSnapshot().DHCPLeaseStatus,
	}
}

// CurrentDHCPLeases is unavailable on non-appliance platforms.
func CurrentDHCPLeases() []DHCPLease { return []DHCPLease{} }

func CurrentDHCPLeaseSnapshot() DHCPLeaseSnapshot {
	return DHCPLeaseSnapshot{DHCPLeaseStatus: DHCPLeaseStatus{CollectedAt: time.Now().UTC(), Error: "DHCP lease observation requires Linux"}, Leases: []DHCPLease{}}
}
