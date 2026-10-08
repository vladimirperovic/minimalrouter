package dnsfilter

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeHelper struct {
	mu      sync.Mutex
	state   Applied
	applies int
	reject  bool
}

func (f *fakeHelper) Call(_ context.Context, r Request, emit func(io.Writer) error) (Applied, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Operation == "apply" {
		if f.reject {
			return f.state, errors.New("activation failed; previous policy retained")
		}
		if r.ExpectedRevision != f.state.Policy.Revision {
			return f.state, errors.New("revision conflict")
		}
		var data strings.Builder
		if err := emit(&data); err != nil {
			return f.state, err
		}
		f.state = Applied{Policy: r.Policy, Sources: r.Sources, AppliedAt: time.Now().Unix(), Healthy: true, Domains: strings.Count(data.String(), "\n")}
		f.state.Policy.Revision++
		f.applies++
	}
	return f.state, nil
}

func awaitFilter(t *testing.T, s *Service) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := s.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !status.Updating {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("DNS filter worker did not finish")
	return Status{}
}

func TestPolicyRejectsInjectionAndPublicSuffixes(t *testing.T) {
	for _, domain := range []string{"com", "co.uk", "github.io", "127.0.0.1", "example.com/1.2.3.4", "example.com\nserver=/#/1.2.3.4", "*.example.com", "router.lan", "x.local", "bad..example.com", "https://example.com"} {
		if _, ok := Domain(domain); ok {
			t.Errorf("accepted %q", domain)
		}
	}
	for _, domain := range []string{"EXAMPLE.COM.", "cdn.example.com", "tenant.github.io"} {
		if _, ok := Domain(domain); !ok {
			t.Errorf("rejected %q", domain)
		}
	}
	p := DefaultPolicy()
	p.Categories["unknown"] = true
	if p.Validate() == nil {
		t.Fatal("unknown source accepted")
	}
	p = DefaultPolicy()
	p.Exceptions = []Exception{{Domain: "school.example.com"}}
	if !p.Allowed("media.school.example.com") || p.Allowed("otherschool.example.com") {
		t.Fatal("exception label boundary lost")
	}
}

func TestCatalogRejectsInvalidAndMatchesParents(t *testing.T) {
	source := Sources()[0]
	path := filepath.Join(t.TempDir(), "catalog.db")
	for _, body := range []string{"# empty\n", "<html>broken</html>\n", "example.com\nbad/domain\n"} {
		if _, err := buildCatalog(context.Background(), path, source, strings.NewReader(body), nil); err == nil {
			t.Fatal("invalid catalog accepted")
		}
	}
	meta, err := buildCatalog(context.Background(), path, source, strings.NewReader("# source\nEXAMPLE.COM\nexample.com\ntenant.github.io\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Entries != 2 {
		t.Fatalf("duplicate count %d", meta.Entries)
	}
	if match, err := catalogMatch(context.Background(), path, "cdn.example.com"); err != nil || match != "example.com" {
		t.Fatalf("parent match %s %v", match, err)
	}
	if match, err := catalogMatch(context.Background(), path, "other.github.io"); err != nil || match != "" {
		t.Fatalf("shared-host mismatch %s %v", match, err)
	}
}

func TestFilterOptInRefreshFailureAndRevisionConflict(t *testing.T) {
	helper := &fakeHelper{state: Applied{Policy: DefaultPolicy(), Sources: map[string]string{}}}
	s, err := Open(t.TempDir(), helper, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.download = func(ctx context.Context, path string, source Source, _ Catalog, allow func() bool) (Catalog, error) {
		return buildCatalog(ctx, path, source, strings.NewReader("bad.example.com\ntracking.example.org\n"), allow)
	}
	initial := awaitFilter(t, s)
	if initial.Domains != 0 || len(initial.Policy.Categories) != 0 {
		t.Fatal("blocking activated without opt-in")
	}
	p := DefaultPolicy()
	p.Categories["adult"] = true
	if err = s.Start(p, false); err != nil {
		t.Fatal(err)
	}
	active := awaitFilter(t, s)
	if !active.Healthy || active.Domains != 2 || active.Policy.Revision != 1 {
		t.Fatalf("not activated: %+v", active)
	}
	check, err := s.Check(context.Background(), "cdn.bad.example.com")
	if err != nil || check.Action != "Block" || check.Matches[0].Domain != "bad.example.com" {
		t.Fatalf("check: %+v %v", check, err)
	}
	// A stale editor cannot replace the accepted policy.
	if err = s.Start(p, false); err != nil {
		t.Fatal(err)
	}
	stale := awaitFilter(t, s)
	if stale.Error == "" || stale.Policy.Revision != 1 {
		t.Fatal("stale edit accepted")
	}
	s.download = func(context.Context, string, Source, Catalog, func() bool) (Catalog, error) {
		return Catalog{}, errors.New("offline")
	}
	s.mu.Lock()
	s.lastAttempt = time.Time{}
	s.mu.Unlock()
	if err = s.Start(active.Policy, true); err != nil {
		t.Fatal(err)
	}
	failed := awaitFilter(t, s)
	if failed.Error == "" || failed.Sources["adult"] != active.Sources["adult"] || failed.Policy.Revision != 1 {
		t.Fatal("failed refresh replaced working policy")
	}
	helper.mu.Lock()
	if helper.applies != 1 {
		t.Errorf("unexpected applies %d", helper.applies)
	}
	helper.mu.Unlock()
	// Turning a category off works without any network download.
	off := DefaultPolicy()
	off.Revision = 1
	if err = s.Start(off, false); err != nil {
		t.Fatal(err)
	}
	stopped := awaitFilter(t, s)
	if stopped.Domains != 0 || stopped.Policy.Revision != 2 {
		t.Fatal("offline disable failed")
	}
}

func TestDecodeMetadataRejectsTrailingUnknownAndOversized(t *testing.T) {
	for _, raw := range []string{`{"version":1,"command":"sh"}`, `{"version":1} {}`, strings.Repeat(" ", MaxMetadataBytes+1)} {
		var r Request
		if DecodeMetadata([]byte(raw), &r) == nil {
			t.Fatal("invalid metadata accepted")
		}
	}
}
