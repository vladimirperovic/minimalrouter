package gateway

import "time"

// Outage describes observed samples in which the WAN was unusable, not an
// inferred Internet SLA: either the PPPoE link was down, or the link stayed up
// while every probe target was unreachable. The second case is the common
// "modem up, Internet down" failure and was previously missing from history.
// Unknown ends mark missing evidence, including retention and collection gaps.
type Outage struct {
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	LastObservedAt  time.Time  `json:"last_observed_at"`
	DurationSeconds int64      `json:"duration_seconds"`
	Outcome         string     `json:"outcome"`
	Partial         bool       `json:"partial"`
	Cause           string     `json:"cause"`
}

const (
	// OutageCausePPPoELink marks an episode with at least one link-down sample.
	OutageCausePPPoELink = "pppoe_link"
	// OutageCauseInternet marks an episode where the link stayed up but no
	// probe target answered.
	OutageCauseInternet = "internet"
	// futureSampleTolerance ignores samples stamped ahead of the current clock.
	// A router that booted with a wrong RTC (Hyper-V hands guests local time
	// as UTC) keeps such rows after NTP steps the clock back, and they would
	// otherwise sort as the newest evidence for hours.
	futureSampleTolerance = time.Minute
)

// notAfter bounds every read of stored evidence to the current clock, see
// futureSampleTolerance.
func notAfter() int64 { return time.Now().UTC().Add(futureSampleTolerance).Unix() }

func (m *Monitor) Outages() ([]Outage, error) {
	now := m.now().UTC()
	rows, err := m.store.db.Query(`SELECT timestamp, link_connected, state FROM gateway_samples WHERE timestamp >= ? AND timestamp <= ? ORDER BY timestamp, id`,
		now.Add(-retentionPeriod).Unix(), now.Add(futureSampleTolerance).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []Outage{}
	var current *Outage
	var previous time.Time
	finish := func(outcome string, end *time.Time) {
		if current == nil {
			return
		}
		current.Outcome, current.EndedAt = outcome, end
		until := current.LastObservedAt
		if end != nil {
			until = *end
		}
		current.DurationSeconds = int64(until.Sub(current.StartedAt).Seconds())
		events = append(events, *current)
		current = nil
	}
	for rows.Next() {
		var seconds int64
		var linkUp bool
		var sampleState string
		if err := rows.Scan(&seconds, &linkUp, &sampleState); err != nil {
			return nil, err
		}
		at := time.Unix(seconds, 0).UTC()
		gap := !previous.IsZero() && at.Sub(previous) > 2*time.Minute
		if gap {
			finish("unknown", nil)
		}
		if linkUp && HealthState(sampleState) != StateOffline {
			finish("recovered", &at)
		} else {
			if current == nil {
				current = &Outage{StartedAt: at, Partial: previous.IsZero() || gap, Cause: OutageCauseInternet}
			}
			if !linkUp {
				current.Cause = OutageCausePPPoELink
			}
			current.LastObservedAt = at
		}
		previous = at
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	state := "ongoing"
	summary := m.Summary()
	stillDown := !summary.Link.Connected || summary.State == StateOffline
	if now.Sub(previous) > 2*time.Minute || !summary.Enabled || !summary.Available || !stillDown {
		state = "unknown"
	}
	finish(state, nil)
	// Most recent first, at most 20 episodes from the bounded raw store.
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	if len(events) > 20 {
		events = events[:20]
	}
	return events, nil
}
