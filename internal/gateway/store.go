package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const (
	retentionPeriod = 7 * 24 * time.Hour
	maxSampleRows   = 41000
	maxEventRows    = 2048
)

type Store struct {
	db *sql.DB
}

func OpenStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("create gateway data directory: %w", err)
	}
	if err := os.Chmod(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("secure gateway data directory: %w", err)
	}
	path := filepath.Join(dataDir, "gateway-quality.db")
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=trusted_schema(OFF)&_pragma=secure_delete(ON)&_pragma=busy_timeout(3000)&_pragma=cache_size(-1000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open gateway history: %w", err)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(2 * time.Minute)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize gateway history: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, fmt.Errorf("secure gateway history: %w", err)
	}
	if err := migrateGatewayStore(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

const (
	// hourlyRetention bounds the rollup table. 400 days keeps a full year plus
	// slack while staying trivially small on disk.
	hourlyRetention = 400 * 24 * time.Hour
	maxHourlyRows   = 12000
	// rawHistoryHorizon is how far back History() trusts raw samples. It sits
	// just inside retentionPeriod so a request that straddles the prune boundary
	// falls back to the rollup instead of returning a truncated series.
	rawHistoryHorizon = 6 * 24 * time.Hour
)

func migrateGatewayStore(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS gateway_samples (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp INTEGER NOT NULL,
		state TEXT NOT NULL,
		link_connected INTEGER NOT NULL,
		local_ip TEXT NOT NULL DEFAULT '',
		peer_ip TEXT NOT NULL DEFAULT '',
		targets_json TEXT NOT NULL,
		peer_probe_json TEXT NOT NULL DEFAULT '',
		latency_ms REAL NOT NULL DEFAULT 0,
		jitter_ms REAL NOT NULL DEFAULT 0,
		packet_loss_percent REAL NOT NULL DEFAULT 100,
		pppoe_uptime_seconds INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_gateway_samples_timestamp ON gateway_samples(timestamp);
	CREATE TABLE IF NOT EXISTS gateway_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp INTEGER NOT NULL,
		type TEXT NOT NULL CHECK(type IN ('reconnect'))
	);
	CREATE INDEX IF NOT EXISTS idx_gateway_events_timestamp ON gateway_events(timestamp);
	-- Raw samples are pruned after retentionPeriod (7 days). Proving an ISP
	-- problem usually needs a longer view than that, but keeping 30 days of raw
	-- 30-second samples would be ~86k rows. Roll each hour up instead: 400 days
	-- of hourly rows is under 10k and answers "was last Tuesday evening bad?".
	CREATE TABLE IF NOT EXISTS gateway_hourly (
		hour_start INTEGER PRIMARY KEY,
		samples INTEGER NOT NULL DEFAULT 0,
		latency_sum REAL NOT NULL DEFAULT 0,
		jitter_sum REAL NOT NULL DEFAULT 0,
		loss_sum REAL NOT NULL DEFAULT 0,
		worst_state TEXT NOT NULL DEFAULT 'unknown',
		uptime_max INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS gateway_settings (
		id INTEGER PRIMARY KEY CHECK(id = 1),
		enabled INTEGER NOT NULL,
		targets_json TEXT NOT NULL,
		interval_seconds INTEGER NOT NULL
	);
	`)
	if err != nil {
		return fmt.Errorf("migrate gateway history: %w", err)
	}
	defaults := DefaultSettings()
	targetsJSON, err := json.Marshal(defaults.Targets)
	if err != nil {
		return fmt.Errorf("encode default gateway settings: %w", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO gateway_settings
		(id, enabled, targets_json, interval_seconds) VALUES (1, ?, ?, ?)`,
		boolInt(defaults.Enabled), string(targetsJSON), defaults.IntervalSeconds); err != nil {
		return fmt.Errorf("initialize gateway settings: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Settings() (Settings, error) {
	if s == nil || s.db == nil {
		return Settings{}, fmt.Errorf("gateway store is unavailable")
	}
	var settings Settings
	var enabled int
	var targetsJSON string
	if err := s.db.QueryRow(`SELECT enabled, targets_json, interval_seconds
		FROM gateway_settings WHERE id = 1`).Scan(&enabled, &targetsJSON, &settings.IntervalSeconds); err != nil {
		return Settings{}, fmt.Errorf("load gateway settings: %w", err)
	}
	settings.Enabled = enabled != 0
	if err := json.Unmarshal([]byte(targetsJSON), &settings.Targets); err != nil {
		return Settings{}, fmt.Errorf("decode gateway settings: %w", err)
	}
	if err := settings.Validate(); err != nil {
		return Settings{}, fmt.Errorf("stored gateway settings are invalid: %w", err)
	}
	return settings, nil
}

func (s *Store) SaveSettings(settings Settings) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("gateway store is unavailable")
	}
	if err := settings.Validate(); err != nil {
		return err
	}
	targetsJSON, err := json.Marshal(settings.Targets)
	if err != nil {
		return fmt.Errorf("encode gateway settings: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE gateway_settings SET enabled = ?, targets_json = ?,
		interval_seconds = ? WHERE id = 1`, boolInt(settings.Enabled), string(targetsJSON), settings.IntervalSeconds); err != nil {
		return fmt.Errorf("save gateway settings: %w", err)
	}
	return nil
}

func (s *Store) SaveSample(sample Sample) error {
	targetsJSON, err := json.Marshal(sample.Targets)
	if err != nil {
		return fmt.Errorf("encode gateway targets: %w", err)
	}
	peerJSON := []byte{}
	if sample.PeerProbe != nil {
		peerJSON, err = json.Marshal(sample.PeerProbe)
		if err != nil {
			return fmt.Errorf("encode PPPoE peer probe: %w", err)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO gateway_samples
		(timestamp, state, link_connected, local_ip, peer_ip, targets_json, peer_probe_json,
		 latency_ms, jitter_ms, packet_loss_percent, pppoe_uptime_seconds)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sample.Timestamp.UTC().Unix(), string(sample.State), boolInt(sample.Link.Connected),
		sample.Link.LocalIP, sample.Link.PeerIP, string(targetsJSON), string(peerJSON), sample.LatencyMS,
		sample.JitterMS, sample.PacketLossPercent, sample.PPPoEUptime); err != nil {
		return fmt.Errorf("save gateway sample: %w", err)
	}
	hourStart := sample.Timestamp.UTC().Truncate(time.Hour).Unix()
	if _, err := tx.Exec(`INSERT INTO gateway_hourly
		(hour_start, samples, latency_sum, jitter_sum, loss_sum, worst_state, uptime_max)
		VALUES (?, 1, ?, ?, ?, ?, ?)
		ON CONFLICT(hour_start) DO UPDATE SET
			samples = samples + 1,
			latency_sum = latency_sum + excluded.latency_sum,
			jitter_sum = jitter_sum + excluded.jitter_sum,
			loss_sum = loss_sum + excluded.loss_sum,
			uptime_max = MAX(uptime_max, excluded.uptime_max),
			worst_state = CASE
				WHEN `+stateSeverityCase("excluded.worst_state")+` > `+stateSeverityCase("worst_state")+`
				THEN excluded.worst_state ELSE worst_state END`,
		hourStart, sample.LatencyMS, sample.JitterMS, sample.PacketLossPercent,
		string(sample.State), sample.PPPoEUptime); err != nil {
		return fmt.Errorf("roll up gateway sample: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM gateway_hourly WHERE hour_start < ?`,
		sample.Timestamp.Add(-hourlyRetention).UTC().Unix()); err != nil {
		return fmt.Errorf("prune gateway rollup: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM gateway_hourly WHERE hour_start NOT IN
		(SELECT hour_start FROM gateway_hourly ORDER BY hour_start DESC LIMIT ?)`, maxHourlyRows); err != nil {
		return fmt.Errorf("bound gateway rollup: %w", err)
	}
	cutoff := sample.Timestamp.Add(-retentionPeriod).UTC().Unix()
	if _, err := tx.Exec(`DELETE FROM gateway_samples WHERE timestamp < ?`, cutoff); err != nil {
		return fmt.Errorf("prune old gateway samples: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM gateway_samples WHERE id NOT IN
		(SELECT id FROM gateway_samples ORDER BY id DESC LIMIT ?)`, maxSampleRows); err != nil {
		return fmt.Errorf("bound gateway samples: %w", err)
	}
	return tx.Commit()
}

func (s *Store) AddReconnect(at time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO gateway_events(timestamp, type) VALUES (?, 'reconnect')`, at.UTC().Unix()); err != nil {
		return fmt.Errorf("save reconnect event: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM gateway_events WHERE timestamp < ?`, at.Add(-retentionPeriod).UTC().Unix()); err != nil {
		return fmt.Errorf("prune reconnect events: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM gateway_events WHERE id NOT IN
		(SELECT id FROM gateway_events ORDER BY id DESC LIMIT ?)`, maxEventRows); err != nil {
		return fmt.Errorf("bound reconnect events: %w", err)
	}
	return tx.Commit()
}

func (s *Store) CountReconnects(since time.Time) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM gateway_events WHERE type = 'reconnect' AND timestamp >= ?`, since.UTC().Unix()).Scan(&count)
	return count, err
}

func (s *Store) LatestSample() (Sample, bool, error) {
	row := s.db.QueryRow(`SELECT timestamp, state, link_connected, local_ip, peer_ip,
		targets_json, peer_probe_json, latency_ms, jitter_ms, packet_loss_percent, pppoe_uptime_seconds
		FROM gateway_samples ORDER BY timestamp DESC, id DESC LIMIT 1`)
	var sample Sample
	var timestamp int64
	var connected int
	var targetsJSON, peerJSON, state string
	if err := row.Scan(&timestamp, &state, &connected, &sample.Link.LocalIP,
		&sample.Link.PeerIP, &targetsJSON, &peerJSON, &sample.LatencyMS, &sample.JitterMS,
		&sample.PacketLossPercent, &sample.PPPoEUptime); err != nil {
		if err == sql.ErrNoRows {
			return Sample{}, false, nil
		}
		return Sample{}, false, err
	}
	sample.Timestamp = time.Unix(timestamp, 0).UTC()
	sample.State = HealthState(state)
	sample.Link.Connected = connected != 0
	sample.Link.Interface = "ppp0"
	if err := json.Unmarshal([]byte(targetsJSON), &sample.Targets); err != nil {
		return Sample{}, false, fmt.Errorf("decode stored gateway targets: %w", err)
	}
	if peerJSON != "" {
		var peer TargetResult
		if err := json.Unmarshal([]byte(peerJSON), &peer); err != nil {
			return Sample{}, false, fmt.Errorf("decode stored PPPoE peer probe: %w", err)
		}
		sample.PeerProbe = &peer
	}
	return sample, true, nil
}

// stateSeverityCase renders the severity ordering used by worst_state as SQL so
// the rollup keeps the worst state seen in the hour rather than the last one.
func stateSeverityCase(column string) string {
	return "(CASE " + column +
		" WHEN 'offline' THEN 4" +
		" WHEN 'flapping' THEN 3" +
		" WHEN 'degraded' THEN 2" +
		" WHEN 'healthy' THEN 1" +
		" ELSE 0 END)"
}

// hourlyHistory serves windows longer than the raw retention from the rollup.
func (s *Store) hourlyHistory(since time.Time, maxPoints int) ([]HistoryPoint, error) {
	rows, err := s.db.Query(`SELECT hour_start, samples, latency_sum, jitter_sum, loss_sum,
		worst_state, uptime_max FROM gateway_hourly WHERE hour_start >= ? ORDER BY hour_start ASC`,
		since.UTC().Truncate(time.Hour).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var raw []HistoryPoint
	for rows.Next() {
		var (
			hourStart                      int64
			samples                        int64
			latencySum, jitterSum, lossSum float64
			worstState                     string
			uptimeMax                      int64
		)
		if err := rows.Scan(&hourStart, &samples, &latencySum, &jitterSum, &lossSum, &worstState, &uptimeMax); err != nil {
			return nil, err
		}
		if samples <= 0 {
			continue
		}
		raw = append(raw, HistoryPoint{
			Timestamp:         time.Unix(hourStart, 0).UTC(),
			State:             HealthState(worstState),
			LatencyMS:         latencySum / float64(samples),
			JitterMS:          jitterSum / float64(samples),
			PacketLossPercent: lossSum / float64(samples),
			PPPoEUptime:       uptimeMax,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return aggregateHistory(raw, since, maxPoints), nil
}

func (s *Store) History(since time.Time, maxPoints int) ([]HistoryPoint, error) {
	if maxPoints < 1 {
		maxPoints = 1
	}
	if maxPoints > 720 {
		maxPoints = 720
	}
	if time.Since(since) > rawHistoryHorizon {
		return s.hourlyHistory(since, maxPoints)
	}
	rows, err := s.db.Query(`SELECT timestamp, state, latency_ms, jitter_ms,
		packet_loss_percent, pppoe_uptime_seconds FROM gateway_samples
		WHERE timestamp >= ? ORDER BY timestamp ASC`, since.UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var raw []HistoryPoint
	for rows.Next() {
		var point HistoryPoint
		var timestamp int64
		var state string
		if err := rows.Scan(&timestamp, &state, &point.LatencyMS, &point.JitterMS,
			&point.PacketLossPercent, &point.PPPoEUptime); err != nil {
			return nil, err
		}
		point.Timestamp = time.Unix(timestamp, 0).UTC()
		point.State = HealthState(state)
		raw = append(raw, point)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return aggregateHistory(raw, since, maxPoints), nil
}

func aggregateHistory(raw []HistoryPoint, since time.Time, maxPoints int) []HistoryPoint {
	if len(raw) <= maxPoints {
		return raw
	}
	window := raw[len(raw)-1].Timestamp.Sub(since)
	if window <= 0 {
		window = time.Hour
	}
	step := window / time.Duration(maxPoints)
	if step < time.Second {
		step = time.Second
	}
	type bucket struct {
		count                 int
		latency, jitter, loss float64
		uptime                int64
		state                 HealthState
		timestamp             time.Time
	}
	buckets := make(map[int64]*bucket)
	order := make([]int64, 0, maxPoints)
	base := since.UTC()
	for _, point := range raw {
		key := int64(point.Timestamp.Sub(base) / step)
		if key >= int64(maxPoints) {
			key = int64(maxPoints - 1)
		}
		item, exists := buckets[key]
		if !exists {
			item = &bucket{timestamp: base.Add(time.Duration(key) * step), state: point.State}
			buckets[key] = item
			order = append(order, key)
		}
		item.count++
		item.latency += point.LatencyMS
		item.jitter += point.JitterMS
		item.loss += point.PacketLossPercent
		item.uptime = point.PPPoEUptime
		if stateSeverity(point.State) > stateSeverity(item.state) {
			item.state = point.State
		}
	}
	result := make([]HistoryPoint, 0, len(order))
	for _, key := range order {
		item := buckets[key]
		result = append(result, HistoryPoint{
			Timestamp: item.timestamp, State: item.state,
			LatencyMS:         item.latency / float64(item.count),
			JitterMS:          item.jitter / float64(item.count),
			PacketLossPercent: item.loss / float64(item.count),
			PPPoEUptime:       item.uptime,
		})
	}
	return result
}

func stateSeverity(state HealthState) int {
	switch state {
	case StateOffline:
		return 4
	case StateFlapping:
		return 3
	case StateDegraded:
		return 2
	case StateHealthy:
		return 1
	default:
		return 0
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
