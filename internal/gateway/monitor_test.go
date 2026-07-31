package gateway

import (
	"context"
	"testing"
	"time"
)

type fakeProber struct{}

func (fakeProber) Probe(_ context.Context, target string) TargetResult {
	return TargetResult{Target: target, Reachable: true, PacketsSent: 4, PacketsReceived: 4, LatencyMS: 20, JitterMS: 2}
}

type sequenceLinkReader struct {
	states []LinkStatus
	index  int
}

func (r *sequenceLinkReader) Read(context.Context) LinkStatus {
	if r.index >= len(r.states) {
		return r.states[len(r.states)-1]
	}
	state := r.states[r.index]
	r.index++
	return state
}

func TestMonitorDetectsReconnectLoopWithoutRemediation(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	connected := LinkStatus{Connected: true, Interface: "ppp0", LocalIP: "203.0.113.10", PeerIP: "198.51.100.1"}
	disconnected := LinkStatus{Connected: false, Interface: "ppp0"}
	links := &sequenceLinkReader{states: []LinkStatus{
		connected,
		disconnected, connected,
		disconnected, connected,
		disconnected, connected,
	}}
	if err := store.SaveSettings(Settings{Enabled: true, Targets: []string{"1.1.1.1", "8.8.8.8"}, IntervalSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	monitor := NewMonitor(store, fakeProber{}, links)
	now := time.Date(2026, 7, 31, 18, 0, 0, 0, time.UTC)
	monitor.now = func() time.Time { return now }

	states := make([]HealthState, 0, len(links.states))
	for range links.states {
		summary, err := monitor.Collect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		states = append(states, summary.State)
		now = now.Add(30 * time.Second)
	}
	if states[0] != StateHealthy || states[1] != StateOffline || states[len(states)-1] != StateFlapping {
		t.Fatalf("unexpected state sequence: %v", states)
	}
	final := monitor.Summary()
	if final.Reconnects1H != 3 || final.Reconnects24H != 3 {
		t.Fatalf("unexpected reconnect counters: %+v", final)
	}
	if final.PeerProbe == nil || !final.PeerProbe.Reachable {
		t.Fatalf("PPPoE peer was not observed: %+v", final.PeerProbe)
	}
}

func TestDisabledMonitorDoesNotWriteSamples(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveSettings(Settings{Enabled: false, Targets: []string{"1.1.1.1", "8.8.8.8"}, IntervalSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	monitor := NewMonitor(store, fakeProber{}, &sequenceLinkReader{states: []LinkStatus{{Connected: true}}})
	summary, err := monitor.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Enabled || summary.State != StateUnknown {
		t.Fatalf("unexpected disabled summary: %+v", summary)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM gateway_samples`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("disabled monitor wrote %d samples", count)
	}
}
