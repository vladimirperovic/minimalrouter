package gateway

import (
	"testing"
	"time"
)

func TestAggregateHistoryBoundsPointsAndPreservesWorstState(t *testing.T) {
	start := time.Now().UTC().Add(-time.Hour)
	raw := make([]HistoryPoint, 0, 120)
	for i := 0; i < 120; i++ {
		state := StateHealthy
		if i == 61 {
			state = StateOffline
		}
		raw = append(raw, HistoryPoint{
			Timestamp: start.Add(time.Duration(i) * 30 * time.Second),
			State:     state, LatencyMS: float64(i), PacketLossPercent: float64(i % 10),
		})
	}
	points := aggregateHistory(raw, start, 30)
	if len(points) > 30 {
		t.Fatalf("history returned %d points, want <=30", len(points))
	}
	foundOffline := false
	for _, point := range points {
		if point.State == StateOffline {
			foundOffline = true
		}
	}
	if !foundOffline {
		t.Fatal("aggregation discarded the worst state in its bucket")
	}
}

// After NTP steps a wrong RTC back, the chart's newest points must be real
// samples, not rows stamped hours ahead (live v0.3.0 test on Hyper-V).
func TestHistoryAndLatestSampleIgnoreFutureTimestamps(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Second)
	if err = store.SaveSample(Sample{Timestamp: now.Add(2 * time.Hour), State: StateOffline}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveSample(Sample{Timestamp: now.Add(-time.Minute), State: StateHealthy, Link: LinkStatus{Connected: true}}); err != nil {
		t.Fatal(err)
	}
	points, err := store.History(now.Add(-time.Hour), 720)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || points[0].State != StateHealthy {
		t.Fatalf("future-dated sample leaked into history: %+v", points)
	}
	latest, ok, err := store.LatestSample()
	if err != nil || !ok {
		t.Fatalf("latest sample: ok=%v err=%v", ok, err)
	}
	if latest.State != StateHealthy {
		t.Fatalf("latest sample must be the last one written, got %+v", latest)
	}
}
