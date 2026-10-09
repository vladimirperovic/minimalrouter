package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/dnsfilter"
)

type recoveryAssessment struct {
	apply.ChangePreview
	CanApply bool     `json:"can_apply"`
	Blockers []string `json:"blockers"`
}

func (s *Server) assessRecovery(candidate config.SystemConfig, remote string) recoveryAssessment {
	result := recoveryAssessment{Blockers: []string{}}
	preview, err := apply.PreviewTransition(s.engine.GetCurrentConfig(), candidate)
	result.ChangePreview = preview
	if result.Changes == nil {
		result.Changes = []string{}
	}
	if err != nil {
		result.Blockers = append(result.Blockers, err.Error())
	}
	if err := managementContinuityErr(candidate, remote); err != nil {
		result.Blockers = append(result.Blockers, err.Error())
	}
	if s.engine.GetPendingTransaction() != nil {
		result.Blockers = append(result.Blockers, "Confirm or roll back the pending network change first.")
	}
	if s.engine.GetStatus().RecoveryRequired {
		result.Blockers = append(result.Blockers, "Canonical reconciliation is required before restoring.")
	}
	result.CanApply = len(result.Blockers) == 0
	return result
}

func normalizeDNS(policy *dnsfilter.Policy) *dnsfilter.Policy {
	if policy == nil {
		return nil
	}
	copy := dnsfilter.DefaultPolicy()
	copy.Revision = policy.Revision
	for key, value := range policy.Categories {
		copy.Categories[key] = value
	}
	copy.Exceptions = append(copy.Exceptions, policy.Exceptions...)
	return &copy
}

func (s *Server) recoveryPreview(w http.ResponseWriter, r *http.Request, candidate config.SystemConfig, kind string, policy *dnsfilter.Policy, report *config.PfSenseImportReport) {
	session, err := s.requestSession(r)
	if err != nil {
		http.Error(w, "Authenticated session required", 401)
		return
	}
	if r.Context().Err() != nil {
		return
	}
	var entropy [24]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		http.Error(w, "Preview unavailable", 500)
		return
	}
	id := base64.RawURLEncoding.EncodeToString(entropy[:])
	candidate.Revision = s.engine.GetCurrentConfig().Revision
	expires := time.Now().UTC().Add(10 * time.Minute)
	s.mu.Lock()
	if r.Context().Err() != nil {
		s.mu.Unlock()
		return
	}
	for key, pending := range s.pendingImports {
		if time.Now().After(pending.expiresAt) || pending.sessionID == session.ID {
			delete(s.pendingImports, key)
		}
	}
	if len(s.pendingImports) >= 32 {
		s.mu.Unlock()
		http.Error(w, "Too many active previews; retry later", 503)
		return
	}
	s.pendingImports[id] = pendingPfSenseImport{sessionID: session.ID, config: candidate, expiresAt: expires, kind: kind, dnsPolicy: normalizeDNS(policy)}
	s.mu.Unlock()
	result := map[string]any{"import_id": id, "source": kind, "base_revision": candidate.Revision, "expires_at": expires, "expires_in_seconds": 600,
		"candidate": redactConfig(candidate), "assessment": s.assessRecovery(candidate, r.RemoteAddr), "dns_filter": normalizeDNS(policy)}
	if report != nil {
		report.Config = redactConfig(candidate)
		result["report"] = report
	}
	writeGatewayJSON(w, 200, result)
}

func (s *Server) applyRecoveryPreview(w http.ResponseWriter, r *http.Request, kind string) {
	session, err := s.requestSession(r)
	if err != nil {
		http.Error(w, "Authenticated session required", 401)
		return
	}
	id := r.PathValue("id")
	s.mu.Lock()
	pending, ok := s.pendingImports[id]
	// A wrong session/source must not consume another operator's preview.
	if !ok || pending.sessionID != session.ID || pending.kind != kind || time.Now().After(pending.expiresAt) {
		s.mu.Unlock()
		http.Error(w, "Preview expired or replaced; validate again", 404)
		return
	}
	delete(s.pendingImports, id)
	s.mu.Unlock()
	if pending.config.Revision != s.engine.GetCurrentConfig().Revision {
		http.Error(w, "Configuration changed after preview; validate again", 409)
		return
	}
	assessment := s.assessRecovery(pending.config, r.RemoteAddr)
	if !assessment.CanApply {
		writeGatewayJSON(w, 422, map[string]any{"error": "Restore is blocked", "assessment": assessment})
		return
	}
	s.performRecovery(w, r, pending.config, kind, pending.dnsPolicy)
}

type recoveryOperation struct {
	ID              string            `json:"id"`
	Source          string            `json:"source"`
	State           string            `json:"state"`
	TransactionID   string            `json:"transaction_id"`
	TargetRevision  config.Revision   `json:"target_revision"`
	TargetChecksum  string            `json:"target_checksum"`
	StartedAt       time.Time         `json:"started_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	Deadline        *time.Time        `json:"confirmation_deadline,omitempty"`
	DNSPolicy       *dnsfilter.Policy `json:"dns_policy,omitempty"`
	DNSBaseRevision uint64            `json:"dns_base_revision,omitempty"`
	Error           string            `json:"error,omitempty"`
}

func configurationFingerprint(cfg config.SystemConfig) string {
	cfg.Revision = 0
	cfg.UpdatedAt = time.Time{}
	data, _ := json.Marshal(cfg)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func operationFinished(state string) bool {
	switch state {
	case "completed", "failed", "rolled_back", "needs_review", "dismissed":
		return true
	}
	return false
}

// Reconcile operation metadata from authoritative state. Observed final outcomes
// are retained, so a later unrelated edit cannot rewrite a completed restore.
// Status reads never start work or infer success from an HTTP 202 response.
// Callers serialize reconciliation and mutations with recoveryMu.
func (s *Server) recoveryOperation(ctx context.Context) (result *recoveryOperation, resultErr error) {
	store := s.engine.GetStore()
	if store == nil {
		return nil, errors.New("Recovery store unavailable")
	}
	var op *recoveryOperation
	if err := store.ReadRecoveryOperation(&op); err != nil {
		return nil, err
	}
	if op == nil || operationFinished(op.State) {
		return op, nil
	}
	previousState := op.State
	defer func() {
		if resultErr == nil && op.State != previousState && (operationFinished(op.State) || op.State == "dns_failed") {
			op.UpdatedAt = time.Now().UTC()
			if err := store.SaveRecoveryOperation(op); err != nil {
				resultErr = fmt.Errorf("could not retain recovery outcome: %w", err)
			}
		}
	}()
	if tx := s.engine.GetPendingTransaction(); tx != nil && tx.ID == op.TransactionID {
		op.State = "awaiting_confirmation"
		op.Deadline = tx.ConfirmationDeadline
		return op, nil
	}
	current := s.engine.GetCurrentConfig()
	if current.Revision != op.TargetRevision || configurationFingerprint(current) != op.TargetChecksum {
		status := s.engine.GetStatus()
		op.State = "needs_review"
		op.Error = "The restored configuration is no longer active. Inspect the outcome before starting a new restore."
		if status.ActiveTransactionID == op.TransactionID && status.ActiveState == apply.StateRolledBack {
			op.State = "rolled_back"
			op.Error = "The network change was rolled back. DNS restoration was not continued."
		}
		return op, nil
	}
	op.Deadline = nil
	if op.DNSPolicy == nil {
		op.State = "completed"
		return op, nil
	}
	if op.State != "dns_running" {
		if op.State != "dns_failed" {
			op.State = "dns_pending"
		}
		return op, nil
	}
	if s.dnsFilter == nil {
		op.State = "dns_failed"
		op.Error = "DNS protection unavailable. Retry when the service is ready."
		return op, nil
	}
	status, err := s.dnsFilter.Status(ctx)
	if err != nil {
		op.Error = "DNS operation status unavailable; refresh to check its outcome."
		return op, nil
	}
	if status.Updating {
		return op, nil
	}
	actual, expected := normalizeDNS(&status.Policy), normalizeDNS(op.DNSPolicy)
	actual.Revision = 0
	expected.Revision = 0
	if status.Healthy && status.Policy.Revision > op.DNSBaseRevision && config.EqualSection(actual, expected) {
		op.State = "completed"
		op.Error = ""
	} else {
		op.State = "dns_failed"
		op.Error = status.Error
		if op.Error == "" {
			op.Error = "DNS restoration did not finish with the expected healthy policy. Review DNS Filter, then retry."
		}
	}
	return op, nil
}

func (s *Server) performRecovery(w http.ResponseWriter, r *http.Request, candidate config.SystemConfig, source string, policy *dnsfilter.Policy) {
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	previous, err := s.recoveryOperation(r.Context())
	if err != nil {
		http.Error(w, "Recovery state unavailable; no change applied", 503)
		return
	}
	if previous != nil && !operationFinished(previous.State) {
		http.Error(w, "Finish or dismiss the existing restore operation first", 409)
		return
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		http.Error(w, "Recovery unavailable", 500)
		return
	}
	id := "recovery-" + hex.EncodeToString(entropy[:])
	op := recoveryOperation{ID: id, Source: source, State: "applying", TransactionID: id, TargetRevision: candidate.Revision + 1,
		TargetChecksum: configurationFingerprint(candidate), StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), DNSPolicy: normalizeDNS(policy)}
	store := s.engine.GetStore()
	if err := store.SaveRecoveryOperation(op); err != nil {
		http.Error(w, "Cannot persist restore operation; no change applied", 503)
		return
	}
	tx, applyErr := s.engine.ProcessTransaction(id, candidate)
	s.auditConfigRequest(r, tx)
	op.UpdatedAt = time.Now().UTC()
	if applyErr != nil {
		op.State = "failed"
		op.Error = "Configuration restore failed; inspect the transaction and retry a fresh preview."
	} else if tx.CurrentState == apply.StateAwaitingConfirmation {
		op.State = "awaiting_confirmation"
		op.Deadline = tx.ConfirmationDeadline
	} else if policy != nil {
		op.State = "dns_pending"
	} else {
		op.State = "completed"
	}
	if err := store.SaveRecoveryOperation(op); err != nil {
		http.Error(w, "Restore was submitted but its record could not be updated; inspect Recovery status before retrying", 503)
		return
	}
	if applyErr != nil {
		writeGatewayJSON(w, 422, map[string]any{"error": applyErr.Error(), "operation": op, "tx": redactTransaction(tx)})
		return
	}
	code := 200
	if tx.CurrentState == apply.StateAwaitingConfirmation {
		code = 202
	}
	// Preserve the existing transaction response contract.
	writeGatewayJSON(w, code, redactTransaction(tx))
}

func (s *Server) handleRecoveryStatus(w http.ResponseWriter, r *http.Request) {
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	op, err := s.recoveryOperation(r.Context())
	if err != nil {
		http.Error(w, "Recovery status unavailable", 503)
		return
	}
	store := s.engine.GetStore()
	last, err := store.LastBackupExport()
	if err != nil {
		http.Error(w, "Backup export history unavailable", 503)
		return
	}
	snapshots, err := store.ListSnapshots()
	if err != nil {
		http.Error(w, "Snapshot history unavailable", 503)
		return
	}
	var pending any
	if tx := s.engine.GetPendingTransaction(); tx != nil {
		pending = map[string]any{"id": tx.ID, "state": tx.CurrentState, "confirmation_deadline": tx.ConfirmationDeadline, "management_access": tx.Config.System.ManagementAccess}
	}
	writeGatewayJSON(w, 200, map[string]any{"generated_at": time.Now().UTC(), "revision": s.engine.GetCurrentConfig().Revision,
		"last_backup_export_at": last, "backup_export_note": "Server export recorded; file storage and restore testing are not verified.",
		"snapshot_count": len(snapshots), "retention": map[string]int{"manual": 20, "automatic": 20}, "pending": pending, "operation": op, "engine": s.engine.GetStatus()})
}

func (s *Server) handleRecoveryDNS(w http.ResponseWriter, r *http.Request) {
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	op, err := s.recoveryOperation(r.Context())
	if err != nil || op == nil || op.ID != r.PathValue("id") {
		http.Error(w, "Restore operation unavailable", 404)
		return
	}
	engine := s.engine.GetStatus()
	if s.engine.GetPendingTransaction() != nil || engine.RecoveryRequired || engine.Applying || (op.State != "dns_pending" && op.State != "dns_failed") {
		http.Error(w, "Confirm the restored network configuration before continuing DNS restoration", 409)
		return
	}
	if s.dnsFilter == nil {
		http.Error(w, "DNS protection unavailable", 503)
		return
	}
	status, err := s.dnsFilter.Status(r.Context())
	if err != nil {
		http.Error(w, "DNS status unavailable", 503)
		return
	}
	if status.Updating {
		http.Error(w, "Wait for the current DNS operation to finish", 409)
		return
	}
	op.DNSBaseRevision = status.Policy.Revision
	op.State = "dns_running"
	op.Error = ""
	op.UpdatedAt = time.Now().UTC()
	if err := s.engine.GetStore().SaveRecoveryOperation(op); err != nil {
		http.Error(w, "Cannot persist DNS continuation", 503)
		return
	}
	policy := *normalizeDNS(op.DNSPolicy)
	policy.Revision = status.Policy.Revision
	if err := s.dnsFilter.Start(policy, false); err != nil {
		op.State = "dns_failed"
		op.Error = err.Error()
		_ = s.engine.GetStore().SaveRecoveryOperation(op)
		http.Error(w, err.Error(), 409)
		return
	}
	s.appendAudit("recovery.dns_requested", auditActor(r.RemoteAddr), map[string]string{"operation_id": op.ID})
	writeGatewayJSON(w, 202, op)
}

func (s *Server) handleRecoveryDismiss(w http.ResponseWriter, r *http.Request) {
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	op, err := s.recoveryOperation(r.Context())
	if err != nil || op == nil || op.ID != r.PathValue("id") {
		http.Error(w, "Restore operation unavailable", 404)
		return
	}
	if s.engine.GetPendingTransaction() != nil || op.State == "dns_running" || op.State == "applying" {
		http.Error(w, "Wait for the active operation to finish", 409)
		return
	}
	op.State = "dismissed"
	op.DNSPolicy = nil
	op.Error = "DNS continuation dismissed by the operator."
	op.UpdatedAt = time.Now().UTC()
	if err := s.engine.GetStore().SaveRecoveryOperation(op); err != nil {
		http.Error(w, "Could not dismiss operation", 503)
		return
	}
	writeGatewayJSON(w, 200, op)
}

func (s *Server) snapshotCandidate(id string) (config.SystemConfig, error) {
	if s.engine.GetStore() == nil {
		return config.SystemConfig{}, errors.New("Snapshot store unavailable")
	}
	snap, err := s.engine.GetStore().GetSnapshot(id)
	if err != nil {
		return config.SystemConfig{}, err
	}
	var cfg config.SystemConfig
	if err := json.Unmarshal([]byte(snap.ConfigJSON), &cfg); err != nil {
		return cfg, fmt.Errorf("Snapshot is corrupted")
	}
	cfg.MigrateLegacyFields()
	cfg.Revision = s.engine.GetCurrentConfig().Revision
	return cfg, nil
}

func (s *Server) handleSnapshotPreview(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.snapshotCandidate(r.PathValue("id"))
	if err != nil {
		snapshotReadError(w, err)
		return
	}
	writeGatewayJSON(w, 200, map[string]any{"snapshot_id": r.PathValue("id"), "source": "snapshot", "base_revision": cfg.Revision,
		"candidate": redactConfig(cfg), "assessment": s.assessRecovery(cfg, r.RemoteAddr)})
}

func snapshotReadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, config.ErrSnapshotNotFound):
		http.Error(w, "Snapshot not found", http.StatusNotFound)
	case errors.Is(err, config.ErrSnapshotCorrupt):
		http.Error(w, "Snapshot integrity check failed; this restore point cannot be used", http.StatusUnprocessableEntity)
	default:
		http.Error(w, "Snapshot storage unavailable; retry after checking appliance health", http.StatusServiceUnavailable)
	}
}
