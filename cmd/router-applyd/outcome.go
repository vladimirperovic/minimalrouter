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

// journalPersistenceFailure always blocks further mutation. Even when the
// previous runtime was restored, losing the durable idempotency result means a
// lost response could be replayed without proof of the first outcome.
func journalPersistenceFailure(id string, previous apply.ApplyResponse) apply.ApplyResponse {
	message := "transaction result could not be persisted; durable idempotency is unavailable and canonical reconciliation is required"
	if previous.RolledBack && !previous.RecoveryRequired {
		message = "transaction result could not be persisted; previous runtime was restored but durable idempotency is unavailable and canonical reconciliation is required"
	}
	return recoveryFailure(id, message)
}

func persistTransactionOutcome(record transactionRecord, save func(transactionRecord) error) (transactionRecord, apply.ApplyResponse) {
	response := record.Response
	if err := save(record); err != nil {
		response = journalPersistenceFailure(record.ID, response)
		record.Response = response
	}
	return record, response
}

func replayTransactionResponse(id, configHash string, previous *transactionRecord, loadErr error) (*apply.ApplyResponse, bool) {
	return replayTransactionResponseWithOverride(id, configHash, previous, loadErr, false)
}

func replayTransactionResponseWithOverride(id, configHash string, previous *transactionRecord, loadErr error, canonicalReconcile bool) (*apply.ApplyResponse, bool) {
	if loadErr != nil {
		if errors.Is(loadErr, os.ErrNotExist) || canonicalReconcile {
			return nil, false
		}
		response := recoveryFailure(id, "transaction journal could not be read; canonical reconciliation is required")
		return &response, true
	}
	if previous == nil {
		if canonicalReconcile {
			return nil, false
		}
		response := recoveryFailure(id, "transaction journal returned no record; canonical reconciliation is required")
		return &response, true
	}
	if previous.ID != id {
		unresolved := previous.CompletedAt.IsZero() || previous.Response.RecoveryRequired
		if unresolved && !canonicalReconcile {
			response := recoveryFailure(id, "a previous privileged transaction remains unresolved; canonical reconciliation is required")
			return &response, true
		}
		return nil, false
	}
	if previous.ConfigHash != configHash {
		response := failure(id, "transaction ID was already used for different content", false)
		return &response, true
	}
	if previous.CompletedAt.IsZero() {
		response := recoveryFailure(id, "privileged transaction outcome is incomplete; canonical reconciliation is required")
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
