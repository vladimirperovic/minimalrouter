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
