//go:build !linux

package telemetry

import "runtime"

func InterfaceInventory() []NetworkInterfaceStatus {
	return nil
}

func RuntimeSnapshot(_, _ string) RuntimeStatus {
	return RuntimeStatus{
		Available:    false,
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		CPUCount:     runtime.NumCPU(),
		DHCPLeases:   []DHCPLease{},
	}
}
