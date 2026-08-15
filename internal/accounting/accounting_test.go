package accounting

import (
	"path/filepath"
	"testing"
	"time"
)

func TestParseCountersReadsDynamicSetElements(t *testing.T) {
	raw := `{"nftables":[
		{"metainfo":{"version":"1.1.1"}},
		{"set":{"family":"inet","name":"acct_tx","table":"minimalrouter","type":"ipv4_addr",
		 "elem":[
			{"elem":{"val":"192.168.1.50","timeout":604800,"expires":600000,"counter":{"packets":12,"bytes":4096}}},
			{"elem":{"val":"192.168.1.51","counter":{"packets":1,"bytes":128}}}
		 ]}}
	]}`
	counters, err := ParseCounters(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(counters) != 2 {
		t.Fatalf("expected 2 counters, got %d", len(counters))
	}
	if counters[0].Address != "192.168.1.50" || counters[0].Bytes != 4096 {
		t.Fatalf("unexpected first counter: %+v", counters[0])
	}
	if counters[1].Bytes != 128 {
		t.Fatalf("unexpected second counter: %+v", counters[1])
	}
}

func TestParseCountersIgnoresEmptyAndMalformedInput(t *testing.T) {
	for _, raw := range []string{"", "   ", `{"nftables":[]}`} {
		counters, err := ParseCounters(raw)
		if err != nil {
			t.Fatalf("input %q should not error: %v", raw, err)
		}
		if len(counters) != 0 {
			t.Fatalf("input %q should yield no counters", raw)
		}
	}
	if _, err := ParseCounters("not json"); err == nil {
		t.Fatal("malformed JSON must be reported")
	}
}

// The nftables table is deleted and recreated on every apply, so counters
// restart at zero. A decrease is a reset, and the current value is the delta.
func TestDeltaTreatsCounterResetAsFreshTraffic(t *testing.T) {
	if got := Delta(100, 250); got != 150 {
		t.Fatalf("normal increase: want 150, got %d", got)
	}
	if got := Delta(1000, 40); got != 40 {
		t.Fatalf("counter reset: want 40, got %d", got)
	}
	if got := Delta(500, 500); got != 0 {
		t.Fatalf("idle host: want 0, got %d", got)
	}
}

func TestStoreAccumulatesAcrossRestartWithoutDoubleCounting(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	if err := store.Record(now, []Counter{{Address: "192.168.1.50", Bytes: 1000}}, nil, 13); err != nil {
		t.Fatal(err)
	}
	// Same raw counter observed again: nothing new happened.
	if err := store.Record(now, []Counter{{Address: "192.168.1.50", Bytes: 1000}}, nil, 13); err != nil {
		t.Fatal(err)
	}
	// Counter advanced.
	if err := store.Record(now, []Counter{{Address: "192.168.1.50", Bytes: 1500}}, nil, 13); err != nil {
		t.Fatal(err)
	}
	months, err := store.Months(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(months) != 1 || len(months[0].Devices) != 1 {
		t.Fatalf("expected one month with one device, got %+v", months)
	}
	if months[0].Devices[0].RXBytes != 1500 {
		t.Fatalf("expected 1500 accumulated bytes, got %d", months[0].Devices[0].RXBytes)
	}
	if months[0].Month != "2026-03" {
		t.Fatalf("unexpected month key %q", months[0].Month)
	}
}

func TestStoreSeparatesCalendarMonths(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	march := time.Date(2026, 3, 31, 23, 0, 0, 0, time.UTC)
	april := time.Date(2026, 4, 1, 1, 0, 0, 0, time.UTC)
	if err := store.Record(march, []Counter{{Address: "192.168.1.60", Bytes: 500}}, nil, 13); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(april, []Counter{{Address: "192.168.1.60", Bytes: 900}}, nil, 13); err != nil {
		t.Fatal(err)
	}
	months, err := store.Months(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(months) != 2 {
		t.Fatalf("expected two months, got %d", len(months))
	}
	byMonth := map[string]uint64{}
	for _, month := range months {
		byMonth[month.Month] = month.TotalBytes
	}
	if byMonth["2026-03"] != 500 {
		t.Fatalf("March should hold 500 bytes, got %d", byMonth["2026-03"])
	}
	if byMonth["2026-04"] != 400 {
		t.Fatalf("April should hold only the delta (400), got %d", byMonth["2026-04"])
	}
}

func TestStoreResetClearsHistory(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Record(time.Now(), []Counter{{Address: "192.168.1.70", Bytes: 10}}, nil, 13); err != nil {
		t.Fatal(err)
	}
	if err := store.Reset(); err != nil {
		t.Fatal(err)
	}
	months, err := store.Months(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(months) != 0 {
		t.Fatalf("expected empty history after reset, got %+v", months)
	}
	if _, err := filepath.Abs(dir); err != nil {
		t.Fatal(err)
	}
}
