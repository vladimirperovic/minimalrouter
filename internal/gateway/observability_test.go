package gateway

import (
	"context"
	"testing"
	"time"
)

func TestSlowIntervalCollectsFreshOutageEvidence(t *testing.T) {
	down := Summary{Available: true, Enabled: true, Link: LinkStatus{Connected: false}}
	for _, interval := range []time.Duration{15 * time.Second, 30 * time.Second, 2 * time.Minute, 5 * time.Minute} {
		next := collectionInterval(interval, down, nil)
		if next > 30*time.Second || next > interval {
			t.Fatalf("outage interval %v -> %v", interval, next)
		}
	}
	down.Link.Connected = true
	if collectionInterval(5*time.Minute, down, nil) != 5*time.Minute {
		t.Fatal("healthy link should retain chosen probe interval")
	}
}

func TestProbeNowCooldownAndDisabled(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	monitor := NewMonitor(store, fakeProber{}, &sequenceLinkReader{states: []LinkStatus{{Connected: true}}})
	now := time.Now()
	monitor.now = func() time.Time { return now }
	if _, err = monitor.ProbeNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = monitor.ProbeNow(context.Background()); err == nil {
		t.Fatal("duplicate probe must be rate limited")
	}
	now = now.Add(11 * time.Second)
	if _, err = monitor.ProbeNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultSettings()
	cfg.Enabled = false
	if err = monitor.UpdateSettings(cfg); err != nil {
		t.Fatal(err)
	}
	now = now.Add(11 * time.Second)
	if _, err = monitor.ProbeNow(context.Background()); err == nil {
		t.Fatal("disabled monitoring must not send probes")
	}
}

type cancelledLink struct{ cancel context.CancelFunc }

func (l cancelledLink) Read(context.Context) LinkStatus {
	l.cancel()
	return LinkStatus{Connected: false}
}
func TestCancelledProbeCannotPublishLinkDownEvidence(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	monitor := NewMonitor(store, fakeProber{}, cancelledLink{cancel: cancel})
	if _, err = monitor.ProbeNow(ctx); err == nil {
		t.Fatal("cancelled collection must fail")
	}
	if monitor.Summary().Available {
		t.Fatal("cancelled observation cannot become recovery evidence")
	}
	points, err := monitor.History(time.Hour, 120)
	if err != nil || len(points) != 0 {
		t.Fatalf("cancelled sample was persisted: %+v %v", points, err)
	}
}

func TestOutagesPreserveGapsAndObservedDuration(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	monitor := NewMonitor(store, fakeProber{}, &sequenceLinkReader{})
	now := time.Now().UTC().Truncate(time.Second)
	monitor.now = func() time.Time { return now }
	for _, entry := range []struct {
		age int
		up  bool
	}{{600, true}, {570, false}, {540, false}, {510, true}, {300, false}, {270, false}, {30, false}} {
		state := StateOffline
		if entry.up {
			state = StateHealthy
		}
		s := Sample{Timestamp: now.Add(-time.Duration(entry.age) * time.Second), State: state, Link: LinkStatus{Connected: entry.up}}
		if err = store.SaveSample(s); err != nil {
			t.Fatal(err)
		}
	}
	monitor.latest = Summary{Enabled: true, Available: true, Link: LinkStatus{Connected: false}}
	events, err := monitor.Outages()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("want 3 episodes, got %+v", events)
	}
	if events[0].Outcome != "ongoing" || !events[0].Partial || events[0].DurationSeconds != 0 {
		t.Fatalf("gap must start a new partial episode: %+v", events[0])
	}
	if events[1].Outcome != "unknown" || events[1].DurationSeconds != 30 {
		t.Fatalf("missing collection must not be counted: %+v", events[1])
	}
	if events[2].Outcome != "recovered" || events[2].DurationSeconds != 60 || events[2].EndedAt == nil {
		t.Fatalf("restored episode: %+v", events[2])
	}
	for _, event := range events {
		if event.Cause != OutageCausePPPoELink {
			t.Fatalf("link-down episodes must say so: %+v", event)
		}
	}
}

// Live v0.3.0 finding: the ISP blackholed traffic for four minutes while the
// PPPoE session stayed up. Gateway Quality showed "offline", but the outage
// history stayed empty because only link-down samples counted.
func TestOutagesRecordInternetLossWhileLinkStaysUp(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	monitor := NewMonitor(store, fakeProber{}, &sequenceLinkReader{})
	now := time.Now().UTC().Truncate(time.Second)
	monitor.now = func() time.Time { return now }
	for _, entry := range []struct {
		age   int
		state HealthState
		up    bool
	}{{300, StateHealthy, true}, {270, StateDegraded, true}, {240, StateOffline, true}, {210, StateOffline, true}, {180, StateOffline, true}, {150, StateHealthy, true}, {120, StateOffline, true}, {90, StateOffline, false}, {60, StateHealthy, true}} {
		s := Sample{Timestamp: now.Add(-time.Duration(entry.age) * time.Second), State: entry.state, Link: LinkStatus{Connected: entry.up}}
		if err = store.SaveSample(s); err != nil {
			t.Fatal(err)
		}
	}
	monitor.latest = Summary{Enabled: true, Available: true, State: StateHealthy, Link: LinkStatus{Connected: true}}
	events, err := monitor.Outages()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("want the internet-only and the mixed episode, got %+v", events)
	}
	internet, mixed := events[1], events[0]
	if internet.Cause != OutageCauseInternet || internet.Outcome != "recovered" || internet.DurationSeconds != 90 {
		t.Fatalf("link-up blackout must be recorded as an Internet outage: %+v", internet)
	}
	if mixed.Cause != OutageCausePPPoELink || mixed.DurationSeconds != 60 {
		t.Fatalf("an episode with a link-down sample is a link outage: %+v", mixed)
	}
}

func TestOutagesStillOngoingWhileInternetIsUnreachable(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	monitor := NewMonitor(store, fakeProber{}, &sequenceLinkReader{})
	now := time.Now().UTC().Truncate(time.Second)
	monitor.now = func() time.Time { return now }
	for _, age := range []int{90, 60, 30} {
		if err = store.SaveSample(Sample{Timestamp: now.Add(-time.Duration(age) * time.Second), State: StateOffline, Link: LinkStatus{Connected: true}}); err != nil {
			t.Fatal(err)
		}
	}
	monitor.latest = Summary{Enabled: true, Available: true, State: StateOffline, Link: LinkStatus{Connected: true}}
	events, err := monitor.Outages()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Outcome != "ongoing" || events[0].Cause != OutageCauseInternet {
		t.Fatalf("an unreachable Internet with the link up is an ongoing outage: %+v", events)
	}
}

// After NTP steps a wrong RTC back, rows stamped in the "future" must not be
// reported as the newest outage evidence.
func TestOutagesIgnoreSamplesFromTheFuture(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	monitor := NewMonitor(store, fakeProber{}, &sequenceLinkReader{})
	now := time.Now().UTC().Truncate(time.Second)
	monitor.now = func() time.Time { return now }
	for _, offset := range []time.Duration{2 * time.Hour, 2*time.Hour + 30*time.Second} {
		if err = store.SaveSample(Sample{Timestamp: now.Add(offset), State: StateOffline, Link: LinkStatus{Connected: false}}); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.SaveSample(Sample{Timestamp: now.Add(-30 * time.Second), State: StateHealthy, Link: LinkStatus{Connected: true}}); err != nil {
		t.Fatal(err)
	}
	monitor.latest = Summary{Enabled: true, Available: true, State: StateHealthy, Link: LinkStatus{Connected: true}}
	events, err := monitor.Outages()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("future-dated samples leaked into outage history: %+v", events)
	}
}
