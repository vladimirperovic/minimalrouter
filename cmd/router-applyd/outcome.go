package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

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

func replayTransactionResponse(id, configHash string, previous *transactionRecord, loadErr error) (*apply.ApplyResponse, bool) {
	if loadErr != nil {
		if errors.Is(loadErr, os.ErrNotExist) {
			return nil, false
		}
		response := recoveryFailure(id, "transaction journal could not be read; canonical reconciliation is required")
		return &response, true
	}
	if previous == nil {
		response := recoveryFailure(id, "transaction journal returned no record; canonical reconciliation is required")
		return &response, true
	}
	if previous.ID != id {
		return nil, false
	}
	if previous.ConfigHash != configHash {
		response := failure(id, "transaction ID was already used for different content", false)
		return &response, true
	}
	response := previous.Response
	return &response, true
}

func normalizeLastGood(previous *config.SystemConfig, loadErr error) (*config.SystemConfig, error) {
	if loadErr != nil {
		if errors.Is(loadErr, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read last-good configuration: %w", loadErr)
	}
	if previous == nil {
		return nil, errors.New("last-good loader returned no configuration")
	}
	return previous, nil
}

func pendingLoadFailure(id string, loadErr error) apply.ApplyResponse {
	if errors.Is(loadErr, os.ErrNotExist) {
		return failure(id, "no configuration is awaiting confirmation", false)
	}
	return recoveryFailure(id, "pending confirmation state could not be read; canonical reconciliation is required")
}
