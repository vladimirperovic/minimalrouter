package accounting

import (
	"testing"
	"time"
)

func TestRecentHistoryBaselineGapUTCAndClear(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	start := time.Date(2026, 9, 7, 23, 50, 0, 0, time.UTC)
	record := func(at time.Time, rx, tx uint64) {
		t.Helper()
		if err := s.Record(at, []Counter{{Address: "192.0.2.1", Bytes: rx}}, []Counter{{Address: "192.0.2.1", Bytes: tx}}, 13, 1); err != nil {
			t.Fatal(err)
		}
	}
	read := func(at time.Time, period string) Insights {
		t.Helper()
		v, err := s.Insights(at, period)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	record(start, 10000, 1000)
	if v := read(start, "today"); v.TotalBytes != 0 || v.PeakSampleMbps != nil {
		t.Fatalf("pre-upgrade bytes leaked: %+v", v)
	}
	record(start.Add(5*time.Minute), 10600, 1100)
	record(start.Add(10*time.Minute), 11000, 1300)
	v := read(start.Add(11*time.Minute), "today")
	if v.TotalBytes != 600 || v.RXBytes != 400 || len(v.Devices) != 1 || v.Devices[0].TotalBytes != 600 {
		t.Fatalf("UTC day mismatch: %+v", v)
	}
	if yesterday := read(start.Add(11*time.Minute), "yesterday"); yesterday.TotalBytes != 700 {
		t.Fatalf("yesterday=%+v", yesterday)
	}
	record(start.Add(2*time.Hour), 99000, 9900)
	if v := read(start.Add(2*time.Hour), "today"); v.TotalBytes != 600 {
		t.Fatalf("gap assigned to a bucket: %+v", v)
	}
	record(start.Add(125*time.Minute), 99000, 9900)
	v = read(start.Add(126*time.Minute), "today")
	if v.Points[1].Samples != 1 || v.Points[1].TotalBytes != 0 {
		t.Fatal("idle sample must be a measured zero")
	}
	if v := read(start.Add(126*time.Minute), "7d"); v.TotalBytes != 1300 {
		t.Fatalf("week mismatch: %+v", v)
	}
	if _, err := s.Insights(start, "invalid"); err == nil {
		t.Fatal("invalid period accepted")
	}
	if err := s.ClearHistory(); err != nil {
		t.Fatal(err)
	}
	if v := read(start.Add(126*time.Minute), "today"); v.HistoryStartedAt != nil || v.TotalBytes != 0 || len(v.Devices) != 0 {
		t.Fatal("disabled history not deleted")
	}
}
func TestFirewallBaselineRestartGenerationGapAndRetention(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	record := func(at time.Time, g string, seen, allowed uint64) {
		t.Helper()
		if err := s.RecordFirewall(at, g, seen, allowed); err != nil {
			t.Fatal(err)
		}
	}
	record(now, "a", 1000, 900)
	record(now.Add(time.Minute), "a", 1200, 1050)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	record(now.Add(2*time.Minute), "a", 1200, 1050)
	record(now.Add(3*time.Minute), "b", 90000, 80000) // even larger post-reset values are a new baseline
	record(now.Add(4*time.Minute), "b", 90100, 80080)
	record(now.Add(10*time.Minute), "b", 99999, 90000) // interrupted collection
	v, err := s.FirewallActivity(now.Add(10 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !v.Available || v.Allowed != 230 || v.Blocked != 70 {
		t.Fatalf("wrong counters: %+v", v)
	}
	if err := s.ClearHistory(); err != nil {
		t.Fatal(err)
	}
	v, err = s.FirewallActivity(now.Add(14 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if v.Available || v.Allowed != 230 {
		t.Fatal("stale availability or aggregate cleared with device history")
	}
	record(now.Add(26*time.Hour), "b", 100000, 99000)
	v, err = s.FirewallActivity(now.Add(26 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if v.Allowed != 0 || v.Blocked != 0 {
		t.Fatal("expired totals still present")
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM firewall_minute`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("retention not bounded", rows, err)
	}
}
