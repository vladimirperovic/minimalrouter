package dnsfilter

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func journalService(t *testing.T, dir string, helper Helper) *Service {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{dir: dir, helper: helper, ctx: ctx, cancel: cancel}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.openJournal(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOperationsPersistBoundedHistoryOutcomesAndRefreshCooldown(t *testing.T) {
	dir := t.TempDir()
	helper := &fakeHelper{state: Applied{Policy: DefaultPolicy()}}
	s := journalService(t, dir, helper)
	events := []Operation{}
	s.SetObserver(func(op Operation) { events = append(events, op) })
	for i := 0; i < OperationRetention+2; i++ {
		p := DefaultPolicy()
		p.Revision = uint64(i)
		op, err := s.StartOperation(p, false, "test-actor")
		if err != nil || op.ID == "" || op.State != "running" {
			t.Fatalf("accept: %+v %v", op, err)
		}
		status := awaitFilter(t, s)
		if status.Operation.State != "completed" || status.Operation.AppliedRevision != p.Revision+1 {
			t.Fatalf("outcome: %+v", status.Operation)
		}
	}
	s.Close()
	if len(events) != (OperationRetention+2)*2 || events[0].State != "running" || events[1].State != "completed" {
		t.Fatalf("audit events: %+v", events)
	}
	s = journalService(t, dir, helper)
	defer s.Close()
	history := s.History()
	if len(history) != OperationRetention || history[0].Actor != "test-actor" || history[0].AppliedRevision != OperationRetention+2 {
		t.Fatalf("history: %+v", history)
	}
	if _, err := s.StartOperation(helper.state.Policy, true, "test"); err == nil {
		t.Fatal("restart bypassed refresh cooldown")
	}
	helper.reject = true
	if _, err := s.StartOperation(helper.state.Policy, false, "test"); err != nil {
		t.Fatal(err)
	}
	status := awaitFilter(t, s)
	if status.Operation.State != "failed" || status.Error == "" || status.Policy.Revision != OperationRetention+2 {
		t.Fatalf("failure replaced policy or claimed success: %+v", status)
	}
}

func TestInterruptedOperationRequiresExactHealthyTargetAndClearsOldError(t *testing.T) {
	dir := t.TempDir()
	policy := DefaultPolicy()
	policy.Revision = 7
	s := journalService(t, dir, &fakeHelper{})
	op := newOperation(policy, false, "test")
	s.operations = []Operation{op}
	if err := s.saveJournalLocked(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	helper := &fakeHelper{state: Applied{Policy: policy, Healthy: true}}
	s = journalService(t, dir, helper)
	defer s.Close()
	if s.History()[0].State != "needs_review" {
		t.Fatal("restart claimed completed")
	}
	s.lastError = "old unknown outcome"
	target := policy
	target.Revision++
	for _, state := range []Applied{{Policy: policy, Healthy: true}, {Policy: target}, {Policy: target, Healthy: true, ActivationPending: true}} {
		s.reconcileOperationLocked(state)
		if s.History()[0].State != "needs_review" {
			t.Fatal("unverified target accepted")
		}
	}
	other := DefaultPolicy()
	other.Revision = 8
	other.Categories["ads"] = true
	s.reconcileOperationLocked(Applied{Policy: other, Healthy: true})
	if s.History()[0].State != "needs_review" {
		t.Fatal("different policy accepted")
	}
	s.reconcileOperationLocked(Applied{Policy: target, Healthy: true})
	if s.History()[0].State != "completed" || s.lastError != "" {
		t.Fatal("exact target not reconciled")
	}
}

func TestOfflineExceptionEditReusesInstalledOldCatalog(t *testing.T) {
	helper := &fakeHelper{state: Applied{Policy: DefaultPolicy(), Sources: map[string]string{}}}
	s := journalService(t, t.TempDir(), helper)
	defer s.Close()
	source := Sources()[0]
	path := catalogPath(s.dir, source.ID, 0)
	meta, err := buildCatalog(context.Background(), path, source, strings.NewReader("bad.example.com\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("UPDATE metadata SET updated_at=?", time.Now().Add(-72*time.Hour).Unix())
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	helper.state.Policy.Categories[source.ID] = true
	helper.state.Sources[source.ID] = meta.Hash
	s.download = func(context.Context, string, Source, Catalog, func() bool) (Catalog, error) {
		t.Error("offline policy edit attempted a download")
		return Catalog{}, errors.New("offline")
	}
	policy := helper.state.Policy
	policy.Exceptions = []Exception{{Domain: "bad.example.com"}}
	if err := s.Start(policy, false); err != nil {
		t.Fatal(err)
	}
	status := awaitFilter(t, s)
	if status.Error != "" || status.Policy.Revision != 1 || !status.Policy.Allowed("bad.example.com") {
		t.Fatalf("offline edit: %+v", status)
	}
}
