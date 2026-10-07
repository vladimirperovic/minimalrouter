package accounting

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// DNS activity history.
//
// Unlike byte accounting this is browsing history, so it is opt-in and kept
// deliberately coarse on disk: one row per UTC day, device and site with a
// lookup count and first/last time seen, plus hourly lookup totals per device
// for the chart. Individual queries and full hostnames exist only in memory
// (see DNSCollector.Recent) and are lost on restart by design.

const (
	// maxDNSDailyRows bounds distinct device/site pairs stored per UTC day.
	// Lookups for new pairs beyond it are counted as unitemized instead.
	maxDNSDailyRows = 20000
	// maxDNSTotalRows bounds the whole daily table; the oldest days go first.
	maxDNSTotalRows = 1000000
)

// DNSDailyKey identifies one aggregated row.
type DNSDailyKey struct {
	Day     int64 // UTC midnight, Unix seconds
	Address string
	Site    string
}

// DNSDailyCount is the in-memory aggregate for one DNSDailyKey.
type DNSDailyCount struct {
	Lookups   uint64
	FirstSeen int64
	LastSeen  int64
}

// DNSHourlyKey identifies one device's lookups in one UTC hour.
type DNSHourlyKey struct {
	Hour    int64 // UTC hour start, Unix seconds
	Address string
}

// DNSFlush is one batch of aggregated lookups written in a single transaction.
type DNSFlush struct {
	Daily      map[DNSDailyKey]DNSDailyCount
	Hourly     map[DNSHourlyKey]uint64
	Unitemized map[int64]uint64 // UTC day -> lookups not attributed to a site
}

func (f DNSFlush) empty() bool {
	return len(f.Daily) == 0 && len(f.Hourly) == 0 && len(f.Unitemized) == 0
}

// DNSPoint is the lookup total for one chart bucket. A bucket before history
// started or after the last collection is a gap, not proof of zero lookups.
type DNSPoint struct {
	Start    time.Time `json:"start"`
	Lookups  uint64    `json:"lookups"`
	Observed bool      `json:"observed"`
}

// DNSDeviceUsage summarizes one device over the selected period.
type DNSDeviceUsage struct {
	Address  string `json:"address"`
	Hostname string `json:"hostname,omitempty"`
	MAC      string `json:"mac,omitempty"`
	Lookups  uint64 `json:"lookups"`
	Sites    int    `json:"sites"`
	LastSeen int64  `json:"last_seen"`
}

// DNSSiteUsage summarizes one site over the selected period.
type DNSSiteUsage struct {
	Site      string `json:"site"`
	Category  string `json:"category,omitempty"`
	Lookups   uint64 `json:"lookups"`
	Devices   int    `json:"devices"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
}

// DNSDeviceSite is one device's use of one site over the selected period.
type DNSDeviceSite struct {
	Address   string `json:"address"`
	Hostname  string `json:"hostname,omitempty"`
	Site      string `json:"site"`
	Category  string `json:"category,omitempty"`
	Lookups   uint64 `json:"lookups"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
}

// DNSActivity is the API-facing summary for one period.
type DNSActivity struct {
	Available         bool             `json:"available"`
	Enabled           bool             `json:"enabled"`
	RetentionDays     int              `json:"retention_days"`
	Period            string           `json:"period"`
	From              time.Time        `json:"from"`
	Until             time.Time        `json:"until"`
	Device            string           `json:"device,omitempty"`
	Search            string           `json:"search,omitempty"`
	HistoryStartedAt  *time.Time       `json:"history_started_at"`
	CollectedAt       *time.Time       `json:"collected_at"`
	TotalLookups      uint64           `json:"total_lookups"`
	UnitemizedLookups uint64           `json:"unitemized_lookups"`
	SiteCount         int              `json:"site_count"`
	Points            []DNSPoint       `json:"points"`
	Devices           []DNSDeviceUsage `json:"devices"`
	Sites             []DNSSiteUsage   `json:"sites"`
	Flagged           []DNSDeviceSite  `json:"flagged"`
}

// DNSQuery selects what DNSActivity returns.
type DNSQuery struct {
	Period string
	Device string
	Search string
	// FlaggedSites are matched exactly against stored sites, e.g. the adult
	// category list. The store does not know categories itself.
	FlaggedSites []string
	SiteLimit    int
}

func migrateDNS(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS dns_daily (
		day INTEGER NOT NULL,
		address TEXT NOT NULL,
		site TEXT NOT NULL,
		lookups INTEGER NOT NULL,
		first_seen INTEGER NOT NULL,
		last_seen INTEGER NOT NULL,
		PRIMARY KEY (day, address, site)
	) WITHOUT ROWID;
	CREATE INDEX IF NOT EXISTS idx_dns_daily_site ON dns_daily(day, site);
	CREATE TABLE IF NOT EXISTS dns_hourly (
		hour INTEGER NOT NULL,
		address TEXT NOT NULL,
		lookups INTEGER NOT NULL,
		PRIMARY KEY (hour, address)
	) WITHOUT ROWID;
	CREATE TABLE IF NOT EXISTS dns_unitemized (day INTEGER PRIMARY KEY, lookups INTEGER NOT NULL);
	CREATE TABLE IF NOT EXISTS dns_clock (id INTEGER PRIMARY KEY CHECK(id = 1), started_at INTEGER NOT NULL, last_at INTEGER NOT NULL);
	-- The operator's setting. It lives here rather than in the canonical
	-- configuration so the feature does not touch packages compiled into the
	-- byte-identical bootstrap tools.
	CREATE TABLE IF NOT EXISTS dns_settings (id INTEGER PRIMARY KEY CHECK(id = 1), enabled INTEGER NOT NULL, retention_days INTEGER NOT NULL);
	`)
	if err != nil {
		return fmt.Errorf("migrate DNS activity store: %w", err)
	}
	return nil
}

// Retention limits for DNS activity history, in days.
const (
	DefaultDNSRetentionDays = 30
	MaxDNSRetentionDays     = 90
)

// DNSSettings returns the stored setting; recording is off until enabled.
func (s *Store) DNSSettings() (DNSSettings, error) {
	settings := DNSSettings{RetentionDays: DefaultDNSRetentionDays}
	if s == nil || s.db == nil {
		return settings, nil
	}
	var enabled int
	err := s.db.QueryRow(`SELECT enabled, retention_days FROM dns_settings WHERE id = 1`).Scan(&enabled, &settings.RetentionDays)
	if err == sql.ErrNoRows {
		return settings, nil
	}
	settings.Enabled = enabled != 0
	return settings, err
}

// SetDNSSettings validates and stores the setting.
func (s *Store) SetDNSSettings(settings DNSSettings) error {
	if settings.RetentionDays < 1 || settings.RetentionDays > MaxDNSRetentionDays {
		return fmt.Errorf("retention_days must be between 1 and %d", MaxDNSRetentionDays)
	}
	if s == nil || s.db == nil {
		return fmt.Errorf("DNS activity store unavailable")
	}
	enabled := 0
	if settings.Enabled {
		enabled = 1
	}
	_, err := s.db.Exec(`INSERT INTO dns_settings(id, enabled, retention_days) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET enabled = excluded.enabled, retention_days = excluded.retention_days`, enabled, settings.RetentionDays)
	return err
}

// RecordDNS writes one flush in a single transaction. Rows for a day that has
// already reached maxDNSDailyRows only grow existing pairs; lookups for new
// pairs are added to that day's unitemized total.
func (s *Store) RecordDNS(now time.Time, flush DNSFlush) error {
	if s == nil || s.db == nil || flush.empty() {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	rowsPerDay := map[int64]int{}
	unitemized := map[int64]uint64{}
	for day, lookups := range flush.Unitemized {
		unitemized[day] += lookups
	}
	keys := make([]DNSDailyKey, 0, len(flush.Daily))
	for key := range flush.Daily {
		keys = append(keys, key)
	}
	// Busy pairs first, so a day that reaches the bound keeps its most
	// significant sites itemized.
	sort.Slice(keys, func(i, j int) bool { return flush.Daily[keys[i]].Lookups > flush.Daily[keys[j]].Lookups })
	for _, key := range keys {
		count := flush.Daily[key]
		result, err := tx.Exec(`UPDATE dns_daily SET lookups = lookups + ?, first_seen = MIN(first_seen, ?), last_seen = MAX(last_seen, ?)
			WHERE day = ? AND address = ? AND site = ?`, count.Lookups, count.FirstSeen, count.LastSeen, key.Day, key.Address, key.Site)
		if err != nil {
			return fmt.Errorf("update DNS activity: %w", err)
		}
		if changed, _ := result.RowsAffected(); changed > 0 {
			continue
		}
		rows, seen := rowsPerDay[key.Day]
		if !seen {
			if err := tx.QueryRow(`SELECT COUNT(*) FROM dns_daily WHERE day = ?`, key.Day).Scan(&rows); err != nil {
				return fmt.Errorf("count DNS activity rows: %w", err)
			}
		}
		if rows >= maxDNSDailyRows {
			rowsPerDay[key.Day] = rows
			unitemized[key.Day] += count.Lookups
			continue
		}
		if _, err := tx.Exec(`INSERT INTO dns_daily(day, address, site, lookups, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?)`,
			key.Day, key.Address, key.Site, count.Lookups, count.FirstSeen, count.LastSeen); err != nil {
			return fmt.Errorf("insert DNS activity: %w", err)
		}
		rowsPerDay[key.Day] = rows + 1
	}
	for key, lookups := range flush.Hourly {
		if _, err := tx.Exec(`INSERT INTO dns_hourly(hour, address, lookups) VALUES (?, ?, ?)
			ON CONFLICT(hour, address) DO UPDATE SET lookups = lookups + excluded.lookups`, key.Hour, key.Address, lookups); err != nil {
			return fmt.Errorf("record hourly DNS activity: %w", err)
		}
	}
	for day, lookups := range unitemized {
		if lookups == 0 {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO dns_unitemized(day, lookups) VALUES (?, ?)
			ON CONFLICT(day) DO UPDATE SET lookups = lookups + excluded.lookups`, day, lookups); err != nil {
			return fmt.Errorf("record unitemized DNS activity: %w", err)
		}
	}
	epoch := now.UTC().Unix()
	if _, err := tx.Exec(`INSERT INTO dns_clock(id, started_at, last_at) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET last_at = excluded.last_at`, epoch, epoch); err != nil {
		return fmt.Errorf("record DNS activity clock: %w", err)
	}
	return tx.Commit()
}

// TouchDNSClock records a collection round that found no lookups, so the
// chart can tell a quiet hour from a collection gap.
func (s *Store) TouchDNSClock(now time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	epoch := now.UTC().Unix()
	_, err := s.db.Exec(`INSERT INTO dns_clock(id, started_at, last_at) VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET last_at = excluded.last_at`, epoch, epoch)
	return err
}

// PruneDNS removes days older than the retention window, any future-dated
// rows left by a clock correction, and the oldest days beyond maxDNSTotalRows.
func (s *Store) PruneDNS(now time.Time, retentionDays int) error {
	if s == nil || s.db == nil {
		return nil
	}
	if retentionDays <= 0 {
		retentionDays = DefaultDNSRetentionDays
	}
	today := utcDay(now)
	cutoff := today - int64(retentionDays-1)*86400
	tomorrow := today + 86400
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`DELETE FROM dns_daily WHERE day < ? OR day >= ?`,
		`DELETE FROM dns_hourly WHERE hour < ? OR hour >= ?`,
		`DELETE FROM dns_unitemized WHERE day < ? OR day >= ?`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement, cutoff, tomorrow); err != nil {
			return fmt.Errorf("prune DNS activity: %w", err)
		}
	}
	var total int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM dns_daily`).Scan(&total); err != nil {
		return err
	}
	for total > maxDNSTotalRows {
		var oldest int64
		if err := tx.QueryRow(`SELECT MIN(day) FROM dns_daily`).Scan(&oldest); err != nil {
			return err
		}
		result, err := tx.Exec(`DELETE FROM dns_daily WHERE day = ?`, oldest)
		if err != nil {
			return err
		}
		removed, _ := result.RowsAffected()
		if removed == 0 {
			break
		}
		total -= int(removed)
		if _, err := tx.Exec(`DELETE FROM dns_hourly WHERE hour < ?`, oldest+86400); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM dns_unitemized WHERE day <= ?`, oldest); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// HasDNSHistory reports whether any DNS activity is stored.
func (s *Store) HasDNSHistory() (bool, error) {
	if s == nil || s.db == nil {
		return false, nil
	}
	var exists int
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM dns_daily) OR EXISTS(SELECT 1 FROM dns_hourly)
		OR EXISTS(SELECT 1 FROM dns_unitemized) OR EXISTS(SELECT 1 FROM dns_clock)`).Scan(&exists)
	return exists != 0, err
}

// ClearDNS deletes all DNS activity history.
func (s *Store) ClearDNS() error {
	if s == nil || s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"dns_daily", "dns_hourly", "dns_unitemized", "dns_clock"} {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DNSActivity summarizes one period. Days and hours are UTC, matching the
// traffic insights.
func (s *Store) DNSActivity(now time.Time, query DNSQuery) (DNSActivity, error) {
	from, until, step, err := InsightsRange(now, query.Period)
	if err != nil {
		return DNSActivity{}, err
	}
	result := DNSActivity{
		Available: s != nil && s.db != nil, Period: query.Period, From: from, Until: until,
		Device: query.Device, Search: query.Search,
		Points: []DNSPoint{}, Devices: []DNSDeviceUsage{}, Sites: []DNSSiteUsage{}, Flagged: []DNSDeviceSite{},
	}
	if !result.Available {
		return result, nil
	}
	limit := query.SiteLimit
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	var started, last sql.NullInt64
	if err := s.db.QueryRow(`SELECT started_at, last_at FROM dns_clock WHERE id = 1`).Scan(&started, &last); err != nil && err != sql.ErrNoRows {
		return result, err
	}
	if started.Valid {
		value := time.Unix(started.Int64, 0).UTC()
		result.HistoryStartedAt = &value
	}
	if last.Valid {
		value := time.Unix(last.Int64, 0).UTC()
		result.CollectedAt = &value
	}

	// Day keys are UTC midnights; until is exclusive for "yesterday" (today's
	// midnight) and "now" otherwise, which still includes today's key.
	fromDay, untilEpoch := from.Unix(), until.Unix()
	where := `day >= ? AND day < ?`
	args := []any{fromDay, untilEpoch}
	if query.Device != "" {
		where += ` AND address = ?`
		args = append(args, query.Device)
	}

	if err := s.db.QueryRow(`SELECT COALESCE(SUM(lookups), 0), COUNT(DISTINCT site) FROM dns_daily WHERE `+where, args...).
		Scan(&result.TotalLookups, &result.SiteCount); err != nil {
		return result, err
	}
	if query.Device == "" {
		if err := s.db.QueryRow(`SELECT COALESCE(SUM(lookups), 0) FROM dns_unitemized WHERE day >= ? AND day < ?`, fromDay, untilEpoch).
			Scan(&result.UnitemizedLookups); err != nil {
			return result, err
		}
		result.TotalLookups += result.UnitemizedLookups
	}

	points, err := s.dnsPoints(from, until, step, query.Device, started, last)
	if err != nil {
		return result, err
	}
	result.Points = points

	devices, err := s.db.Query(`SELECT address, SUM(lookups), COUNT(DISTINCT site), MAX(last_seen) FROM dns_daily
		WHERE `+where+` GROUP BY address ORDER BY SUM(lookups) DESC LIMIT 100`, args...)
	if err != nil {
		return result, err
	}
	for devices.Next() {
		var device DNSDeviceUsage
		if err := devices.Scan(&device.Address, &device.Lookups, &device.Sites, &device.LastSeen); err != nil {
			devices.Close()
			return result, err
		}
		result.Devices = append(result.Devices, device)
	}
	devices.Close()
	if err := devices.Err(); err != nil {
		return result, err
	}

	siteWhere, siteArgs := where, append([]any{}, args...)
	if query.Search != "" {
		siteWhere += ` AND site LIKE ? ESCAPE '\'`
		siteArgs = append(siteArgs, "%"+escapeLike(query.Search)+"%")
	}
	sites, err := s.db.Query(`SELECT site, SUM(lookups), COUNT(DISTINCT address), MIN(first_seen), MAX(last_seen) FROM dns_daily
		WHERE `+siteWhere+` GROUP BY site ORDER BY SUM(lookups) DESC, site LIMIT ?`, append(siteArgs, limit)...)
	if err != nil {
		return result, err
	}
	for sites.Next() {
		var site DNSSiteUsage
		if err := sites.Scan(&site.Site, &site.Lookups, &site.Devices, &site.FirstSeen, &site.LastSeen); err != nil {
			sites.Close()
			return result, err
		}
		result.Sites = append(result.Sites, site)
	}
	sites.Close()
	if err := sites.Err(); err != nil {
		return result, err
	}

	if len(query.FlaggedSites) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(query.FlaggedSites)), ",")
		flaggedArgs := append([]any{}, args...)
		for _, site := range query.FlaggedSites {
			flaggedArgs = append(flaggedArgs, site)
		}
		flagged, err := s.db.Query(`SELECT address, site, SUM(lookups), MIN(first_seen), MAX(last_seen) FROM dns_daily
			WHERE `+where+` AND site IN (`+placeholders+`) GROUP BY address, site ORDER BY MAX(last_seen) DESC LIMIT 200`, flaggedArgs...)
		if err != nil {
			return result, err
		}
		for flagged.Next() {
			var item DNSDeviceSite
			if err := flagged.Scan(&item.Address, &item.Site, &item.Lookups, &item.FirstSeen, &item.LastSeen); err != nil {
				flagged.Close()
				return result, err
			}
			result.Flagged = append(result.Flagged, item)
		}
		flagged.Close()
		if err := flagged.Err(); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Store) dnsPoints(from, until time.Time, step time.Duration, device string, started, last sql.NullInt64) ([]DNSPoint, error) {
	query := `SELECT hour, SUM(lookups) FROM dns_hourly WHERE hour >= ? AND hour < ?`
	args := []any{from.Unix(), until.Unix()}
	if device != "" {
		query += ` AND address = ?`
		args = append(args, device)
	}
	rows, err := s.db.Query(query+` GROUP BY hour`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byBucket := map[int64]uint64{}
	stepSeconds := int64(step / time.Second)
	for rows.Next() {
		var hour int64
		var lookups uint64
		if err := rows.Scan(&hour, &lookups); err != nil {
			return nil, err
		}
		byBucket[from.Unix()+(hour-from.Unix())/stepSeconds*stepSeconds] += lookups
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	points := []DNSPoint{}
	for start := from; start.Before(until); start = start.Add(step) {
		end := start.Add(step).Unix()
		observed := started.Valid && last.Valid && started.Int64 < end && last.Int64 >= start.Unix()
		points = append(points, DNSPoint{Start: start, Lookups: byBucket[start.Unix()], Observed: observed || byBucket[start.Unix()] > 0})
	}
	return points, nil
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

func utcDay(at time.Time) int64 {
	return at.UTC().Truncate(24 * time.Hour).Unix()
}

// SiteOf groups a hostname under the registrable domain a person would
// recognize: "rr3---sn-abc.googlevideo.com" becomes "googlevideo.com" and
// "news.bbc.co.uk" becomes "bbc.co.uk". It is a deliberate approximation of
// the public suffix list: two labels, or three when a two-letter country code
// is preceded by a common second-level label such as co, com or org.
func SiteOf(name string) string {
	labels := strings.Split(name, ".")
	count := len(labels)
	if count <= 2 {
		return name
	}
	if len(labels[count-1]) == 2 && secondLevelLabels[labels[count-2]] {
		return strings.Join(labels[count-3:], ".")
	}
	return strings.Join(labels[count-2:], ".")
}

var secondLevelLabels = map[string]bool{
	"ac": true, "co": true, "com": true, "edu": true, "gob": true, "gov": true, "go": true, "in": true,
	"ltd": true, "mil": true, "ne": true, "net": true, "nic": true, "or": true, "org": true, "plc": true, "sch": true,
}
