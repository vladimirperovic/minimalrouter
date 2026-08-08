package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestRollbackWireGuardCleanupInterfacesRemovesCandidateOnlyNames(t *testing.T) {
	previous := config.DefaultConfig()
	previous.WireGuard.Enabled = true
	previous.WireGuard.Interface = "wg0"
	previous.WGClient.Enabled = true
	previous.WGClient.Interface = "wg1"

	candidate := previous.DeepCopy()
	candidate.WireGuard.Interface = "wg9"
	candidate.WGClient.Interface = "wg8"

	got := rollbackWireGuardCleanupInterfaces(&previous, &candidate)
	want := []string{"wg9", "wg8"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanup interfaces = %v, want %v", got, want)
	}
}

func TestRollbackWireGuardCleanupInterfacesCoversFirstRun(t *testing.T) {
	candidate := config.DefaultConfig()
	candidate.WireGuard.Enabled = true
	candidate.WireGuard.Interface = "wg7"
	candidate.WGClient.Enabled = true
	candidate.WGClient.Interface = "wg6"

	got := rollbackWireGuardCleanupInterfaces(nil, &candidate)
	want := []string{"wg7", "wg6"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("first-run cleanup interfaces = %v, want %v", got, want)
	}
}

func TestRollbackWireGuardCleanupInterfacesPreservesSurvivingNames(t *testing.T) {
	previous := config.DefaultConfig()
	previous.WireGuard.Enabled = true
	previous.WireGuard.Interface = "wg0"
	previous.WGClient.Enabled = true
	previous.WGClient.Interface = "wg1"
	candidate := previous.DeepCopy()

	if got := rollbackWireGuardCleanupInterfaces(&previous, &candidate); len(got) != 0 {
		t.Fatalf("surviving interfaces were marked stale: %v", got)
	}
}

func TestVerificationPlanTracksWireGuardClientChanges(t *testing.T) {
	previous := config.DefaultConfig()
	previous.WGClient.Enabled = true
	previous.WGClient.Address = "10.20.30.2/32"
	candidate := previous.DeepCopy()

	if verificationPlan(apply.OpApplyAll, &previous, candidate).WGClient {
		t.Fatal("unchanged WireGuard client unexpectedly requires verification")
	}
	candidate.WGClient.Address = "10.20.30.3/32"
	if !verificationPlan(apply.OpApplyAll, &previous, candidate).WGClient {
		t.Fatal("WireGuard client address change must require verification")
	}
}

func wgDumpForHandshake(timestamp int64) string {
	return fmt.Sprintf("private public 51820 off\npeer psk 198.51.100.1:51820 10.0.0.0/24 %d 10 20 25\n", timestamp)
}

func TestWGHandshakeFreshRejectsFutureTimestamp(t *testing.T) {
	err := wgHandshakeFresh(wgDumpForHandshake(time.Now().Add(10*time.Minute).Unix()), 25)
	if err == nil || !strings.Contains(err.Error(), "future") {
		t.Fatalf("future handshake timestamp was accepted: %v", err)
	}
}

func TestWGHandshakeFreshAcceptsRecentTimestamp(t *testing.T) {
	if err := wgHandshakeFresh(wgDumpForHandshake(time.Now().Add(-10*time.Second).Unix()), 25); err != nil {
		t.Fatalf("recent handshake timestamp was rejected: %v", err)
	}
}
