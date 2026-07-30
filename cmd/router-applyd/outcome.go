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
