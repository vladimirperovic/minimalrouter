package release

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestChecker(t *testing.T, handler http.HandlerFunc) (*Checker, *fakeClock, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	clock := &fakeClock{now: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)}
	checker := NewChecker(Catalog{APIURL: server.URL}, "amd64", func() Channel { return ChannelStable })
	checker.SetClock(clock.Now)
	return checker, clock, server
}

func TestSnapshotIsServedFromCacheWithoutUpstreamCalls(t *testing.T) {
	var calls int
	checker, _, _ := newTestChecker(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		fmt.Fprintf(w, "[%s]", releaseJSON("v0.1.6", false, amd64Assets()...))
	})

	if _, err := checker.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 25 {
		snapshot := checker.Snapshot()
		if snapshot.Candidate == nil || snapshot.Candidate.Version != "0.1.6" {
			t.Fatalf("cached snapshot lost the candidate: %+v", snapshot)
		}
	}
	if calls != 1 {
		t.Fatalf("upstream calls = %d; reading the status must never call GitHub", calls)
	}
}

func TestManualCheckIsRateLimitedByCooldown(t *testing.T) {
	var calls int
	checker, clock, _ := newTestChecker(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		fmt.Fprintf(w, "[%s]", releaseJSON("v0.1.6", false, amd64Assets()...))
	})

	if _, err := checker.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := checker.CheckNow(context.Background()); !errors.Is(err, ErrCheckTooSoon) {
		t.Fatalf("second immediate check err = %v, want ErrCheckTooSoon", err)
	}
	clock.Advance(DefaultCooldown + time.Second)
	if _, err := checker.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d, want 2", calls)
	}
}

func TestFailedCheckKeepsLastGoodAnswerAndMarksTheError(t *testing.T) {
	fail := false
	checker, clock, _ := newTestChecker(t, func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprintf(w, "[%s]", releaseJSON("v0.1.6", false, amd64Assets()...))
	})

	if _, err := checker.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	success := checker.Snapshot().LastSuccessAt

	fail = true
	clock.Advance(DefaultCooldown + time.Second)
	if _, err := checker.CheckNow(context.Background()); err == nil {
		t.Fatal("a failing check must report an error")
	}
	snapshot := checker.Snapshot()
	if snapshot.Error == "" {
		t.Fatal("the failure must be visible; a failed check is not 'up to date'")
	}
	if snapshot.Candidate == nil || snapshot.Candidate.Version != "0.1.6" {
		t.Fatal("the previous good answer must survive a failed check")
	}
	if !snapshot.LastSuccessAt.Equal(success) {
		t.Fatal("a failed check must not advance the last successful check time")
	}
	if snapshot.NextEarliest.IsZero() {
		t.Fatal("a failed check must schedule a backoff")
	}
}

func TestRateLimitedCheckWaitsTheRequestedInterval(t *testing.T) {
	checker, clock, _ := newTestChecker(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "300")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	if _, err := checker.CheckNow(context.Background()); err == nil {
		t.Fatal("rate limiting must be reported")
	}
	snapshot := checker.Snapshot()
	if !snapshot.RateLimited {
		t.Fatal("snapshot must record that the service throttled us")
	}
	if want := clock.Now().Add(5 * time.Minute); !snapshot.NextEarliest.Equal(want) {
		t.Fatalf("next earliest = %s, want the requested %s", snapshot.NextEarliest, want)
	}
}

func TestStaleSnapshotIsNotPresentedAsCurrent(t *testing.T) {
	checker, clock, _ := newTestChecker(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "[%s]", releaseJSON("v0.1.6", false, amd64Assets()...))
	})
	if _, err := checker.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := checker.Snapshot()
	if snapshot.Stale(clock.Now()) {
		t.Fatal("a fresh answer must not be stale")
	}
	clock.Advance(DefaultStaleAfter + time.Hour)
	if !snapshot.Stale(clock.Now()) {
		t.Fatal("an answer older than the stale window must be reported as stale, not as up to date")
	}
}

func TestNotModifiedKeepsTheSelectionAndRefreshesFreshness(t *testing.T) {
	var conditional []string
	checker, clock, _ := newTestChecker(t, func(w http.ResponseWriter, r *http.Request) {
		conditional = append(conditional, r.Header.Get("If-None-Match"))
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		fmt.Fprintf(w, "[%s]", releaseJSON("v0.1.6", false, amd64Assets()...))
	})

	if _, err := checker.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(DefaultCooldown + time.Second)
	if _, err := checker.CheckNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(conditional) != 2 || conditional[1] != `"v1"` {
		t.Fatalf("second check must send the cached validator, got %v", conditional)
	}
	snapshot := checker.Snapshot()
	if snapshot.Candidate == nil || snapshot.Candidate.Version != "0.1.6" {
		t.Fatal("a 304 must keep the previous selection")
	}
	if !snapshot.LastSuccessAt.Equal(clock.Now()) {
		t.Fatal("a 304 is a successful check and must refresh freshness")
	}
}

func TestConcurrentChecksShareOneUpstreamRequest(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	release := make(chan struct{})
	checker, _, _ := newTestChecker(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release
		fmt.Fprintf(w, "[%s]", releaseJSON("v0.1.6", false, amd64Assets()...))
	})

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, errs[0] = checker.CheckNow(context.Background())
	}()
	// Give the first call time to take the in-flight slot.
	for {
		checker.mu.Lock()
		running := checker.inFlight
		checker.mu.Unlock()
		if running {
			break
		}
		time.Sleep(time.Millisecond)
	}
	_, errs[1] = checker.CheckNow(context.Background())
	close(release)
	wg.Wait()

	if !errors.Is(errs[1], ErrCheckInProgress) {
		t.Fatalf("second concurrent check err = %v, want ErrCheckInProgress", errs[1])
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("upstream calls = %d; two tabs must share one request", calls)
	}
}
