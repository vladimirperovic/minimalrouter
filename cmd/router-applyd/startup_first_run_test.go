package main

import (
	"errors"
	"os"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestReconcileStartupRestoresFirstRunRuntimeWhenLastGoodMissing(t *testing.T) {
	restored := 0
	cleared := 0
	err := reconcileStartup(startupReconcileHooks{
		loadLastGood:  func() (*config.SystemConfig, error) { return nil, os.ErrNotExist },
		pendingExists: func() (bool, error) { return false, nil },
		restoreRuntime: func(config.SystemConfig) error {
			t.Fatal("confirmed runtime must not be restored without last-good")
			return nil
		},
		restoreFirstRun: func() error {
			restored++
			return nil
		},
		clearPending: func() error {
			cleared++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("reconcileStartup: %v", err)
	}
	if restored != 1 || cleared != 0 {
		t.Fatalf("restored=%d cleared=%d, want 1/0", restored, cleared)
	}
}

func TestReconcileStartupOrphanedPendingReturnsToFirstRun(t *testing.T) {
	order := make([]string, 0, 2)
	err := reconcileStartup(startupReconcileHooks{
		loadLastGood:  func() (*config.SystemConfig, error) { return nil, os.ErrNotExist },
		pendingExists: func() (bool, error) { return true, nil },
		restoreRuntime: func(config.SystemConfig) error {
			t.Fatal("confirmed runtime must not be restored without last-good")
			return nil
		},
		restoreFirstRun: func() error {
			order = append(order, "restore-first-run")
			return nil
		},
		clearPending: func() error {
			order = append(order, "clear-pending")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("reconcileStartup: %v", err)
	}
	if len(order) != 2 || order[0] != "restore-first-run" || order[1] != "clear-pending" {
		t.Fatalf("unexpected recovery order: %v", order)
	}
}

func TestReconcileStartupKeepsPendingWhenFirstRunRestoreFails(t *testing.T) {
	restoreErr := errors.New("simulated first-run firewall failure")
	cleared := false
	err := reconcileStartup(startupReconcileHooks{
		loadLastGood:  func() (*config.SystemConfig, error) { return nil, os.ErrNotExist },
		pendingExists: func() (bool, error) { return true, nil },
		restoreRuntime: func(config.SystemConfig) error {
			return nil
		},
		restoreFirstRun: func() error { return restoreErr },
		clearPending: func() error {
			cleared = true
			return nil
		},
	})
	if !errors.Is(err, restoreErr) {
		t.Fatalf("error=%v, want wrapped restore error", err)
	}
	if cleared {
		t.Fatal("pending marker cleared before first-run runtime was safely restored")
	}
}
