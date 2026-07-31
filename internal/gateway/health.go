package gateway

const (
	degradedLatencyMS = 150.0
	degradedJitterMS  = 50.0
	degradedLossPct   = 5.0
	flappingPerHour   = 3
)

// classify keeps the first release deliberately conservative: a single target
// failure degrades the link, while all targets failing or PPPoE being down marks
// it offline. Repeated reconnects override the current quality as flapping.
func classify(link LinkStatus, targets []TargetResult, reconnects1H int) HealthState {
	if !link.Connected {
		return StateOffline
	}
	if reconnects1H >= flappingPerHour {
		return StateFlapping
	}
	if len(targets) == 0 {
		return StateUnknown
	}

	reachable := 0
	degraded := false
	for _, result := range targets {
		if result.Reachable {
			reachable++
		} else {
			degraded = true
		}
		if result.PacketLossPercent >= degradedLossPct ||
			result.LatencyMS >= degradedLatencyMS ||
			result.JitterMS >= degradedJitterMS {
			degraded = true
		}
	}
	if reachable == 0 {
		return StateOffline
	}
	if degraded {
		return StateDegraded
	}
	return StateHealthy
}

func aggregateTargets(targets []TargetResult) (latency, jitter, loss float64) {
	if len(targets) == 0 {
		return 0, 0, 100
	}
	var latencyCount, jitterCount int
	for _, result := range targets {
		loss += result.PacketLossPercent
		if result.Reachable {
			latency += result.LatencyMS
			latencyCount++
			jitter += result.JitterMS
			jitterCount++
		}
	}
	loss /= float64(len(targets))
	if latencyCount > 0 {
		latency /= float64(latencyCount)
	}
	if jitterCount > 0 {
		jitter /= float64(jitterCount)
	}
	return latency, jitter, loss
}
