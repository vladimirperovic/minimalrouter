package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// UpdateState is the operator-visible phase of one accepted update.
//
// The dashboard shows these; it must never invent progress the appliance has
// not reached. Only the terminal states below end an operation.
type UpdateState string

const (
	UpdateQueued           UpdateState = "queued"
	UpdateDownloading      UpdateState = "downloading"
	UpdateVerifying        UpdateState = "verifying"
	UpdateStaging          UpdateState = "staging"
	UpdateActivating       UpdateState = "activating"
	UpdateCheckingHealth   UpdateState = "checking_health"
	UpdateSucceeded        UpdateState = "succeeded"
	UpdateFailed           UpdateState = "failed"
	UpdateRollingBack      UpdateState = "rolling_back"
	UpdateRolledBack       UpdateState = "rolled_back"
	UpdateRecoveryRequired UpdateState = "recovery_required"
)

// Terminal reports whether the operation has finished, successfully or not.
func (s UpdateState) Terminal() bool {
	switch s {
	case UpdateSucceeded, UpdateFailed, UpdateRolledBack, UpdateRecoveryRequired:
		return true
	default:
		return false
	}
}

// pastActivation reports whether the slot pointer may already have moved. An
// operation interrupted before this point can be failed safely; after it, the
// authoritative outcome lives in the updater's own slot journal.
func (s UpdateState) pastActivation() bool {
	switch s {
	case UpdateActivating, UpdateCheckingHealth, UpdateRollingBack:
		return true
	default:
		return false
	}
}

// UpdateOperation is the durable record of one accepted update. It exists so
// the answer to "what happened to my update?" survives closing the tab, losing
// the response, and restarting routerd — none of which may change the outcome.
type UpdateOperation struct {
	ID             string      `json:"id"`
	State          UpdateState `json:"state"`
	FromVersion    string      `json:"from_version"`
	TargetVersion  string      `json:"target_version"`
	CandidateID    string      `json:"candidate_id"`
	IdempotencyKey string      `json:"idempotency_key,omitempty"`
	Source         string      `json:"source"`
	StartedAt      time.Time   `json:"started_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
	CompletedAt    *time.Time  `json:"completed_at,omitempty"`
	ErrorCode      string      `json:"error_code,omitempty"`
	Error          string      `json:"error,omitempty"`
}

// updateOperationStore persists exactly one operation record. A single record
// is deliberate: two concurrent updates on one appliance are never valid, so
// the file itself is the mutual exclusion that survives a restart.
type updateOperationStore struct {
	path string

	mu      sync.Mutex
	current *UpdateOperation
	loaded  bool
}

func newUpdateOperationStore(path string) *updateOperationStore {
	return &updateOperationStore{path: path}
}

var errUpdateInProgress = errors.New("another update is already running")

func (s *updateOperationStore) loadLocked() error {
	if s.loaded {
		return nil
	}
	data, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.loaded = true
		return nil
	case err != nil:
		return err
	}
	var operation UpdateOperation
	if err := json.Unmarshal(data, &operation); err != nil {
		// A record that cannot be read must not silently unlock a new update:
		// report it and let the caller decide.
		return fmt.Errorf("update operation state is unreadable: %w", err)
	}
	s.current = &operation
	s.loaded = true
	return nil
}

func (s *updateOperationStore) saveLocked() error {
	if s.current == nil {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(s.current, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".update-operation-")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		os.Remove(tempName)
		return err
	}
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		os.Remove(tempName)
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		os.Remove(tempName)
		return err
	}
	if err := temp.Close(); err != nil {
		os.Remove(tempName)
		return err
	}
	return os.Rename(tempName, s.path)
}

// Current returns a copy of the recorded operation, if any.
func (s *updateOperationStore) Current() (*UpdateOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return nil, err
	}
	if s.current == nil {
		return nil, nil
	}
	copied := *s.current
	return &copied, nil
}

// Begin records a new operation, refusing while one is still running. An
// identical idempotency key returns the existing operation instead of starting
// a second one, so a retried request after a lost response cannot install
// twice.
func (s *updateOperationStore) Begin(operation UpdateOperation) (*UpdateOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return nil, err
	}
	if s.current != nil && !s.current.State.Terminal() {
		if operation.IdempotencyKey != "" && operation.IdempotencyKey == s.current.IdempotencyKey {
			copied := *s.current
			return &copied, nil
		}
		return nil, errUpdateInProgress
	}
	if s.current != nil && operation.IdempotencyKey != "" &&
		operation.IdempotencyKey == s.current.IdempotencyKey {
		// The work already ran to completion under this key. Report that
		// outcome rather than repeating an install the caller already has.
		copied := *s.current
		return &copied, nil
	}
	operation.UpdatedAt = operation.StartedAt
	s.current = &operation
	if err := s.saveLocked(); err != nil {
		s.current = nil
		return nil, err
	}
	copied := operation
	return &copied, nil
}

// Advance moves the recorded operation to a new state.
func (s *updateOperationStore) Advance(id string, state UpdateState, now time.Time) error {
	return s.update(id, now, func(operation *UpdateOperation) {
		operation.State = state
	})
}

// Finish records a terminal outcome with a machine-readable code.
func (s *updateOperationStore) Finish(id string, state UpdateState, code, message string, now time.Time) error {
	return s.update(id, now, func(operation *UpdateOperation) {
		operation.State = state
		operation.ErrorCode = code
		operation.Error = message
		completed := now
		operation.CompletedAt = &completed
	})
}

func (s *updateOperationStore) update(id string, now time.Time, mutate func(*UpdateOperation)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return err
	}
	if s.current == nil || s.current.ID != id {
		return fmt.Errorf("update operation %q is not the recorded operation", id)
	}
	mutate(s.current)
	s.current.UpdatedAt = now
	return s.saveLocked()
}

// RecoverInterrupted resolves a record left behind by a restart. routerd
// starting means this process is not running that operation any more, so a
// non-terminal record is finished here rather than left to look active
// forever. The distinction that matters is whether activation could already
// have moved the slot pointer: before it, nothing was installed and a retry is
// safe; after it, the updater's own journal is authoritative and the operator
// is told to check the resulting version rather than being handed an automatic
// retry.
func (s *updateOperationStore) RecoverInterrupted(now time.Time) (*UpdateOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return nil, err
	}
	if s.current == nil || s.current.State.Terminal() {
		return nil, nil
	}
	if s.current.State.pastActivation() {
		s.current.State = UpdateRecoveryRequired
		s.current.ErrorCode = "interrupted_during_activation"
		s.current.Error = "The management service restarted while the new release was being activated. " +
			"The privileged updater's slot journal holds the authoritative result; verify the running version before retrying."
	} else {
		s.current.State = UpdateFailed
		s.current.ErrorCode = "interrupted"
		s.current.Error = "The management service restarted before the release was activated. " +
			"Nothing was installed; the update can be started again."
	}
	completed := now
	s.current.CompletedAt = &completed
	s.current.UpdatedAt = now
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	copied := *s.current
	return &copied, nil
}
