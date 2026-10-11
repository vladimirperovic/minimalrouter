package apply

import (
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/telemetry"
)

func TestLANChangeRejectsObservedAddressCollision(t *testing.T) {
	current := config.DefaultConfig()
	candidate := current.DeepCopy()
	snapshot := telemetry.DHCPLeaseSnapshot{DHCPLeaseStatus: telemetry.DHCPLeaseStatus{Available: true}, Leases: []telemetry.DHCPLease{{MAC: "02:00:00:00:00:20", IPAddress: "192.168.1.20"}}}
	candidate.LAN.IPAddress = "192.168.1.20"
	if _, err := inspectLANObservation(current, candidate, snapshot); err == nil {
		t.Fatal("gateway stole an observed lease")
	}
	candidate = current.DeepCopy()
	candidate.DHCP.StaticLeases = []config.StaticLease{{ID: "test", MAC: "02:00:00:00:00:21", IPAddress: "192.168.1.20"}}
	if _, err := inspectLANObservation(current, candidate, snapshot); err == nil {
		t.Fatal("reservation stole an observed lease")
	}
	candidate.DHCP.StaticLeases[0].MAC = "02:00:00:00:00:20"
	if _, err := inspectLANObservation(current, candidate, snapshot); err != nil {
		t.Fatal(err)
	}
	snapshot.Available = false
	snapshot.Leases = nil
	if warnings, err := inspectLANObservation(current, candidate, snapshot); err != nil || len(warnings) == 0 {
		t.Fatal("unknown observation was hidden", warnings, err)
	}
}
