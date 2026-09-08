package main

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// confirmationModeAllowed limits provisional configuration to changes that can
// affect the administrator's path back to the appliance. The previous LAN
// interface must remain present so rollback always has a known local path.
// The outbound tunnel (wg1) is confirmation-required but does not affect the
// management path, so its changes remain confirmable.
func confirmationModeAllowed(previous *config.SystemConfig, candidate config.SystemConfig) bool {
	if previous == nil || previous.LAN.Interface != candidate.LAN.Interface {
		return false
	}
	return config.RequiresConfirmation(*previous, candidate)
}

// Validate the requested mode independently of routerd. Recovery and initial
// provisioning have no provisional mode; an ordinary apply cannot suppress a
// required confirmation merely by omitting the flag.
func validateConfirmationRequest(req apply.ApplyRequest, previous *config.SystemConfig) error {
	if req.RequireConfirmation {
		if req.Op != apply.OpApplyAll || req.DeferLastGood || !confirmationModeAllowed(previous, req.Config) {
			return errors.New("confirmation mode is invalid for this change")
		}
	} else if req.Op == apply.OpApplyAll && previous != nil && config.RequiresConfirmation(*previous, req.Config) {
		return errors.New("this change requires confirmation")
	}
	return nil
}

// Clearing the journal is an acknowledgement, so persist its removal before
// reporting success. Also sync on retry after an ambiguous earlier unlink.
func clearPendingFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
