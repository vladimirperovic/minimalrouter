package gateway

import (
	"testing"
	"time"
)

func TestStorePersistsBoundedSamplesAndReconnects(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	settings, err := store.Settings()
	if err != nil || settings.IntervalSeconds != 30 || len(settings.Targets) != 2 {
		t.Fatalf("default settings unavailable: %+v err=%v", settings, err)
	}
	wantedSettings := Settings{Enabled: false, Targets: []string{"9.9.9.9", "1.0.0.1"}, IntervalSeconds: 60}
	if err := store.SaveSettings(wantedSettings); err != nil {
		t.Fatal(err)
	}
	settings, err = store.Settings()
	if err != nil || settings.Enabled || settings.IntervalSeconds != 60 || settings.Targets[0] != "9.9.9.9" {
		t.Fatalf("saved settings mismatch: %+v err=%v", settings, err)
	}

	now := time.Date(2026, 7, 31, 18, 0, 0, 0, time.UTC)
	peer := TargetResult{Target: "198.51.100.1", Reachable: true, PacketsSent: 4, PacketsReceived: 4, LatencyMS: 2}
	sample := Sample{
		Timestamp: now,
		State:     StateHealthy,
		Link:      LinkStatus{Connected: true, Interface: "ppp0", LocalIP: "203.0.113.10", PeerIP: "198.51.100.1"},
		Targets: []TargetResult{
			{Target: "1.1.1.1", Reachable: true, PacketsSent: 4, PacketsReceived: 4, LatencyMS: 20},
			{Target: "8.8.8.8", Reachable: true, PacketsSent: 4, PacketsReceived: 4, LatencyMS: 24},
		},
		PeerProbe:   &peer,
		LatencyMS:   22,
		JitterMS:    2,
		PPPoEUptime: 3600,
	}
	if err := store.SaveSample(sample); err != nil {
		t.Fatal(err)
	}
	latest, ok, err := store.LatestSample()
	if err != nil || !ok {
		t.Fatalf("latest sample unavailable: ok=%v err=%v", ok, err)
	}
	if latest.State != StateHealthy || latest.Link.PeerIP != sample.Link.PeerIP || latest.PeerProbe == nil || latest.PeerProbe.Target != peer.Target {
		t.Fatalf("unexpected persisted sample: %+v", latest)
	}

	if err := store.AddReconnect(now.Add(-2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.AddReconnect(now.Add(-30 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	count, err := store.CountReconnects(now.Add(-time.Hour))
	if err != nil || count != 1 {
		t.Fatalf("reconnect count=%d err=%v, want 1", count, err)
	}

	old := sample
	old.Timestamp = now.Add(-retentionPeriod - time.Minute)
	if err := store.SaveSample(old); err != nil {
		t.Fatal(err)
	}
	newer := sample
	newer.Timestamp = now.Add(time.Minute)
	if err := store.SaveSample(newer); err != nil {
		t.Fatal(err)
	}
	var oldCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM gateway_samples WHERE timestamp < ?`, now.Add(-retentionPeriod).Unix()).Scan(&oldCount); err != nil {
		t.Fatal(err)
	}
	if oldCount != 0 {
		t.Fatalf("retention left %d expired samples", oldCount)
	}
}

func TestHistoryAggregationAndSevenDayCapacity(t *testing.T) {
	if maxSampleRows < int(retentionPeriod/(15*time.Second)) {
		t.Fatalf("maxSampleRows=%d cannot retain seven days at 15-second sampling", maxSampleRows)
	}
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	start := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 120; i++ {
		state := StateHealthy
		if i == 61 {
			state = StateOffline
		}
		if err := store.SaveSample(Sample{
			Timestamp: start.Add(time.Duration(i) * 30 * time.Second),
			State:     state,
			Link:      LinkStatus{Connected: state != StateOffline, Interface: "ppp0"},
			Targets:   []TargetResult{{Target: "1.1.1.1"}, {Target: "8.8.8.8"}},
			LatencyMS: float64(i), PacketLossPercent: float64(i % 10),
		}); err != nil {
			t.Fatal(err)
		}
	}
	points, err := store.History(start, 30)
	if err != nil {
		t.Fatal(err)
	}
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
		t.Fatal("aggregated history discarded offline state")
	}
}
