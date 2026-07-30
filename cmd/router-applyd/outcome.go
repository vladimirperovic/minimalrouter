package main

import "github.com/vladimirperovic/minimalrouter/internal/apply"

func recoveryFailure(id, message string) apply.ApplyResponse {
	return apply.ApplyResponse{
		ID:               id,
		Success:          false,
		Verified:         false,
		RolledBack:       false,
		RecoveryRequired: true,
		Error:            message,
	}
}

// journalPersistenceFailure preserves a verified rollback, but otherwise treats
// the runtime outcome as unknown. Without the idempotency record, routerd cannot
// safely prove that a lost response would not replay a completed mutation.
func journalPersistenceFailure(id string, previous apply.ApplyResponse) apply.ApplyResponse {
	if previous.RolledBack && !previous.RecoveryRequired {
		return failure(id, "transaction result could not be persisted; previous configuration was verified restored", true)
	}
	return recoveryFailure(id, "transaction result could not be persisted; canonical reconciliation is required")
}
