package gateway

import "testing"

func TestClassifyGatewayHealth(t *testing.T) {
	linkUp := LinkStatus{Connected: true, Interface: "ppp0"}
	healthy := []TargetResult{
		{Target: "1.1.1.1", Reachable: true, PacketsSent: 4, PacketsReceived: 4, LatencyMS: 20, JitterMS: 2},
		{Target: "8.8.8.8", Reachable: true, PacketsSent: 4, PacketsReceived: 4, LatencyMS: 24, JitterMS: 3},
	}
	if got := classify(linkUp, healthy, 0); got != StateHealthy {
		t.Fatalf("healthy probes classified as %s", got)
	}

	degraded := append([]TargetResult(nil), healthy...)
	degraded[1].PacketLossPercent = 25
	degraded[1].PacketsReceived = 3
	if got := classify(linkUp, degraded, 0); got != StateDegraded {
		t.Fatalf("lossy probes classified as %s", got)
	}

	offline := []TargetResult{
		{Target: "1.1.1.1", PacketLossPercent: 100},
		{Target: "8.8.8.8", PacketLossPercent: 100},
	}
	if got := classify(linkUp, offline, 0); got != StateOffline {
		t.Fatalf("unreachable probes classified as %s", got)
	}
	if got := classify(LinkStatus{Connected: false}, healthy, 0); got != StateOffline {
		t.Fatalf("disconnected PPPoE classified as %s", got)
	}
	if got := classify(LinkStatus{Connected: false}, healthy, flappingPerHour); got != StateOffline {
		t.Fatalf("current outage was hidden by flapping state: %s", got)
	}
	if got := classify(linkUp, healthy, flappingPerHour); got != StateFlapping {
		t.Fatalf("repeated reconnects classified as %s", got)
	}
}

func TestAggregateTargets(t *testing.T) {
	latency, jitter, loss := aggregateTargets([]TargetResult{
		{Reachable: true, LatencyMS: 20, JitterMS: 2, PacketLossPercent: 0},
		{Reachable: true, LatencyMS: 40, JitterMS: 6, PacketLossPercent: 10},
	})
	if latency != 30 || jitter != 4 || loss != 5 {
		t.Fatalf("unexpected aggregate latency=%v jitter=%v loss=%v", latency, jitter, loss)
	}
}
