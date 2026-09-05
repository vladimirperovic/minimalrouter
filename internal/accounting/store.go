package accounting

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store persists monthly per-device totals plus the last raw kernel counter for
// each host, so a routerd restart cannot double-count traffic that was already
// recorded.
type Store struct {
	db *sql.DB
}

const (
	// defaultRetentionMonths keeps just over a year so a "same month last year"
	// comparison is possible without unbounded growth.
	defaultRetentionMonths = 13
	// maxTrackedDevices bounds the table against a hostile or misconfigured LAN.
	// It matches the nftables set size so neither side can outgrow the other.
	maxTrackedDevices = 512
)

func OpenStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("create accounting data directory: %w", err)
	}
	path := filepath.Join(dataDir, "accounting.db")
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=trusted_schema(OFF)&_pragma=secure_delete(ON)&_pragma=busy_timeout(3000)&_pragma=cache_size(-1000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open accounting store: %w", err)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(2 * time.Minute)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize accounting store: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		db.Close()
		return nil, fmt.Errorf("secure accounting store: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS device_month (
		address TEXT NOT NULL,
		month TEXT NOT NULL,
		rx_bytes INTEGER NOT NULL DEFAULT 0,
		tx_bytes INTEGER NOT NULL DEFAULT 0,
		last_seen INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (address, month)
	);
	CREATE INDEX IF NOT EXISTS idx_device_month_month ON device_month(month);
	-- The last raw kernel counter per host and direction. Without this a routerd
	-- restart would see the whole kernel counter as a fresh delta.
	CREATE TABLE IF NOT EXISTS device_cursor (
		address TEXT NOT NULL,
		direction TEXT NOT NULL CHECK(direction IN ('rx','tx')),
		last_raw INTEGER NOT NULL DEFAULT 0,
		-- The counter generation the cursor was taken in. Comparing raw values
		-- alone cannot see a table reload that already climbed past the previous
		-- reading, which silently dropped a full generation of traffic.
		generation INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (address, direction)
	);
	-- Whether the operator's decision to disable accounting has been carried
	-- out. A process flag could not survive the restart that follows a disable,
	-- which left history on disk that was promised to be deleted.
	CREATE TABLE IF NOT EXISTS accounting_state (
		id INTEGER PRIMARY KEY CHECK(id = 1),
		history_cleared INTEGER NOT NULL DEFAULT 0
	);
	`)
	if err != nil {
		return fmt.Errorf("migrate accounting store: %w", err)
	}
	// Older databases predate the generation column; adding it is idempotent
	// because a duplicate-column error simply means the migration already ran.
	if _, err := db.Exec(`ALTER TABLE device_cursor ADD COLUMN generation INTEGER NOT NULL DEFAULT 0`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column name") {
		return fmt.Errorf("migrate accounting cursors: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Record folds one collection round into the monthly buckets. rx and tx are the
// current raw kernel counters keyed by host address, and generation identifies
// the counter set they were read from — the canonical configuration revision,
// which changes exactly when the nftables table (and with it every counter) is
// recreated.
func (s *Store) Record(now time.Time, rx, tx []Counter, retentionMonths int, generation uint64) error {
	if s == nil || s.db == nil {
		return nil
	}
	if retentionMonths <= 0 {
		retentionMonths = defaultRetentionMonths
	}
	month := MonthKey(now)
	tx2, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx2.Rollback()

	apply := func(direction string, counters []Counter) error {
		for _, counter := range counters {
			var previous, previousGeneration uint64
			row := tx2.QueryRow(`SELECT last_raw, generation FROM device_cursor WHERE address = ? AND direction = ?`,
				counter.Address, direction)
			switch err := row.Scan(&previous, &previousGeneration); err {
			case nil:
			case sql.ErrNoRows:
				previous = 0
				previousGeneration = generation
			default:
				return err
			}
			delta := DeltaAcrossGenerations(previousGeneration, generation, previous, counter.Bytes)
			if _, err := tx2.Exec(`INSERT INTO device_cursor(address, direction, last_raw, generation)
				VALUES (?, ?, ?, ?) ON CONFLICT(address, direction) DO UPDATE SET
					last_raw = excluded.last_raw, generation = excluded.generation`,
				counter.Address, direction, counter.Bytes, generation); err != nil {
				return err
			}
			if delta == 0 {
				continue
			}
			column := "rx_bytes"
			if direction == "tx" {
				column = "tx_bytes"
			}
			if _, err := tx2.Exec(fmt.Sprintf(`INSERT INTO device_month(address, month, %s, last_seen)
				VALUES (?, ?, ?, ?) ON CONFLICT(address, month) DO UPDATE SET
					%s = %s + excluded.%s, last_seen = excluded.last_seen`,
				column, column, column, column),
				counter.Address, month, delta, now.UTC().Unix()); err != nil {
				return err
			}
		}
		return nil
	}
	if err := apply("rx", rx); err != nil {
		return fmt.Errorf("record download counters: %w", err)
	}
	if err := apply("tx", tx); err != nil {
		return fmt.Errorf("record upload counters: %w", err)
	}

	cutoff := now.UTC().AddDate(0, -retentionMonths, 0).Format("2006-01")
	if _, err := tx2.Exec(`DELETE FROM device_month WHERE month < ?`, cutoff); err != nil {
		return fmt.Errorf("prune accounting history: %w", err)
	}
	if _, err := tx2.Exec(`DELETE FROM device_cursor WHERE address NOT IN
		(SELECT address FROM device_month)`); err != nil {
		return fmt.Errorf("prune accounting cursors: %w", err)
	}
	if _, err := tx2.Exec(`DELETE FROM device_month WHERE rowid NOT IN
		(SELECT rowid FROM device_month ORDER BY month DESC, rx_bytes + tx_bytes DESC LIMIT ?)`,
		maxTrackedDevices*retentionMonths); err != nil {
		return fmt.Errorf("bound accounting rows: %w", err)
	}
	return tx2.Commit()
}

// Months returns the newest `limit` calendar months, each with its devices
// sorted by total traffic descending.
func (s *Store) Months(limit int) ([]MonthUsage, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 3
	}
	rows, err := s.db.Query(`SELECT address, month, rx_bytes, tx_bytes, last_seen
		FROM device_month WHERE month IN
		(SELECT DISTINCT month FROM device_month ORDER BY month DESC LIMIT ?)
		ORDER BY month DESC`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byMonth := map[string][]DeviceUsage{}
	var order []string
	for rows.Next() {
		var (
			usage    DeviceUsage
			month    string
			lastSeen int64
		)
		if err := rows.Scan(&usage.Address, &month, &usage.RXBytes, &usage.TXBytes, &lastSeen); err != nil {
			return nil, err
		}
		usage.TotalBytes = usage.RXBytes + usage.TXBytes
		usage.LastSeenUTC = lastSeen
		if _, seen := byMonth[month]; !seen {
			order = append(order, month)
		}
		byMonth[month] = append(byMonth[month], usage)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]MonthUsage, 0, len(order))
	for _, month := range order {
		devices := byMonth[month]
		sort.Slice(devices, func(i, j int) bool { return devices[i].TotalBytes > devices[j].TotalBytes })
		var total uint64
		for _, device := range devices {
			total += device.TotalBytes
		}
		out = append(out, MonthUsage{Month: month, TotalBytes: total, Devices: devices})
	}
	return out, nil
}

// Reset clears all accounting history. Used when the operator disables the
// feature so no stale per-device data lingers on disk.
func (s *Store) Reset() error {
	if s == nil || s.db == nil {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM device_month`); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM device_cursor`)
	return err
}

// ClearHistory deletes every per-device record and durably marks the
// operator's disable as carried out, in one transaction. The flag is what
// makes the promise survive a restart: a process-local "was enabled" flag
// starts false in a new process, so a disable followed by a restart before the
// next collection tick used to leave the history on disk forever. Because the
// flag is only written when the deletion commits, a failed clear is naturally
// retried on the next tick.
func (s *Store) ClearHistory() error {
	if s == nil || s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM device_month`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM device_cursor`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO accounting_state(id, history_cleared) VALUES (1, 1)
		ON CONFLICT(id) DO UPDATE SET history_cleared = 1`); err != nil {
		return err
	}
	return tx.Commit()
}

// HistoryCleared reports whether a disable has already been honored, so a
// restarted collector does not have to guess.
func (s *Store) HistoryCleared() (bool, error) {
	if s == nil || s.db == nil {
		return true, nil
	}
	var cleared int
	switch err := s.db.QueryRow(`SELECT history_cleared FROM accounting_state WHERE id = 1`).Scan(&cleared); err {
	case nil:
		return cleared != 0, nil
	case sql.ErrNoRows:
		// A store that has never recorded anything has nothing to clear, but
		// one carrying rows from a previous release predates the flag.
		var rows int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM device_month`).Scan(&rows); err != nil {
			return false, err
		}
		return rows == 0, nil
	default:
		return false, err
	}
}

// MarkHistoryLive records that accounting is measuring again, so a later
// disable is treated as a fresh instruction to delete.
func (s *Store) MarkHistoryLive() error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO accounting_state(id, history_cleared) VALUES (1, 0)
		ON CONFLICT(id) DO UPDATE SET history_cleared = 0`)
	return err
}
