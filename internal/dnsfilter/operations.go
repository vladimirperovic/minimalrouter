package dnsfilter

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const OperationRetention = 20

// Operation contains bounded diagnostic metadata, never credentials or domain
// lists. TargetHash binds restart reconciliation to the exact requested policy.
type Operation struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	State           string     `json:"state"`
	Phase           string     `json:"phase"`
	Actor           string     `json:"actor"`
	BaseRevision    uint64     `json:"base_revision"`
	TargetRevision  uint64     `json:"target_revision"`
	AppliedRevision uint64     `json:"applied_revision,omitempty"`
	TargetHash      string     `json:"target_hash"`
	StartedAt       time.Time  `json:"started_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	Error           string     `json:"error,omitempty"`
}

type operationJournal struct {
	LastAttempt time.Time   `json:"last_attempt"`
	Operations  []Operation `json:"operations"`
}

func policyHash(p Policy) string {
	next := DefaultPolicy()
	for key, enabled := range p.Categories {
		if enabled {
			next.Categories[key] = true
		}
	}
	next.Exceptions = append(next.Exceptions, p.Exceptions...)
	sort.Slice(next.Exceptions, func(i, j int) bool { return next.Exceptions[i].Domain < next.Exceptions[j].Domain })
	data, _ := json.Marshal(next)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (s *Service) openJournal() error {
	path := filepath.Join(s.dir, "operations.db")
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(1000)&_pragma=synchronous(FULL)&_pragma=trusted_schema(OFF)")
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	s.journal = db
	if _, err = db.Exec("CREATE TABLE IF NOT EXISTS state(id INTEGER PRIMARY KEY CHECK(id=1),body TEXT NOT NULL)"); err != nil {
		db.Close()
		return err
	}
	if err = os.Chmod(path, 0600); err != nil {
		db.Close()
		return err
	}
	var data string
	err = db.QueryRow("SELECT body FROM state WHERE id=1").Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		db.Close()
		return err
	}
	var journal operationJournal
	if len(data) > 128<<10 || json.Unmarshal([]byte(data), &journal) != nil || len(journal.Operations) > OperationRetention {
		db.Close()
		return errors.New("DNS operation history is invalid")
	}
	s.operations, s.lastAttempt = journal.Operations, journal.LastAttempt
	// A restart is not proof of success, and never replays a mutation. A status
	// read can later confirm the exact target revision against the root helper.
	for i := range s.operations {
		if s.operations[i].State == "running" {
			s.operations[i].State = "needs_review"
			s.operations[i].Phase = "interrupted"
			s.operations[i].Error = "Router management restarted before the DNS outcome was recorded. Verify the applied policy."
			s.operations[i].UpdatedAt = time.Now().UTC()
		}
	}
	return s.saveJournalLocked()
}

func (s *Service) saveJournalLocked() error {
	if s.journal == nil {
		return nil
	} // In-memory unit-test services only.
	data, err := json.Marshal(operationJournal{s.lastAttempt, s.operations})
	if err != nil {
		return err
	}
	_, err = s.journal.Exec("INSERT INTO state(id,body) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", string(data))
	return err
}

// SetObserver records requests and terminal outcomes in the appliance audit.
// The callback must not call Service methods; it runs with the service locked.
func (s *Service) SetObserver(observer func(Operation)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observer = observer
}

func (s *Service) History() []Operation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Operation{}, s.operations...)
}

func (s *Service) operationPhase(phase string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.operations) == 0 {
		return nil
	}
	s.operations[0].Phase = phase
	s.operations[0].UpdatedAt = time.Now().UTC()
	return s.saveJournalLocked()
}

func (s *Service) reconcileOperationLocked(state Applied) {
	if len(s.operations) == 0 || s.operations[0].State != "needs_review" || state.ActivationPending || !state.Healthy {
		return
	}
	op := &s.operations[0]
	if op.Phase == "recovery_required" {
		return
	}
	if state.Policy.Revision != op.TargetRevision || policyHash(state.Policy) != op.TargetHash {
		return
	}
	previous := *op
	now := time.Now().UTC()
	op.State, op.Phase, op.Error = "completed", "verified", ""
	op.AppliedRevision, op.UpdatedAt, op.FinishedAt = state.Policy.Revision, now, &now
	if err := s.saveJournalLocked(); err != nil {
		*op = previous
		return
	}
	s.lastError = ""
	if s.observer != nil {
		s.observer(*op)
	}
}

func newOperation(policy Policy, refresh bool, actor string) Operation {
	var id [16]byte
	_, _ = rand.Read(id[:]) // crypto/rand.Read cannot fail in supported Go runtimes.
	now := time.Now().UTC()
	kind := "policy"
	if refresh {
		kind = "refresh"
	}
	return Operation{ID: hex.EncodeToString(id[:]), Kind: kind, State: "running", Phase: "queued", Actor: actor, BaseRevision: policy.Revision, TargetRevision: policy.Revision + 1, TargetHash: policyHash(policy), StartedAt: now, UpdatedAt: now}
}
