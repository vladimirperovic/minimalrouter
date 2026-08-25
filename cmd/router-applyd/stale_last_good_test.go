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

// A last-good file is only written after a candidate was applied and verified.
// When a newer release tightens a rule, that file stops satisfying Validate
// without the stored runtime ever having been unproven. Failing closed there
// stopped routerd and tore down LAN, DNS, WAN and both WireGuard interfaces,
// leaving the operator no way in to repair the field.
func TestReconcileStartupRestoresLastGoodThatPredatesAStricterRule(t *testing.T) {
	stale := staleButVerifiedConfig(t)
	restored := false

	err := reconcileStartup(startupReconcileHooks{
		loadLastGood:  func() (*config.SystemConfig, error) { return &stale, nil },
		pendingExists: func() (bool, error) { return false, nil },
		restoreRuntime: func(cfg config.SystemConfig) error {
			restored = true
			if cfg.WAN.Username != "isp-user" {
				t.Errorf("restored a different configuration than last-good: %+v", cfg.WAN)
			}
			return nil
		},
		clearPending: func() error { return nil },
	})
	if err != nil {
		t.Fatalf("a stale but verified last-good must still be restored: %v", err)
	}
	if !restored {
		t.Fatal("the previously running runtime was not restored")
	}
}

// Downgrading the Validate gate must not reach scenario safety, which is the
// gate that encodes the actual security invariants rather than field syntax.
func TestReconcileStartupStillFailsClosedOnScenarioUnsafeLastGood(t *testing.T) {
	unsafe := config.DefaultConfig()
	unsafe.System.Domain = "a..b"
	if err := unsafe.ValidateScenarioSafety(); err == nil {
		t.Fatal("fixture must be scenario-unsafe")
	}

	restored := false
	err := reconcileStartup(startupReconcileHooks{
		loadLastGood:   func() (*config.SystemConfig, error) { return &unsafe, nil },
		pendingExists:  func() (bool, error) { return false, nil },
		restoreRuntime: func(config.SystemConfig) error { restored = true; return nil },
		clearPending:   func() error { return nil },
	})
	if err == nil {
		t.Fatal("scenario-unsafe last-good must still fail closed")
	}
	if restored {
		t.Fatal("scenario-unsafe last-good must never be activated")
	}
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
