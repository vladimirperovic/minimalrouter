package dnsrisk

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newTestService(t *testing.T) (*Service, *bool, string) {
	t.Helper()
	enabled := true
	dir := t.TempDir()
	s, err := Open(dir, func() (bool, int, error) { return enabled, 30, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.client = &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "blocklistproject.github.io" {
			t.Fatalf("unexpected source: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Etag": {"test-v1"}}, Body: io.NopCloser(strings.NewReader("# test source\nrisk.example\nsub.risk.example\nbad.github.io\n"))}, nil
	})}
	return s, &enabled, dir
}

func TestDomainBoundaries(t *testing.T) {
	for input, want := range map[string][]string{
		"WWW.Risk.Example.": {"www.risk.example", "risk.example"},
		"a.b.example.co.uk": {"a.b.example.co.uk", "b.example.co.uk", "example.co.uk"},
		"a.bad.github.io":   {"a.bad.github.io", "bad.github.io"},
		"bad.github.io":     {"bad.github.io"},
		"github.io":         nil, "co.uk": nil, "192.0.2.1": nil, "a.local": nil,
		"a.in-addr.arpa": nil, "notriskexample": nil, "https://risk.example": nil,
		"bad..example": nil, "-bad.example": nil, "bad.example/path": nil,
	} {
		if got := candidates(input); !reflect.DeepEqual(got, want) {
			t.Errorf("%q = %v, want %v", input, got, want)
		}
	}
}

func TestNetworkWideAlertsAcknowledgementExceptionsAndRestart(t *testing.T) {
	s, enabled, dir := newTestService(t)
	s.updateFeeds(context.Background(), true)
	now := time.Now().Unix()
	b := batch{lookups: []Lookup{
		{Name: "x.sub.risk.example", Address: "192.0.2.1", Count: 2, At: now - 10},
		{Name: "x.sub.risk.example", Address: "2001:db8::42", Count: 3, At: now},
		{Name: "unrelated.example", Address: "192.0.2.1", Count: 5, At: now},
		{Name: "innocent.github.io", Address: "192.0.2.1", Count: 7, At: now},
		{Name: "notrisk.example", Address: "192.0.2.1", Count: 11, At: now},
	}}
	if err := s.process(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	page, err := s.Alerts("new", "adult", 0, 50)
	if err != nil || page.Total != 1 || len(page.Alerts) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	a := page.Alerts[0]
	if a.Domain != "sub.risk.example" || a.Lookups != 5 || a.LastAddress != "2001:db8::42" || a.FirstSeen != now-10 {
		t.Fatalf("alert=%+v", a)
	}
	if err := s.Acknowledge(a.ID); err != nil {
		t.Fatal(err)
	}
	page, _ = s.Alerts("new", "adult", 0, 50)
	if page.Total != 0 {
		t.Fatal("acknowledgement did not hide new alert")
	}
	if err := s.Ignore(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Ignore(a.ID); err != nil {
		t.Fatal(err)
	}
	exceptions, _ := s.Exceptions()
	if len(exceptions) != 1 {
		t.Fatalf("exceptions=%v", exceptions)
	}
	page, _ = s.Alerts("all", "adult", 0, 50)
	if !page.Alerts[0].Ignored {
		t.Fatal("ignored alert absent from history")
	}
	// Persistent cache and history must survive a new service instance.
	reopened, err := Open(dir, func() (bool, int, error) { return *enabled, 30, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	summary, err := reopened.Summary(time.Now())
	if err != nil || summary.NewCount != 4 || summary.Total != 5 || summary.Sources[0].Entries != 3 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	if err := reopened.RemoveException(exceptions[0].ID); err != nil {
		t.Fatal(err)
	}
	*enabled = false
	summary, _ = s.Summary(time.Now())
	page, _ = s.Alerts("all", "", 0, 50)
	if summary.NewCount != 0 || summary.Total != 0 || page.Total != 0 {
		t.Fatal("disabled monitor exposed history")
	}
}

func TestRefreshKeepsLastGoodListAndUsesETag(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	now := time.Now()
	if err := s.refreshFeed(ctx, 0, now); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"", "<html>unavailable</html>", "a.example\n0.0.0.0 bad.example\n", strings.Repeat("a", 5000)} {
		s.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("If-None-Match") != "test-v1" {
				t.Fatal("missing conditional request")
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		if err := s.refreshFeed(ctx, 0, now.Add(time.Hour)); err == nil {
			t.Fatalf("accepted bad list %q", body[:min(50, len(body))])
		}
		if s.feeds[0].status.Entries != 3 || s.feeds[0].status.UpdatedAt != now.Unix() {
			t.Fatal("bad download replaced previous list")
		}
	}
	s.client.Transport = transport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 304, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err := s.refreshFeed(ctx, 0, now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if s.feeds[0].status.UpdatedAt != now.Add(24*time.Hour).Unix() {
		t.Fatal("304 did not refresh list age")
	}
	if err := s.process(ctx, batch{lookups: []Lookup{{Name: "risk.example", Count: 1}}}); err != nil {
		t.Fatal(err)
	}
	page, _ := s.Alerts("new", "adult", 0, 50)
	if page.Total != 1 {
		t.Fatal("last good list no longer matches")
	}
}

func TestFeedValidationBoundsAndStoragePressure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	count, err := buildFeed(context.Background(), path, strings.NewReader("# source\nGOOD.example\ngood.example\n"), nil)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := buildFeed(ctx, path, strings.NewReader("risk.example\n"), nil); err == nil {
		t.Fatal("ignored cancellation")
	}
	if _, err := buildFeed(context.Background(), path, strings.NewReader(strings.Repeat("risk.example\n", 5000)), func() bool { return false }); err == nil {
		t.Fatal("ignored storage pressure during build")
	}
	s, _, _ := newTestService(t)
	s.allowWrite = func() bool { return false }
	s.updateFeeds(context.Background(), true)
	if s.feeds[0].db != nil || s.feeds[0].status.Error == "" {
		t.Fatal("storage pressure did not stop update")
	}
	if err := s.process(context.Background(), batch{lookups: []Lookup{{Name: "risk.example", Count: 7}}}); err == nil {
		t.Fatal("expected unavailable check")
	}
	if s.dropped.Load() != 7 {
		t.Fatal("missing dropped coverage count")
	}
}

func TestClearInvalidatesQueuedHistoryButPreservesExceptions(t *testing.T) {
	s, _, _ := newTestService(t)
	s.updateFeeds(context.Background(), true)
	lookup := []Lookup{{Name: "risk.example", Count: 1}}
	if err := s.process(context.Background(), batch{lookups: lookup}); err != nil {
		t.Fatal(err)
	}
	page, _ := s.Alerts("new", "adult", 0, 1)
	if err := s.Ignore(page.Alerts[0].ID); err != nil {
		t.Fatal(err)
	}
	s.Submit(lookup)
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := s.process(context.Background(), <-s.queue); err != nil {
		t.Fatal(err)
	}
	summary, _ := s.Summary(time.Now())
	exceptions, _ := s.Exceptions()
	if summary.Total != 0 || summary.LastCheckedAt != 0 || len(exceptions) != 1 {
		t.Fatalf("clear=%+v exceptions=%v", summary, exceptions)
	}
}

func TestPartialCoverageAndFailedBatchStayVisibleAfterRecovery(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	if err := s.refreshFeed(ctx, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	b := batch{lookups: []Lookup{{Name: "risk.example", Count: 3}}}
	if err := s.process(ctx, b); err != nil {
		t.Fatal(err)
	}
	if s.dropped.Load() != 3 {
		t.Fatal("partially checked requests were hidden")
	}
	s.updateFeeds(ctx, true)
	if err := s.process(ctx, b); err != nil {
		t.Fatal(err)
	}
	if s.dropped.Load() != 3 {
		t.Fatal("later complete coverage erased earlier gap")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.process(cancelled, b); err == nil {
		t.Fatal("cancelled batch unexpectedly committed")
	}
	if s.dropped.Load() != 6 {
		t.Fatal("failed batch was not counted")
	}
}

func TestRefreshRejectsOversizedAndTruncatedSources(t *testing.T) {
	s, _, _ := newTestService(t)
	var list strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&list, "risk-%d.example\n", i)
	}
	body, length, status := list.String(), int64(list.Len()), http.StatusOK
	s.client.Transport = transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, ContentLength: length, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	ctx := context.Background()
	if err := s.refreshFeed(ctx, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"oversized", "truncated", "upstream unavailable"} {
		body, length, status = "risk.example\n", 13, http.StatusOK
		switch scenario {
		case "oversized":
			length = maxFeedBytes + 1
		case "upstream unavailable":
			status = 503
		}
		if err := s.refreshFeed(ctx, 0, time.Now()); err == nil {
			t.Fatalf("accepted %s response", scenario)
		}
		if s.feeds[0].status.Entries != 200 {
			t.Fatal("failed refresh replaced active list")
		}
	}
}

func TestRetentionPaginationAndHardBound(t *testing.T) {
	s, _, _ := newTestService(t)
	now := time.Now().UTC()
	day := now.Truncate(24 * time.Hour).Unix()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxAlerts+3; i++ {
		_, err = tx.Exec("INSERT INTO alerts(day,domain,category,severity,first_seen,last_seen,lookups,last_address) VALUES(?,?,'adult','warning',?,?,1,'192.0.2.1')", day, fmt.Sprintf("domain-%d.example", i), now.Unix(), now.Unix()+int64(i%100))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := trimAlerts(tx, now, 30); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	summary, _ := s.Summary(now)
	if summary.Total != maxAlerts || summary.RemovedAlerts != 3 {
		t.Fatalf("bounds=%+v", summary)
	}
	first, _ := s.Alerts("all", "adult", 0, 2)
	next, _ := s.Alerts("all", "adult", 2, 2)
	if first.Total != maxAlerts || len(next.Alerts) != 2 || first.Alerts[1].ID == next.Alerts[0].ID {
		t.Fatal("pagination is not stable")
	}
	if err := s.prune(now.AddDate(0, 0, 31), 30); err != nil {
		t.Fatal(err)
	}
	page, _ := s.Alerts("all", "", 0, 50)
	if page.Total != 0 {
		t.Fatal("old alerts survived retention")
	}
}
