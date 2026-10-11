package gateway

import "time"

// Outage describes observed link-down samples, not an inferred Internet SLA.
// Unknown ends mark missing evidence, including retention and collection gaps.
type Outage struct {
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	LastObservedAt  time.Time  `json:"last_observed_at"`
	DurationSeconds int64      `json:"duration_seconds"`
	Outcome         string     `json:"outcome"`
	Partial         bool       `json:"partial"`
}

func (m *Monitor) Outages() ([]Outage, error) {
	now := m.now().UTC()
	rows, err := m.store.db.Query(`SELECT timestamp, link_connected FROM gateway_samples WHERE timestamp >= ? ORDER BY timestamp, id`, now.Add(-retentionPeriod).Unix())
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
		var up bool
		if err := rows.Scan(&seconds, &up); err != nil {
			return nil, err
		}
		at := time.Unix(seconds, 0).UTC()
		gap := !previous.IsZero() && at.Sub(previous) > 2*time.Minute
		if gap {
			finish("unknown", nil)
		}
		if up {
			finish("recovered", &at)
		} else {
			if current == nil {
				current = &Outage{StartedAt: at, Partial: previous.IsZero() || gap}
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
	if now.Sub(previous) > 2*time.Minute || !summary.Enabled || !summary.Available || summary.Link.Connected {
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
