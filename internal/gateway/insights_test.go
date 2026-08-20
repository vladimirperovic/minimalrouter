package gateway

import (
	"math"
	"testing"
	"time"
)

func TestInsightsTrackAvailabilityOutagesAndPublicIPChanges(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureInsightsSchema(); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Minute)
	samples := []Sample{
		{Timestamp: now.Add(-3 * time.Hour), State: StateHealthy, Link: LinkStatus{Connected: true, LocalIP: "203.0.113.10"}},
		{Timestamp: now.Add(-150 * time.Minute), State: StateHealthy, Link: LinkStatus{Connected: true, LocalIP: "203.0.113.10"}},
		{Timestamp: now.Add(-2 * time.Hour), State: StateOffline, Link: LinkStatus{Connected: false}},
		{Timestamp: now.Add(-90 * time.Minute), State: StateHealthy, Link: LinkStatus{Connected: true, LocalIP: "203.0.113.25"}},
		{Timestamp: now.Add(-time.Hour), State: StateHealthy, Link: LinkStatus{Connected: true, LocalIP: "203.0.113.25"}},
	}
	for _, sample := range samples {
		if err := store.SaveSample(sample); err != nil {
			t.Fatal(err)
		}
	}

	insights, err := store.Insights(30*24*time.Hour, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !insights.Available {
		t.Fatal("expected availability insights")
	}
	if insights.Samples != 5 || insights.UpSamples != 4 {
		t.Fatalf("unexpected sample counts: %+v", insights)
	}
	if math.Abs(insights.UptimePercent-80) > 0.001 {
		t.Fatalf("expected 80%% uptime, got %.3f", insights.UptimePercent)
	}
	if insights.Outages != 1 {
		t.Fatalf("expected one link-down transition, got %d", insights.Outages)
	}
	if len(insights.PublicIPChanges) != 1 {
		t.Fatalf("expected one public IP transition, got %+v", insights.PublicIPChanges)
	}
	change := insights.PublicIPChanges[0]
	if change.OldIP != "203.0.113.10" || change.NewIP != "203.0.113.25" {
		t.Fatalf("unexpected public IP transition: %+v", change)
	}
}

func TestInsightsMakePartialCoverageExplicit(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureInsightsSchema(); err != nil {
		t.Fatal(err)
	}

	insights, err := store.Insights(30*24*time.Hour, 20)
	if err != nil {
		t.Fatal(err)
	}
	if insights.Available || insights.Samples != 0 || insights.SampledHours != 0 {
		t.Fatalf("empty store must report collecting/unavailable coverage: %+v", insights)
	}
}
