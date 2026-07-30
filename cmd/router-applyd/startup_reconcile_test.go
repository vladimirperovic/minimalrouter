package main

import (
	"errors"
	"os"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func validStartupConfig() config.SystemConfig {
	return config.DefaultConfig()
}

func TestReconcileStartupFreshInstallIsNoOp(t *testing.T) {
	restored := false
	cleared := false
	err := reconcileStartup(startupReconcileHooks{
		loadLastGood:  func() (*config.SystemConfig, error) { return nil, os.ErrNotExist },
		pendingExists: func() (bool, error) { return false, nil },
		restoreRuntime: func(config.SystemConfig) error {
			restored = true
			return nil
		},
		clearPending: func() error {
			cleared = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("fresh install reconciliation failed: %v", err)
	}
	if restored || cleared {
		t.Fatalf("fresh install mutated runtime: restored=%v cleared=%v", restored, cleared)
	}
}

func TestReconcileStartupRejectsPendingStateWithoutLastGood(t *testing.T) {
	err := reconcileStartup(startupReconcileHooks{
		loadLastGood:   func() (*config.SystemConfig, error) { return nil, os.ErrNotExist },
		pendingExists:  func() (bool, error) { return true, nil },
		restoreRuntime: func(config.SystemConfig) error { return nil },
		clearPending:   func() error { return nil },
	})
	if err == nil {
		t.Fatal("expected fail-closed error for orphaned pending state")
	}
}

func TestReconcileStartupPropagatesPendingInspectionFailure(t *testing.T) {
	inspectErr := errors.New("simulated metadata I/O failure")
	err := reconcileStartup(startupReconcileHooks{
		loadLastGood:  func() (*config.SystemConfig, error) { return nil, os.ErrNotExist },
		pendingExists: func() (bool, error) { return false, inspectErr },
		restoreRuntime: func(config.SystemConfig) error {
			t.Fatal("runtime restore must not run")
			return nil
		},
		clearPending: func() error {
			t.Fatal("pending clear must not run")
			return nil
		},
	})
	if !errors.Is(err, inspectErr) {
		t.Fatalf("error = %v, want wrapped inspection error", err)
	}
}

func TestReconcileStartupAlwaysRestoresVolatileRuntime(t *testing.T) {
	cfg := validStartupConfig()
	restores := 0
	clears := 0
	err := reconcileStartup(startupReconcileHooks{
		loadLastGood:  func() (*config.SystemConfig, error) { return &cfg, nil },
		pendingExists: func() (bool, error) { return false, nil },
		restoreRuntime: func(config.SystemConfig) error {
			restores++
			return nil
		},
		clearPending: func() error {
			clears++
			return os.ErrNotExist
		},
	})
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}
	if restores != 1 {
		t.Fatalf("runtime restore count = %d, want 1", restores)
	}
	if clears != 1 {
		t.Fatalf("pending clear count = %d, want 1", clears)
	}
}

func TestReconcileStartupKeepsPendingMarkerWhenRestoreFails(t *testing.T) {
	cfg := validStartupConfig()
	cleared := false
	restoreErr := errors.New("simulated WireGuard restore failure")
	err := reconcileStartup(startupReconcileHooks{
		loadLastGood:   func() (*config.SystemConfig, error) { return &cfg, nil },
		pendingExists:  func() (bool, error) { return true, nil },
		restoreRuntime: func(config.SystemConfig) error { return restoreErr },
		clearPending: func() error {
			cleared = true
			return nil
		},
	})
	if !errors.Is(err, restoreErr) {
		t.Fatalf("error = %v, want wrapped restore error", err)
	}
	if cleared {
		t.Fatal("pending marker was cleared before confirmed runtime was restored")
	}
}

func TestReconcileStartupClearsUnconfirmedChangeOnlyAfterRestore(t *testing.T) {
	cfg := validStartupConfig()
	order := make([]string, 0, 2)
	err := reconcileStartup(startupReconcileHooks{
		loadLastGood:  func() (*config.SystemConfig, error) { return &cfg, nil },
		pendingExists: func() (bool, error) { return true, nil },
		restoreRuntime: func(config.SystemConfig) error {
			order = append(order, "restore")
			return nil
		},
		clearPending: func() error {
			order = append(order, "clear")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}
	if len(order) != 2 || order[0] != "restore" || order[1] != "clear" {
		t.Fatalf("unexpected operation order: %v", order)
	}
}

func TestReconcileStartupRejectsPendingClearFailure(t *testing.T) {
	cfg := validStartupConfig()
	clearErr := errors.New("simulated read-only state directory")
	restored := false
	err := reconcileStartup(startupReconcileHooks{
		loadLastGood:  func() (*config.SystemConfig, error) { return &cfg, nil },
		pendingExists: func() (bool, error) { return true, nil },
		restoreRuntime: func(config.SystemConfig) error {
			restored = true
			return nil
		},
		clearPending: func() error { return clearErr },
	})
	if !errors.Is(err, clearErr) {
		t.Fatalf("error = %v, want wrapped clear error", err)
	}
	if !restored {
		t.Fatal("confirmed runtime was not restored before pending cleanup")
	}
}

func TestReconcileStartupRejectsInvalidLastGood(t *testing.T) {
	cfg := validStartupConfig()
	cfg.LAN.CIDR = "invalid"
	restored := false
	err := reconcileStartup(startupReconcileHooks{
		loadLastGood:  func() (*config.SystemConfig, error) { return &cfg, nil },
		pendingExists: func() (bool, error) { return false, nil },
		restoreRuntime: func(config.SystemConfig) error {
			restored = true
			return nil
		},
		clearPending: func() error { return nil },
	})
	if err == nil {
		t.Fatal("expected invalid last-good configuration to fail closed")
	}
	if restored {
		t.Fatal("invalid last-good configuration reached runtime activation")
	}
}

func TestReconcileStartupRejectsNilLastGood(t *testing.T) {
	err := reconcileStartup(startupReconcileHooks{
		loadLastGood:   func() (*config.SystemConfig, error) { return nil, nil },
		pendingExists:  func() (bool, error) { return false, nil },
		restoreRuntime: func(config.SystemConfig) error { return nil },
		clearPending:   func() error { return nil },
	})
	if err == nil {
		t.Fatal("expected nil last-good configuration to fail closed")
	}
}

func TestReconcileStartupRejectsIncompleteHooks(t *testing.T) {
	if err := reconcileStartup(startupReconcileHooks{}); err == nil {
		t.Fatal("expected incomplete hooks to be rejected")
	}
}

func TestWireGuardMTUBoundsSurviveStartupReconstruction(t *testing.T) {
	cfg := validStartupConfig()
	cfg.WAN.MTU = 1492
	if got := wireGuardMTU(cfg); got != 1412 {
		t.Fatalf("PPPoE WireGuard MTU = %d, want 1412", got)
	}
	cfg.WAN.MTU = 9000
	if got := wireGuardMTU(cfg); got != 1420 {
		t.Fatalf("jumbo WireGuard MTU = %d, want capped 1420", got)
	}
	cfg.WAN.MTU = 500
	if got := wireGuardMTU(cfg); got != 576 {
		t.Fatalf("small WAN WireGuard MTU = %d, want floor 576", got)
	}
}
