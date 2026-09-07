package config

import "fmt"

// ValidateLiveCandidate admits only complete, valid configurations with a
// recoverable baseline. Legacy migration must happen before live activation:
// MigrateLegacyFields supplies only its explicitly defined deterministic
// defaults, and an operator must repair remaining faults locally while stopped.
// A valid candidate alone cannot make an invalid rollback target safe. A nil
// baseline is the first-run case, whose recovery target is the setup-only LAN.
func ValidateLiveCandidate(candidate SystemConfig, previous *SystemConfig) error {
	if err := candidate.Validate(); err != nil {
		return err
	}
	if err := candidate.ValidateScenarioSafety(); err != nil {
		return err
	}
	if previous != nil {
		if err := previous.Validate(); err != nil {
			return fmt.Errorf("rollback baseline requires local configuration migration before live changes: %w", err)
		}
		if err := previous.ValidateScenarioSafety(); err != nil {
			return fmt.Errorf("rollback baseline requires local configuration migration before live changes: %w", err)
		}
	}
	return nil
}
