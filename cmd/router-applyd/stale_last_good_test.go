package main

import (
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// staleButVerifiedConfig is a configuration an older release wrote and applied
// successfully, carrying one value a later, stricter rule now rejects.
func staleButVerifiedConfig(t *testing.T) config.SystemConfig {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "isp-user"
	cfg.WAN.Password = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("fixture is supposed to be invalid under the current rules")
	}
	if err := cfg.ValidateScenarioSafety(); err != nil {
		t.Fatalf("fixture must still be scenario-safe: %v", err)
	}
	return cfg
}

// validatePrivilegedCandidate is the verdict applyAll reaches before it touches
// anything. It must match the management plane's verdict on the same pair, or an
// edit routerd accepted is refused here and the appliance cannot be saved.
func TestPrivilegedCandidateAcceptsAnEditBesideAnUntouchedStaleFault(t *testing.T) {
	stale := staleButVerifiedConfig(t)

	next := stale
	next.Accounting.Enabled = true

	if err := validatePrivilegedCandidate(next, &stale); err != nil {
		t.Fatalf("the privileged plane disagreed with the management plane: %v", err)
	}
}

func TestPrivilegedCandidateAcceptsRepairOfTheStaleField(t *testing.T) {
	stale := staleButVerifiedConfig(t)

	next := stale
	next.WAN.Password = "a-real-pppoe-secret"

	if err := validatePrivilegedCandidate(next, &stale); err != nil {
		t.Fatalf("repairing the stale field must be accepted: %v", err)
	}
}

func TestPrivilegedCandidateRejectsAFaultTheChangeIntroduces(t *testing.T) {
	stale := staleButVerifiedConfig(t)

	next := stale
	next.LAN.IPAddress = "not-an-address"

	if err := validatePrivilegedCandidate(next, &stale); err == nil {
		t.Fatal("a fault introduced by the change must still be rejected")
	}
}

// Without a last-good record there is nothing to excuse, so the helper must
// fall back to judging the whole candidate.
func TestPrivilegedCandidateIsStrictWithoutALastGoodRecord(t *testing.T) {
	stale := staleButVerifiedConfig(t)

	if err := validatePrivilegedCandidate(stale, nil); err == nil {
		t.Fatal("a first-run candidate must be judged in full")
	}
}

// Scenario safety carries the security invariants and is never excused, even
// when the same fault is already stored.
func TestPrivilegedCandidateNeverExcusesScenarioSafety(t *testing.T) {
	stale := staleButVerifiedConfig(t)
	stale.System.Domain = "a..b"

	next := stale
	next.Accounting.Enabled = true

	if err := validatePrivilegedCandidate(next, &stale); err == nil {
		t.Fatal("a scenario-unsafe candidate must be rejected regardless of what is stored")
	}
}
