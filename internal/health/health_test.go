package health

import (
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/storage"
	"github.com/vladimirperovic/minimalrouter/internal/telemetry"
)

func healthyInput() Input {
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = false
	cfg.WireGuard.Enabled = false
	now := time.Date(2026, 8, 1, 7, 0, 0, 0, time.UTC)
	backup := now.Add(-24 * time.Hour)
	return Input{
		Config: cfg,
		Runtime: telemetry.RuntimeStatus{
			Available:             true,
			MemoryUsedBytes:       256,
			MemoryTotalBytes:      1024,
			TimeSynchronized:      true,
			ConntrackCount:        100,
			ConntrackMax:          1000,
			ConntrackUsagePercent: 10,
			Storage:               storage.Evaluate(1000, 700),
		},
		Engine:                apply.EngineStatus{},
		UpdateTrustConfigured: true,
		Facts: RuntimeFacts{
			Available:            true,
			RouterdHealthy:       true,
			ApplydHealthy:        true,
			ApplySocketAvailable: true,
			DnsmasqStarted:       true,
			UpdateStateAvailable: true,
		},
		LastBackupAt: &backup,
		Now:          now,
	}
}

func TestBuildHealthyAppliance(t *testing.T) {
	snapshot := Build(healthyInput())
	if snapshot.State != StateHealthy {
		t.Fatalf("state = %q, want healthy; checks=%+v", snapshot.State, snapshot.Checks)
	}
}

func TestRecoveryRequiredOverridesOtherSignals(t *testing.T) {
	input := healthyInput()
	input.Engine.RecoveryRequired = true
	input.Engine.RecoveryReason = "canonical state cannot be verified"
	input.Runtime.Storage = storage.Evaluate(100, 5)
	snapshot := Build(input)
	if snapshot.State != StateRecoveryRequired {
		t.Fatalf("state = %q, want recovery_required", snapshot.State)
	}
}

func TestCriticalStorageIsDegraded(t *testing.T) {
	input := healthyInput()
	input.Runtime.Storage = storage.Evaluate(100, 10)
	snapshot := Build(input)
	if snapshot.State != StateDegraded {
		t.Fatalf("state = %q, want degraded", snapshot.State)
	}
}

func TestWarningSignalsAggregate(t *testing.T) {
	input := healthyInput()
	input.Runtime.TimeSynchronized = false
	snapshot := Build(input)
	if snapshot.State != StateWarning {
		t.Fatalf("state = %q, want warning", snapshot.State)
	}
}

func TestStaleBackupDegradesHealth(t *testing.T) {
	input := healthyInput()
	old := input.Now.Add(-31 * 24 * time.Hour)
	input.LastBackupAt = &old
	snapshot := Build(input)
	if snapshot.State != StateDegraded {
		t.Fatalf("state = %q, want degraded", snapshot.State)
	}
}
