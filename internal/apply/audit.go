package apply

import (
	"fmt"
	"log"
	"strconv"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// Audit outcomes separately from HTTP requests, including timer-driven rollback.
// Never persist Error, Diff, helper output or config: those may contain secrets.
// Failure to write diagnostic metadata must not prevent safety rollback.
func (e *Engine) auditTransaction(tx *Transaction, previous config.Revision, phase string) {
	if e.store == nil || tx == nil {
		return
	}
	reason := "state_recorded"
	switch tx.CurrentState {
	case StateCommitted:
		reason = "configuration_committed"
	case StateAwaitingConfirmation:
		reason = "confirmation_required"
	case StateRejected:
		reason = "configuration_rejected"
	case StateRolledBack:
		reason = "previous_configuration_restored"
	case StateRecoveryRequired:
		reason = "recovery_required"
	}
	if phase == "timeout_rollback" {
		reason = "confirmation_deadline_expired"
	}
	if err := e.store.AppendAuditEvent("config.transaction", "local", map[string]string{
		"transaction_id": tx.ID, "state": string(tx.CurrentState), "phase": phase, "reason": reason,
		"previous_revision": fmt.Sprint(previous), "candidate_revision": fmt.Sprint(tx.Config.Revision),
		"has_error": strconv.FormatBool(tx.Error != ""),
	}); err != nil {
		log.Printf("[AUDIT] failed to persist transaction outcome: %v", err)
	}
}
