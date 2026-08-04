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

func TestWGClientHealthFollowsHandshakeNotInterface(t *testing.T) {
	base := healthyInput()
	base.Config.WGClient.Enabled = true
	base.Config.WGClient.Interface = "wg1"
	now := base.Now

	// Interface down: degraded.
	down := base
	down.Facts.WireGuardClientInterfaceUp = false
	down.Facts.WireGuardClientLastHandshake = now.Add(-1 * time.Minute).Unix()
	if got := Build(down).State; got != StateDegraded {
		t.Fatalf("interface down state = %q, want degraded", got)
	}

	// Interface up but no handshake ever: degraded, not healthy.
	noHandshake := base
	noHandshake.Facts.WireGuardClientInterfaceUp = true
	noHandshake.Facts.WireGuardClientLastHandshake = 0
	if got := Build(noHandshake).State; got != StateDegraded {
		t.Fatalf("no-handshake state = %q, want degraded", got)
	}

	// Stale handshake: degraded.
	stale := base
	stale.Facts.WireGuardClientInterfaceUp = true
	stale.Facts.WireGuardClientLastHandshake = now.Add(-10 * time.Minute).Unix()
	if got := Build(stale).State; got != StateDegraded {
		t.Fatalf("stale handshake state = %q, want degraded", got)
	}

	// Recent handshake: healthy.
	connected := base
	connected.Facts.WireGuardClientInterfaceUp = true
	connected.Facts.WireGuardClientLastHandshake = now.Add(-30 * time.Second).Unix()
	snapshot := Build(connected)
	if snapshot.State != StateHealthy {
		t.Fatalf("connected state = %q, want healthy; checks=%+v", snapshot.State, snapshot.Checks)
	}
	found := false
	for _, check := range snapshot.Checks {
		if check.ID == "wireguard-client" {
			found = true
			if check.State != StateHealthy {
				t.Fatalf("wireguard-client check = %q, want healthy", check.State)
			}
		}
	}
	if !found {
		t.Fatal("wireguard-client health check is missing")
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

func TestDNSServerRunningButNotResolvingDegradesHealth(t *testing.T) {
	input := healthyInput()
	falseValue := false
	input.DNSResolves = &falseValue
	input.DNSError = "lookup example.com on 127.0.0.1:53: i/o timeout"
	snapshot := Build(input)
	state := ""
	for _, check := range snapshot.Checks {
		if check.ID == "dns_dhcp" {
			state = string(check.State)
		}
	}
	if state != string(StateDegraded) {
		t.Fatalf("dns_dhcp state = %q, want degraded; checks=%+v", state, snapshot.Checks)
	}
	if snapshot.State != StateDegraded {
		t.Fatalf("overall state = %q, want degraded", snapshot.State)
	}
}
