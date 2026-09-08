package accounting

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const recentDays = 32

// UsagePoint contains aggregate byte deltas observed in a UTC hour/day.
// A point with no samples is a gap, not proof of zero traffic.
type UsagePoint struct {
	Start           time.Time `json:"start"`
	RXBytes         uint64    `json:"rx_bytes"`
	TXBytes         uint64    `json:"tx_bytes"`
	TotalBytes      uint64    `json:"total_bytes"`
	Samples         int       `json:"samples"`
	ObservedSeconds int64     `json:"observed_seconds"`
}

type Insights struct {
	Available        bool          `json:"available"`
	Enabled          bool          `json:"enabled"`
	Period           string        `json:"period"`
	From             time.Time     `json:"from"`
	Until            time.Time     `json:"until"`
	HistoryStartedAt *time.Time    `json:"history_started_at"`
	CollectedAt      *time.Time    `json:"collected_at"`
	Points           []UsagePoint  `json:"points"`
	Devices          []DeviceUsage `json:"devices"`
	RXBytes          uint64        `json:"rx_bytes"`
	TXBytes          uint64        `json:"tx_bytes"`
	TotalBytes       uint64        `json:"total_bytes"`
	PeakSampleMbps   *float64      `json:"peak_sample_mbps"`
	ObservedSeconds  int64         `json:"observed_seconds"`
}

// InsightsRange uses UTC calendar boundaries, consistently with monthly usage.
func InsightsRange(now time.Time, period string) (time.Time, time.Time, time.Duration, error) {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	switch period {
	case "today":
		return today, now, time.Hour, nil
	case "yesterday":
		return today.AddDate(0, 0, -1), today, time.Hour, nil
	case "7d":
		return today.AddDate(0, 0, -6), now, 24 * time.Hour, nil
	case "30d":
		return today.AddDate(0, 0, -29), now, 24 * time.Hour, nil
	default:
		return time.Time{}, time.Time{}, 0, fmt.Errorf("period must be today, yesterday, 7d or 30d")
	}
}

func migrateInsights(db *sql.DB) error {
	_, err := db.Exec(`
 CREATE TABLE IF NOT EXISTS traffic_clock(id INTEGER PRIMARY KEY CHECK(id=1), started_at INTEGER NOT NULL, last_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS traffic_hour(hour INTEGER PRIMARY KEY, rx_bytes INTEGER NOT NULL DEFAULT 0, tx_bytes INTEGER NOT NULL DEFAULT 0,
   samples INTEGER NOT NULL DEFAULT 0, observed_seconds INTEGER NOT NULL DEFAULT 0, peak_mbps REAL NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS device_day(address TEXT NOT NULL, day TEXT NOT NULL, rx_bytes INTEGER NOT NULL DEFAULT 0,
   tx_bytes INTEGER NOT NULL DEFAULT 0, last_seen INTEGER NOT NULL, PRIMARY KEY(address,day));
 CREATE INDEX IF NOT EXISTS idx_device_day_day ON device_day(day);`)
	if err != nil {
		return err
	}
	return migrateFirewall(db)
}

// Recent history begins with a fresh baseline. We cannot assign pre-upgrade
// counters, or counters spanning an outage, to a particular hour. Monthly
// totals retain those deltas; the recent chart leaves that interval blank.
// Deltas are grouped by collection time, not fabricated across missing hours.
func recordInsights(tx *sql.Tx, now time.Time, devices map[string]*DeviceUsage) error {
	var last int64
	err := tx.QueryRow(`SELECT last_at FROM traffic_clock WHERE id=1`).Scan(&last)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	epoch := now.UTC().Unix()
	elapsed := epoch - last
	if last > 0 && elapsed > 0 && elapsed <= 600 {
		var rx, upload uint64
		for _, d := range devices {
			rx += d.RXBytes
			upload += d.TXBytes
			if d.RXBytes+d.TXBytes == 0 {
				continue
			}
			_, err := tx.Exec(`INSERT INTO device_day(address,day,rx_bytes,tx_bytes,last_seen) VALUES(?,?,?,?,?)
    ON CONFLICT(address,day) DO UPDATE SET rx_bytes=rx_bytes+excluded.rx_bytes, tx_bytes=tx_bytes+excluded.tx_bytes,last_seen=excluded.last_seen`,
				d.Address, now.UTC().Format("2006-01-02"), d.RXBytes, d.TXBytes, epoch)
			if err != nil {
				return err
			}
		}
		peak := float64(rx+upload) * 8 / float64(elapsed) / 1e6
		_, err := tx.Exec(`INSERT INTO traffic_hour(hour,rx_bytes,tx_bytes,samples,observed_seconds,peak_mbps) VALUES(?,?,?,1,?,?)
   ON CONFLICT(hour) DO UPDATE SET rx_bytes=rx_bytes+excluded.rx_bytes,tx_bytes=tx_bytes+excluded.tx_bytes,
   samples=samples+1,observed_seconds=observed_seconds+excluded.observed_seconds,peak_mbps=MAX(peak_mbps,excluded.peak_mbps)`,
			now.UTC().Truncate(time.Hour).Unix(), rx, upload, elapsed, peak)
		if err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO traffic_clock(id,started_at,last_at) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET last_at=excluded.last_at`, epoch, epoch); err != nil {
		return err
	}
	cutoff := now.UTC().AddDate(0, 0, -recentDays)
	if _, err := tx.Exec(`DELETE FROM traffic_hour WHERE hour < ? OR hour > ?`, cutoff.Unix(), now.UTC().Add(time.Hour).Unix()); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM device_day WHERE day < ? OR day > ?`, cutoff.Format("2006-01-02"), now.UTC().Format("2006-01-02")); err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM device_day WHERE rowid NOT IN (SELECT rowid FROM device_day ORDER BY day DESC, rx_bytes+tx_bytes DESC LIMIT ?)`, maxTrackedDevices*recentDays)
	return err
}

func (s *Store) Insights(now time.Time, period string) (Insights, error) {
	from, until, step, err := InsightsRange(now, period)
	result := Insights{Period: period, From: from, Until: until, Points: []UsagePoint{}, Devices: []DeviceUsage{}}
	if err != nil {
		return result, err
	}
	if s == nil || s.db == nil {
		return result, nil
	}
	// Read all aggregates in one transaction so panels agree during collection.
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var started, last int64
	if err := tx.QueryRow(`SELECT started_at,last_at FROM traffic_clock WHERE id=1`).Scan(&started, &last); err != nil && err != sql.ErrNoRows {
		return result, err
	}
	if started > 0 {
		at := time.Unix(started, 0).UTC()
		result.HistoryStartedAt = &at
	}
	if last > 0 {
		at := time.Unix(last, 0).UTC()
		result.CollectedAt = &at
	}
	rows, err := tx.Query(`SELECT hour,rx_bytes,tx_bytes,samples,observed_seconds,peak_mbps FROM traffic_hour WHERE hour>=? AND hour<? ORDER BY hour`, from.Unix(), until.Unix())
	if err != nil {
		return result, err
	}
	points := map[int64]*UsagePoint{}
	pointEnd := until
	if period == "today" {
		pointEnd = from.Add(24 * time.Hour)
	}
	for at := from; at.Before(pointEnd); at = at.Add(step) {
		points[at.Unix()] = &UsagePoint{Start: at}
	}
	for rows.Next() {
		var at, seconds int64
		var rx, upload uint64
		var samples int
		var peak float64
		if err := rows.Scan(&at, &rx, &upload, &samples, &seconds, &peak); err != nil {
			rows.Close()
			return result, err
		}
		key := from.Add(time.Duration((at-from.Unix())/int64(step.Seconds())) * step).Unix()
		p := points[key]
		if p == nil {
			continue
		}
		p.RXBytes += rx
		p.TXBytes += upload
		p.TotalBytes += rx + upload
		p.Samples += samples
		p.ObservedSeconds += seconds
		result.RXBytes += rx
		result.TXBytes += upload
		result.TotalBytes += rx + upload
		result.ObservedSeconds += seconds
		if result.PeakSampleMbps == nil || peak > *result.PeakSampleMbps {
			value := peak
			result.PeakSampleMbps = &value
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	for at := from; at.Before(pointEnd); at = at.Add(step) {
		result.Points = append(result.Points, *points[at.Unix()])
	}
	rows, err = tx.Query(`SELECT address,SUM(rx_bytes),SUM(tx_bytes),MAX(last_seen) FROM device_day WHERE day>=? AND day<=? GROUP BY address ORDER BY SUM(rx_bytes)+SUM(tx_bytes) DESC,address LIMIT ?`,
		from.Format("2006-01-02"), until.Add(-time.Nanosecond).Format("2006-01-02"), maxTrackedDevices)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var d DeviceUsage
		if err := rows.Scan(&d.Address, &d.RXBytes, &d.TXBytes, &d.LastSeenUTC); err != nil {
			rows.Close()
			return result, err
		}
		d.TotalBytes = d.RXBytes + d.TXBytes
		result.Devices = append(result.Devices, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	if err := tx.Commit(); err != nil {
		return result, err
	}
	result.Available = true
	result.Enabled = true
	return result, nil
}
