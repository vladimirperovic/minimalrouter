package gateway

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Monitor struct {
	store  *Store
	prober Prober
	link   LinkReader
	now    func() time.Time
	wake   chan struct{}

	mu             sync.RWMutex
	latest         Summary
	lastConnected  bool
	initialized    bool
	connectedSince time.Time
}

func NewMonitor(store *Store, prober Prober, link LinkReader) *Monitor {
	return &Monitor{
		store: store, prober: prober, link: link,
		now: time.Now, wake: make(chan struct{}, 1),
		latest: Summary{Available: true, State: StateUnknown},
	}
}

func (m *Monitor) Run(ctx context.Context) {
	if m == nil || m.store == nil {
		return
	}
	for {
		settings, err := m.store.Settings()
		interval := 30 * time.Second
		if err == nil {
			interval = settings.Interval()
		}
		if _, collectErr := m.Collect(ctx); collectErr != nil {
			m.mu.Lock()
			m.latest.Available = false
			m.mu.Unlock()
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-m.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (m *Monitor) Collect(ctx context.Context) (Summary, error) {
	if m == nil || m.store == nil || m.prober == nil || m.link == nil {
		return Summary{}, fmt.Errorf("gateway monitor is not configured")
	}
	settings, err := m.store.Settings()
	if err != nil {
		return Summary{}, err
	}
	now := m.now().UTC()
	if !settings.Enabled {
		summary := Summary{Available: true, Enabled: false, State: StateUnknown, Timestamp: now}
		m.mu.Lock()
		m.latest = summary
		m.initialized = false
		m.lastConnected = false
		m.connectedSince = time.Time{}
		m.mu.Unlock()
		return summary, nil
	}

	linkCtx, cancelLink := context.WithTimeout(ctx, 2*time.Second)
	linkStatus := m.link.Read(linkCtx)
	cancelLink()

	m.mu.Lock()
	if !m.initialized {
		previous, ok, previousErr := m.store.LatestSample()
		recentLimit := settings.Interval() * 3
		if recentLimit < 2*time.Minute {
			recentLimit = 2 * time.Minute
		}
		if previousErr == nil && ok && previous.Link.Connected && linkStatus.Connected && now.Sub(previous.Timestamp) <= recentLimit {
			m.connectedSince = previous.Timestamp.Add(-time.Duration(previous.PPPoEUptime) * time.Second)
		} else if linkStatus.Connected {
			m.connectedSince = now
			if previousErr == nil && ok && !previous.Link.Connected && now.Sub(previous.Timestamp) <= recentLimit {
				if err := m.store.AddReconnect(now); err != nil {
					m.mu.Unlock()
					return Summary{}, err
				}
			}
		}
		m.lastConnected = linkStatus.Connected
		m.initialized = true
	} else if linkStatus.Connected && !m.lastConnected {
		m.connectedSince = now
		if err := m.store.AddReconnect(now); err != nil {
			m.mu.Unlock()
			return Summary{}, err
		}
	} else if !linkStatus.Connected {
		m.connectedSince = time.Time{}
	}
	m.lastConnected = linkStatus.Connected
	connectedSince := m.connectedSince
	m.mu.Unlock()

	targetResults := make([]TargetResult, len(settings.Targets))
	for index, target := range settings.Targets {
		targetResults[index] = TargetResult{Target: target, PacketLossPercent: 100}
	}
	var peerProbe *TargetResult
	if linkStatus.Connected {
		var wg sync.WaitGroup
		for index, target := range settings.Targets {
			wg.Add(1)
			go func(index int, target string) {
				defer wg.Done()
				probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
				defer cancel()
				targetResults[index] = m.prober.Probe(probeCtx, target)
			}(index, target)
		}
		wg.Wait()
		if linkStatus.PeerIP != "" {
			peerCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
			result := m.prober.Probe(peerCtx, linkStatus.PeerIP)
			cancel()
			peerProbe = &result
		}
	}

	reconnects1H, err := m.store.CountReconnects(now.Add(-time.Hour))
	if err != nil {
		return Summary{}, err
	}
	reconnects24H, err := m.store.CountReconnects(now.Add(-24 * time.Hour))
	if err != nil {
		return Summary{}, err
	}
	latency, jitter, loss := aggregateTargets(targetResults)
	uptime := int64(0)
	if linkStatus.Connected && !connectedSince.IsZero() {
		uptime = int64(now.Sub(connectedSince).Seconds())
		if uptime < 0 {
			uptime = 0
		}
	}
	state := classify(linkStatus, targetResults, reconnects1H)
	sample := Sample{
		Timestamp: now, State: state, Link: linkStatus, Targets: targetResults, PeerProbe: peerProbe,
		LatencyMS: latency, JitterMS: jitter, PacketLossPercent: loss, PPPoEUptime: uptime,
	}
	if err := m.store.SaveSample(sample); err != nil {
		return Summary{}, err
	}
	summary := Summary{
		Available: true, Enabled: true, State: state, Timestamp: now, Link: linkStatus,
		Targets: targetResults, PeerProbe: peerProbe, LatencyMS: latency, JitterMS: jitter,
		PacketLossPercent: loss, PPPoEUptime: uptime,
		Reconnects1H: reconnects1H, Reconnects24H: reconnects24H,
	}
	m.mu.Lock()
	m.latest = summary
	m.mu.Unlock()
	return summary, nil
}

func (m *Monitor) Settings() (Settings, error) {
	if m == nil || m.store == nil {
		return Settings{}, fmt.Errorf("gateway monitor is unavailable")
	}
	return m.store.Settings()
}

func (m *Monitor) UpdateSettings(settings Settings) error {
	if m == nil || m.store == nil {
		return fmt.Errorf("gateway monitor is unavailable")
	}
	if err := m.store.SaveSettings(settings); err != nil {
		return err
	}
	if !settings.Enabled {
		m.mu.Lock()
		m.latest = Summary{Available: true, Enabled: false, State: StateUnknown, Timestamp: m.now().UTC()}
		m.initialized = false
		m.lastConnected = false
		m.connectedSince = time.Time{}
		m.mu.Unlock()
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return nil
}

func (m *Monitor) Summary() Summary {
	if m == nil {
		return Summary{Available: false, State: StateUnknown}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := m.latest
	result.Targets = append([]TargetResult(nil), result.Targets...)
	if result.PeerProbe != nil {
		peer := *result.PeerProbe
		result.PeerProbe = &peer
	}
	return result
}

func (m *Monitor) History(window time.Duration, maxPoints int) ([]HistoryPoint, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("gateway monitor is unavailable")
	}
	return m.store.History(m.now().UTC().Add(-window), maxPoints)
}
