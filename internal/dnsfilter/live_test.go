package dnsfilter

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in source compatibility check; ordinary tests stay offline.
func TestLiveBlockingSources(t *testing.T) {
	if os.Getenv("MINIMALROUTER_LIVE_FEED_TEST") != "1" {
		t.Skip("set MINIMALROUTER_LIVE_FEED_TEST=1 to download the public lists")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	total := 0
	for _, source := range Sources() {
		path := filepath.Join(t.TempDir(), "catalog.db")
		meta, err := downloadCatalog(ctx, path, source, Catalog{}, nil)
		if err != nil {
			t.Fatalf("%s: %v", source.ID, err)
		}
		if err = emitCatalog(ctx, path, io.Discard); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		total += meta.Entries
		t.Logf("%s: %d entries, %d bytes", source.ID, meta.Entries, info.Size())
	}
	if total > MaxDomains {
		t.Fatalf("combined source size %d exceeds appliance limit %d", total, MaxDomains)
	}
}
