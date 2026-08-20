package gateway

import (
	"fmt"
	"time"
)

const (
	insightRetention = 400 * 24 * time.Hour
	maxIPChangeRows  = 1024
)

// PublicIPChange records only WAN address transitions. It deliberately stores
// no remote destinations or traffic metadata.
type PublicIPChange struct {
	Timestamp time.Time `json:"timestamp"`
	OldIP     string    `json:"old_ip"`
	NewIP     string    `json:"new_ip"`
}

// Insights summarizes sampled PPPoE link availability. UptimePercent is based
// on actual monitor samples, while SampledHours makes partial post-upgrade
// coverage explicit instead of pretending a full 30-day history exists.
type Insights struct {
	WindowDays      int              `json:"window_days"`
	Available       bool             `json:"available"`
	SampledHours    int              `json:"sampled_hours"`
	Samples         int64            `json:"samples"`
	UpSamples       int64            `json:"up_samples"`
	UptimePercent   float64          `json:"uptime_percent"`
	Outages         int64            `json:"outages"`
	FirstSample     *time.Time       `json:"first_sample,omitempty"`
	LastSample      *time.Time       `json:"last_sample,omitempty"`
	PublicIPChanges []PublicIPChange `json:"public_ip_changes"`
}

// EnsureInsightsSchema installs bounded derived-history tables and triggers.
// It must run before Monitor.Run so every future raw sample is folded into the
// long-lived availability counters and public-IP transition log. Existing raw
// history is backfilled once, giving an upgrade as much honest coverage as the
// seven-day raw store still contains.
func (s *Store) EnsureInsightsSchema() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("gateway store is unavailable")
	}
	if _, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS gateway_availability_hourly (
		hour_start INTEGER PRIMARY KEY,
		samples INTEGER NOT NULL DEFAULT 0,
		up_samples INTEGER NOT NULL DEFAULT 0,
		outage_starts INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS gateway_public_ip_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp INTEGER NOT NULL,
		old_ip TEXT NOT NULL,
		new_ip TEXT NOT NULL,
		UNIQUE(timestamp, old_ip, new_ip)
	);
	CREATE INDEX IF NOT EXISTS idx_gateway_public_ip_events_timestamp
		ON gateway_public_ip_events(timestamp);

	-- Recreate the trigger on startup so a corrected migration is actually
	-- installed on an appliance that has already seen an older development
	-- build of this schema.
	DROP TRIGGER IF EXISTS gateway_insights_after_sample;
	CREATE TRIGGER gateway_insights_after_sample
	AFTER INSERT ON gateway_samples
	BEGIN
		INSERT INTO gateway_availability_hourly(hour_start, samples, up_samples, outage_starts)
		VALUES (
			(NEW.timestamp / 3600) * 3600,
			1,
			CASE WHEN NEW.link_connected != 0 THEN 1 ELSE 0 END,
			CASE WHEN NEW.link_connected = 0 AND COALESCE(
				(SELECT link_connected FROM gateway_samples WHERE id < NEW.id ORDER BY id DESC LIMIT 1), 1
			) != 0 THEN 1 ELSE 0 END
		)
		ON CONFLICT(hour_start) DO UPDATE SET
			samples = samples + 1,
			up_samples = up_samples + excluded.up_samples,
			outage_starts = outage_starts + excluded.outage_starts;

		-- Compare NEW only with the immediately preceding non-empty WAN address.
		-- Applying the inequality after selecting that row is important: filtering
		-- previous rows by != NEW.local_ip first would walk past the most recent
		-- equal address and record the same A -> B transition on every B sample.
		INSERT OR IGNORE INTO gateway_public_ip_events(timestamp, old_ip, new_ip)
		SELECT NEW.timestamp, previous.prev_ip, NEW.local_ip
		FROM (
			SELECT local_ip AS prev_ip
			FROM gateway_samples
			WHERE id < NEW.id AND local_ip != ''
			ORDER BY id DESC
			LIMIT 1
		) AS previous
		WHERE NEW.local_ip != '' AND previous.prev_ip != NEW.local_ip;

		DELETE FROM gateway_availability_hourly
		WHERE hour_start < NEW.timestamp - 34560000;
		DELETE FROM gateway_public_ip_events
		WHERE timestamp < NEW.timestamp - 34560000;
		DELETE FROM gateway_public_ip_events
		WHERE id NOT IN (
			SELECT id FROM gateway_public_ip_events ORDER BY id DESC LIMIT 1024
		);
	END;
	`); err != nil {
		return fmt.Errorf("migrate gateway insights: %w", err)
	}

	// Backfill only hours that predate the trigger-derived store. This runs
	// before collection starts, so it cannot race a new sample into the same
	// hour during process startup.
	if _, err := s.db.Exec(`
	INSERT OR IGNORE INTO gateway_availability_hourly(hour_start, samples, up_samples, outage_starts)
	SELECT hour_start, COUNT(*), SUM(link_connected), SUM(outage_start)
	FROM (
		SELECT
			(timestamp / 3600) * 3600 AS hour_start,
			link_connected,
			CASE WHEN link_connected = 0 AND COALESCE(LAG(link_connected) OVER (ORDER BY id), 1) != 0 THEN 1 ELSE 0 END AS outage_start
		FROM gateway_samples
	)
	GROUP BY hour_start;
	`); err != nil {
		return fmt.Errorf("backfill gateway availability: %w", err)
	}

	// The transition must likewise be between consecutive non-empty addresses,
	// not between the current address and any older different address.
	if _, err := s.db.Exec(`
	INSERT OR IGNORE INTO gateway_public_ip_events(timestamp, old_ip, new_ip)
	SELECT timestamp, previous_ip, local_ip
	FROM (
		SELECT timestamp, local_ip,
		       LAG(local_ip) OVER (ORDER BY id) AS previous_ip
		FROM gateway_samples
		WHERE local_ip != ''
	)
	WHERE previous_ip IS NOT NULL AND previous_ip != local_ip;
	`); err != nil {
		return fmt.Errorf("backfill public IP history: %w", err)
	}
	return nil
}

func (s *Store) Insights(window time.Duration, ipLimit int) (Insights, error) {
	if s == nil || s.db == nil {
		return Insights{}, fmt.Errorf("gateway store is unavailable")
	}
	if window <= 0 || window > insightRetention {
		return Insights{}, fmt.Errorf("gateway insight window is out of range")
	}
	if ipLimit <= 0 {
		ipLimit = 20
	}
	if ipLimit > 100 {
		ipLimit = 100
	}

	now := time.Now().UTC()
	since := now.Add(-window).Unix()
	var result Insights
	result.WindowDays = int(window / (24 * time.Hour))
	var firstHour, lastHour *int64
	if err := s.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(samples), 0), COALESCE(SUM(up_samples), 0),
		       COALESCE(SUM(outage_starts), 0), MIN(hour_start), MAX(hour_start)
		FROM gateway_availability_hourly WHERE hour_start >= ?`, since).
		Scan(&result.SampledHours, &result.Samples, &result.UpSamples, &result.Outages, &firstHour, &lastHour); err != nil {
		return Insights{}, fmt.Errorf("read gateway availability: %w", err)
	}
	if result.Samples > 0 {
		result.Available = true
		result.UptimePercent = float64(result.UpSamples) * 100 / float64(result.Samples)
	}
	if firstHour != nil {
		value := time.Unix(*firstHour, 0).UTC()
		result.FirstSample = &value
	}
	if lastHour != nil {
		value := time.Unix(*lastHour, 0).UTC().Add(time.Hour)
		result.LastSample = &value
	}

	rows, err := s.db.Query(`
		SELECT timestamp, old_ip, new_ip
		FROM gateway_public_ip_events
		ORDER BY timestamp DESC LIMIT ?`, ipLimit)
	if err != nil {
		return Insights{}, fmt.Errorf("read public IP history: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var timestamp int64
		var change PublicIPChange
		if err := rows.Scan(&timestamp, &change.OldIP, &change.NewIP); err != nil {
			return Insights{}, fmt.Errorf("scan public IP history: %w", err)
		}
		change.Timestamp = time.Unix(timestamp, 0).UTC()
		result.PublicIPChanges = append(result.PublicIPChanges, change)
	}
	if err := rows.Err(); err != nil {
		return Insights{}, fmt.Errorf("iterate public IP history: %w", err)
	}
	return result, nil
}

func (m *Monitor) Insights(window time.Duration, ipLimit int) (Insights, error) {
	if m == nil || m.store == nil {
		return Insights{}, fmt.Errorf("gateway monitor is not configured")
	}
	return m.store.Insights(window, ipLimit)
}
