package dnsfilter

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Helper interface {
	Call(context.Context, Request, func(io.Writer) error) (Applied, error)
}

type Status struct {
	Applied
	SourcesStatus    []Catalog  `json:"lists"`
	Updating         bool       `json:"updating"`
	Error            string     `json:"error,omitempty"`
	NextRefreshAt    int64      `json:"next_refresh_at"`
	RefreshAllowedAt int64      `json:"refresh_allowed_at"`
	Operation        *Operation `json:"operation,omitempty"`
	GeneratedAt      time.Time  `json:"generated_at"`
}

type Service struct {
	dir         string
	helper      Helper
	allowWrite  func() bool
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	mu          sync.Mutex
	busy        bool
	lastError   string
	lastAttempt time.Time
	closed      bool
	generation  uint64
	journal     *sql.DB
	operations  []Operation
	observer    func(Operation)
	download    func(context.Context, string, Source, Catalog, func() bool) (Catalog, error)
}

func Open(dataDir string, helper Helper, allowWrite func() bool) (*Service, error) {
	dir := filepath.Join(dataDir, "dns-filter")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{dir: dir, helper: helper, allowWrite: allowWrite, ctx: ctx, cancel: cancel, download: downloadCatalog}
	if err := s.openJournal(); err != nil {
		cancel()
		if s.journal != nil {
			s.journal.Close()
		}
		return nil, err
	}
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.run() }()
	return s, nil
}

func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
	if s.journal != nil {
		s.journal.Close()
	}
}

func (s *Service) catalog(source Source, hash string) (Catalog, int, error) {
	var chosen Catalog
	slot := -1
	for i := 0; i < 2; i++ {
		meta, err := readCatalog(catalogPath(s.dir, source.ID, i), source)
		if err != nil {
			continue
		}
		if hash != "" && meta.Hash == hash {
			return meta, i, nil
		}
		if hash == "" && meta.UpdatedAt >= chosen.UpdatedAt {
			chosen = meta
			slot = i
		}
	}
	if slot < 0 {
		return Catalog{Category: source.ID, Label: source.Label, URL: source.URL()}, -1, errors.New("list not downloaded")
	}
	return chosen, slot, nil
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	s.mu.Lock()
	generation := s.generation
	s.mu.Unlock()
	state, err := s.helper.Call(ctx, Request{Operation: "status"}, nil)
	if err != nil {
		return Status{}, err
	}
	status := Status{Applied: state, SourcesStatus: []Catalog{}, GeneratedAt: time.Now().UTC()}
	for _, source := range Sources() {
		meta, _, err := s.catalog(source, state.Sources[source.ID])
		if err != nil && state.Policy.Categories[source.ID] {
			meta.Error = "Installed list index unavailable; domain checks cannot verify it"
			// The root-owned rules remain active, but a lost local index must
			// schedule rebuilding rather than disabling automatic refresh forever.
			status.NextRefreshAt = time.Now().Unix()
		}
		status.SourcesStatus = append(status.SourcesStatus, meta)
		if state.Policy.Categories[source.ID] && meta.UpdatedAt > 0 && (status.NextRefreshAt == 0 || meta.UpdatedAt+86400 < status.NextRefreshAt) {
			status.NextRefreshAt = meta.UpdatedAt + 86400
		}
	}
	s.mu.Lock()
	s.reconcileOperationLocked(state)
	status.Updating = s.busy || generation != s.generation
	status.Error = s.lastError
	if len(s.operations) > 0 {
		op := s.operations[0]
		status.Operation = &op
		if status.Error == "" {
			status.Error = op.Error
		}
	}
	if !s.lastAttempt.IsZero() {
		status.RefreshAllowedAt = s.lastAttempt.Add(5 * time.Minute).Unix()
	}
	if status.NextRefreshAt > 0 && !s.lastAttempt.IsZero() && status.NextRefreshAt < s.lastAttempt.Add(6*time.Hour).Unix() {
		status.NextRefreshAt = s.lastAttempt.Add(6 * time.Hour).Unix()
	}
	s.mu.Unlock()
	return status, nil
}

// Start accepts a revision-bound policy. The expensive download/build happens
// in one owned worker, while the previously verified DNS rules keep running.
func (s *Service) Start(policy Policy, refresh bool) error {
	_, err := s.StartOperation(policy, refresh, "system")
	return err
}

func (s *Service) StartOperation(policy Policy, refresh bool, actor string) (Operation, error) {
	if err := policy.Validate(); err != nil {
		return Operation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Operation{}, errors.New("DNS filter is stopping")
	}
	if s.busy {
		return Operation{}, errors.New("DNS filter update is already running")
	}
	if refresh && time.Since(s.lastAttempt) < 5*time.Minute {
		return Operation{}, errors.New("wait five minutes before refreshing lists again")
	}
	if policy.Revision == ^uint64(0) {
		return Operation{}, errors.New("DNS filter revision exhausted")
	}
	// Detach maps/slices owned by an HTTP caller before the asynchronous work.
	next := DefaultPolicy()
	next.Revision = policy.Revision
	for k, v := range policy.Categories {
		next.Categories[k] = v
	}
	next.Exceptions = append(next.Exceptions, policy.Exceptions...)
	op := newOperation(next, refresh, actor)
	oldOperations, oldAttempt := s.operations, s.lastAttempt
	s.operations = append([]Operation{op}, s.operations...)
	if len(s.operations) > OperationRetention {
		s.operations = s.operations[:OperationRetention]
	}
	s.lastAttempt = op.StartedAt
	if err := s.saveJournalLocked(); err != nil {
		s.operations, s.lastAttempt = oldOperations, oldAttempt
		return Operation{}, errors.New("DNS operation could not be recorded; no change started")
	}
	if s.observer != nil {
		s.observer(op)
	}
	s.busy = true
	s.generation++
	s.lastError = ""
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		err := s.apply(s.ctx, next, refresh)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.busy = false
		s.generation++
		finished := time.Now().UTC()
		current := &s.operations[0]
		current.UpdatedAt, current.FinishedAt = finished, &finished
		current.State, current.AppliedRevision = "completed", next.Revision+1
		if err == nil {
			current.Phase = "verified"
		}
		if err != nil {
			s.lastError = err.Error()
			current.State, current.AppliedRevision, current.Error = "failed", 0, err.Error()
			if strings.Contains(err.Error(), "previous DNS configuration restored") {
				current.State = "rolled_back"
			}
			if strings.Contains(err.Error(), "outcome unconfirmed") {
				current.State = "needs_review"
				current.Phase = "outcome_unknown"
			}
			if strings.Contains(err.Error(), "recovery required") {
				current.State = "needs_review"
				current.Phase = "recovery_required"
			}
		}
		if persistErr := s.saveJournalLocked(); persistErr != nil {
			current.State = "needs_review"
			current.Error = "DNS outcome could not be recorded. Refresh to verify the applied policy."
			s.lastError = current.Error
		}
		if s.observer != nil {
			s.observer(*current)
		}
	}()
	return op, nil
}

func (s *Service) apply(ctx context.Context, policy Policy, refresh bool) error {
	if err := s.operationPhase("preparing"); err != nil {
		return errors.New("DNS operation history unavailable; no change applied")
	}
	before, err := s.helper.Call(ctx, Request{Operation: "status"}, nil)
	if err != nil {
		return err
	}
	if before.Policy.Revision != policy.Revision {
		return errors.New("DNS filter changed in another session; reload before saving")
	}
	paths := []string{}
	hashes := map[string]string{}
	total := 0
	for _, source := range Sources() {
		if !policy.Categories[source.ID] {
			continue
		}
		meta, slot, readErr := s.catalog(source, before.Sources[source.ID])
		// An explicit policy edit reuses an installed, hash-matched catalog.
		// Age alone must not prevent an offline exception or category removal.
		if readErr != nil || refresh {
			if err := s.operationPhase("downloading"); err != nil {
				return errors.New("DNS operation history unavailable; no change applied")
			}
			if s.allowWrite != nil && !s.allowWrite() {
				return errors.New("storage pressure prevents DNS list refresh; current policy retained")
			}
			nextSlot := 0
			if slot == 0 {
				nextSlot = 1
			}
			newMeta, downloadErr := s.download(ctx, catalogPath(s.dir, source.ID, nextSlot), source, meta, s.allowWrite)
			if downloadErr != nil {
				return errors.New(source.Label + ": refresh failed; current policy and lists retained (" + downloadErr.Error() + ")")
			}
			meta, slot = newMeta, nextSlot
		}
		total += meta.Entries
		if total > MaxDomains {
			return errors.New("selected lists exceed the appliance domain limit")
		}
		paths = append(paths, catalogPath(s.dir, source.ID, slot))
		hashes[source.ID] = meta.Hash
	}
	if err := s.operationPhase("applying"); err != nil {
		return errors.New("DNS operation history unavailable; no change applied")
	}
	applied, err := s.helper.Call(ctx, Request{Operation: "apply", ExpectedRevision: policy.Revision, Policy: policy, Sources: hashes}, func(w io.Writer) error {
		for _, path := range paths {
			if err := emitCatalog(ctx, path, w); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil && (!applied.Healthy || applied.ActivationPending || applied.Policy.Revision != policy.Revision+1 || policyHash(applied.Policy) != policyHash(policy)) {
		return errors.New("DNS outcome unconfirmed; verify the applied policy before retrying")
	}
	if err != nil && applied.Policy.Revision == 0 {
		return errors.New("DNS outcome unconfirmed: " + err.Error())
	}
	return err
}

func (s *Service) run() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		status, err := s.Status(ctx)
		cancel()
		if err == nil && status.NextRefreshAt > 0 && status.NextRefreshAt <= time.Now().Unix() && !status.Updating {
			_ = s.Start(status.Policy, true)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type Match struct {
	Category string `json:"category"`
	Domain   string `json:"domain"`
	Enabled  bool   `json:"enabled"`
}
type Check struct {
	Domain         string    `json:"domain"`
	Action         string    `json:"action"`
	Exception      bool      `json:"exception"`
	Matches        []Match   `json:"matches"`
	Healthy        bool      `json:"healthy"`
	PolicyRevision uint64    `json:"policy_revision"`
	CheckedAt      time.Time `json:"checked_at"`
}

func (s *Service) Check(ctx context.Context, name string) (Check, error) {
	d, ok := Domain(name)
	if !ok {
		return Check{}, errors.New("enter a public domain name without a URL or IP address")
	}
	state, err := s.helper.Call(ctx, Request{Operation: "status"}, nil)
	if err != nil {
		return Check{}, err
	}
	result := Check{Domain: d, Action: "No category block", Matches: []Match{}, Healthy: state.Healthy && !state.ActivationPending, Exception: state.Policy.Allowed(d), PolicyRevision: state.Policy.Revision, CheckedAt: time.Now().UTC()}
	for _, source := range Sources() {
		_, slot, err := s.catalog(source, state.Sources[source.ID])
		if err != nil {
			if state.Policy.Categories[source.ID] {
				return result, errors.New("installed list index is unavailable")
			}
			continue
		}
		match, err := catalogMatch(ctx, catalogPath(s.dir, source.ID, slot), d)
		if err != nil {
			return result, err
		}
		if match != "" {
			on := state.Policy.Categories[source.ID]
			result.Matches = append(result.Matches, Match{source.ID, match, on})
			if on {
				result.Action = "Block"
			}
		}
	}
	if result.Exception {
		result.Action = "Allow exception"
	}
	return result, nil
}
