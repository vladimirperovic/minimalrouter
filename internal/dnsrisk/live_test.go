package dnsrisk

import (
	"context"
	"os"
	"testing"
	"time"
)

// Opt-in source compatibility check. Normal tests are fully offline.
func TestLiveCategorySources(t *testing.T) {
	if os.Getenv("MINIMALROUTER_LIVE_FEED_TEST") != "1" {
		t.Skip("set MINIMALROUTER_LIVE_FEED_TEST=1 to download the public lists")
	}
	s, err := Open(t.TempDir(), func() (bool, int, error) { return true, 30, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	for i, src := range sources {
		start := time.Now()
		if err := s.refreshFeed(ctx, i, start); err != nil {
			t.Errorf("%s: %v", src.ID, err)
			continue
		}
		info, err := os.Stat(feedPath(s.dir, src.ID, s.feeds[i].slot))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d entries, %.1f MiB disk, %s", src.ID, s.feeds[i].status.Entries, float64(info.Size())/(1<<20), time.Since(start).Round(time.Millisecond))
	}
	lookups := make([]Lookup, 1000)
	for i := range lookups {
		lookups[i] = Lookup{Name: "www.example.com", Count: 1}
	}
	start := time.Now()
	if err := s.process(ctx, batch{lookups: lookups}); err != nil {
		t.Fatal(err)
	}
	t.Logf("1000 repeated lookups: %s", time.Since(start))
}
