package main

import (
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestLegacyCandidateAdmissionPersistenceStartupContract(t *testing.T) {
	previous := staleButVerifiedConfig(t)
	for _, state := range []struct {
		name             string
		repair, migrated bool
	}{
		{"unrepaired", false, false},
		{"candidate-only repair", true, false},
		{"explicit local migration completed", true, true},
	} {
		t.Run(state.name, func(t *testing.T) {
			repair := state.repair
			baseline := previous.DeepCopy()
			if state.migrated {
				baseline.WAN.Password = "synthetic-repaired-password"
			}
			candidate := baseline.DeepCopy()
			candidate.Accounting.Enabled = !candidate.Accounting.Enabled
			if repair {
				candidate.WAN.Password = "synthetic-repaired-password"
			}
			_, previewErr := apply.PreviewTransition(baseline, candidate)
			helperErr := validatePrivilegedCandidate(candidate, &baseline)
			hash, err := hashConfig(candidate)
			if err != nil {
				t.Fatal(err)
			}
			pendingErr := validatePendingConfirmation(pendingConfirmation{Config: candidate, ConfigHash: hash})
			store, err := config.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			candidate.Revision++
			persistErr := store.SaveConfig(candidate)
			restored := false
			startupErr := reconcileStartup(startupReconcileHooks{
				loadLastGood:   func() (*config.SystemConfig, error) { return &candidate, nil },
				pendingExists:  func() (bool, error) { return false, nil },
				restoreRuntime: func(config.SystemConfig) error { restored = true; return nil },
				clearPending:   func() error { return nil },
			})
			for stage, err := range map[string]error{"pending": pendingErr, "canonical persistence": persistErr, "startup": startupErr} {
				if (err == nil) != repair {
					t.Errorf("%s admission mismatch: repaired=%v err=%v", stage, repair, err)
				}
			}
			for stage, err := range map[string]error{"preview": previewErr, "helper": helperErr} {
				if (err == nil) != state.migrated {
					t.Errorf("%s must require a valid rollback baseline: migrated=%v err=%v", stage, state.migrated, err)
				}
			}
			if restored != repair {
				t.Fatal("startup activated invalid configuration")
			}
			if repair {
				loaded, err := store.GetLatestConfig()
				if err != nil || loaded.WAN.Password != candidate.WAN.Password {
					t.Fatalf("accepted configuration cannot reload: %v", err)
				}
			}
		})
	}
}
