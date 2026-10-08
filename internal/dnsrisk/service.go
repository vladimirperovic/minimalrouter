package dnsrisk

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

const maxAlerts = 10000

type Lookup struct {
	Name, Address string
	Count         uint32
	At            int64
}
type batch struct {
	lookups    []Lookup
	generation uint64
}
type match struct{ domain, category, severity string }

type Service struct {
	dir          string
	db           *sql.DB
	client       *http.Client
	settings     func() (bool, int, error)
	allowWrite   func() bool
	feedMu       sync.RWMutex
	feeds        []feed
	historyMu    sync.Mutex // serializes clear/disable with classification and commit
	queue        chan batch
	refresh      chan struct{}
	generation   atomic.Uint64
	dropped      atomic.Uint64
	mu           sync.Mutex
	lastChecked  int64
	processError string
	lastRefresh  time.Time
}

type Summary struct {
	Available      bool         `json:"available"`
	Enabled        bool         `json:"enabled"`
	NewCount       int          `json:"new_count"`
	Total          int          `json:"total"`
	LastCheckedAt  int64        `json:"last_checked_at"`
	DroppedLookups uint64       `json:"dropped_lookups"`
	RemovedAlerts  int64        `json:"removed_alerts"`
	Error          string       `json:"error,omitempty"`
	Sources        []FeedStatus `json:"sources"`
}

func Open(dataDir string, settings func() (bool, int, error), allowWrite func() bool) (*Service, error) {
	dir := filepath.Join(dataDir, "dns-risk")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "alerts.db")+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=trusted_schema(OFF)&_pragma=secure_delete(ON)&_pragma=busy_timeout(3000)&_pragma=cache_size(-1024)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`
	 CREATE TABLE IF NOT EXISTS feeds(category TEXT PRIMARY KEY,slot INTEGER NOT NULL,entries INTEGER NOT NULL,updated_at INTEGER NOT NULL,attempted_at INTEGER NOT NULL,etag TEXT NOT NULL,error TEXT NOT NULL);
	 CREATE TABLE IF NOT EXISTS alerts(id INTEGER PRIMARY KEY AUTOINCREMENT,day INTEGER NOT NULL,domain TEXT NOT NULL,category TEXT NOT NULL,severity TEXT NOT NULL,first_seen INTEGER NOT NULL,last_seen INTEGER NOT NULL,lookups INTEGER NOT NULL,last_address TEXT NOT NULL,acknowledged_at INTEGER NOT NULL DEFAULT 0,UNIQUE(day,domain,category));
	 CREATE INDEX IF NOT EXISTS alerts_recent ON alerts(last_seen DESC,id DESC);
	 CREATE TABLE IF NOT EXISTS exceptions(id INTEGER PRIMARY KEY,domain TEXT NOT NULL,category TEXT NOT NULL,UNIQUE(domain,category));
	 CREATE TABLE IF NOT EXISTS state(id INTEGER PRIMARY KEY CHECK(id=1),removed_alerts INTEGER NOT NULL DEFAULT 0);
	 INSERT OR IGNORE INTO state(id) VALUES(1);
	`); err != nil {
		db.Close()
		return nil, err
	}
	if allowWrite == nil {
		allowWrite = func() bool { return true }
	}
	s := &Service{dir: dir, db: db, client: feedHTTPClient(), settings: settings, allowWrite: allowWrite, queue: make(chan batch, 8), refresh: make(chan struct{}, 1)}
	for _, src := range sources {
		f := feed{source: src, status: FeedStatus{Category: src.ID, Label: src.Label, Source: "Block List Project", URL: sourceURL(src)}}
		err := db.QueryRow("SELECT slot,entries,updated_at,attempted_at,etag,error FROM feeds WHERE category=?", src.ID).Scan(&f.slot, &f.status.Entries, &f.status.UpdatedAt, &f.status.AttemptedAt, &f.etag, &f.status.Error)
		if err != nil && err != sql.ErrNoRows {
			s.Close()
			return nil, err
		}
		if f.status.Entries > 0 && (f.slot == 0 || f.slot == 1) {
			f.db, err = openFeed(feedPath(dir, src.ID, f.slot))
			if err != nil {
				f.status.Entries = 0
				f.status.UpdatedAt = 0
				f.status.AttemptedAt = 0
				f.status.Error = "Saved category list is unavailable; waiting for download"
				f.etag = ""
			}
		} else {
			f.slot = 0
		}
		s.feeds = append(s.feeds, f)
	}
	return s, nil
}

// Close is called only after Run and all producers have stopped.
func (s *Service) Close() error {
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	for _, f := range s.feeds {
		if f.db != nil {
			f.db.Close()
		}
	}
	return s.db.Close()
}

// Submit never holds up DNS accounting or the helper. Backpressure is visible.
func (s *Service) Submit(lookups []Lookup) {
	if len(lookups) == 0 {
		return
	}
	if len(lookups) > 4096 {
		for _, l := range lookups[4096:] {
			s.dropped.Add(uint64(l.Count))
		}
		lookups = lookups[:4096]
	}
	b := batch{lookups: append([]Lookup(nil), lookups...), generation: s.generation.Load()}
	select {
	case s.queue <- b:
	default:
		for _, l := range lookups {
			s.dropped.Add(uint64(l.Count))
		}
	}
}

func (s *Service) Run(ctx context.Context) {
	updateDone := make(chan struct{})
	go func() {
		defer close(updateDone)
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		s.updateFeeds(ctx, false)
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				s.updateFeeds(ctx, false)
			case <-s.refresh:
				s.updateFeeds(ctx, true)
			}
		}
	}()
	defer func() { <-updateDone }()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-s.queue:
			if err := s.process(ctx, b); err != nil {
				s.mu.Lock()
				s.processError = "DNS risk checks failed; coverage is incomplete"
				s.mu.Unlock()
			}
		case <-tick.C:
			enabled, days, err := s.settings()
			if err != nil {
				s.mu.Lock()
				s.processError = "DNS risk settings could not be read"
				s.mu.Unlock()
				continue
			}
			if !enabled {
				err = s.Clear()
			} else {
				err = s.prune(time.Now(), days)
			}
			if err != nil {
				s.mu.Lock()
				s.processError = "DNS risk history maintenance failed"
				s.mu.Unlock()
			}
		}
	}
}

func (s *Service) RequestRefresh() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.lastRefresh) < 5*time.Minute {
		return false
	}
	s.lastRefresh = time.Now()
	select {
	case s.refresh <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Service) process(ctx context.Context, b batch) (resultErr error) {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	// Failed batches must remain visible after a later successful check clears
	// the transient error. Some lookups can have been checked only partially.
	incomplete := false
	defer func() {
		if resultErr != nil || incomplete {
			for _, l := range b.lookups {
				s.dropped.Add(uint64(l.Count))
			}
		}
	}()
	if b.generation != s.generation.Load() {
		return nil
	}
	enabled, days, err := s.settings()
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	if !s.allowWrite() {
		return errors.New("storage pressure")
	}
	cache := map[string][]match{}
	s.feedMu.RLock()
	defer s.feedMu.RUnlock()
	loaded := 0
	for _, f := range s.feeds {
		if f.db != nil {
			loaded++
		}
	}
	if loaded == 0 {
		return errors.New("no category lists loaded")
	}
	incomplete = loaded != len(s.feeds)
	// Match full query names before the coarse site aggregation loses subdomains.
	for _, l := range b.lookups {
		if _, ok := cache[l.Name]; ok {
			continue
		}
		names := candidates(l.Name)
		cache[l.Name] = nil
		if len(names) == 0 {
			continue
		}
		args := make([]any, len(names))
		for i, n := range names {
			args[i] = n
		}
		query := "SELECT domain FROM domains WHERE domain IN (" + strings.TrimSuffix(strings.Repeat("?,", len(names)), ",") + ") ORDER BY length(domain) DESC LIMIT 1"
		for _, f := range s.feeds {
			if f.db == nil {
				continue
			}
			var domain string
			err := f.db.QueryRowContext(ctx, query, args...).Scan(&domain)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return err
			}
			cache[l.Name] = append(cache[l.Name], match{domain, f.source.ID, f.source.Severity})
		}
	}
	// Disabling can race with the slow matching phase. Never commit after it.
	enabled, _, err = s.settings()
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	insert, err := tx.PrepareContext(ctx, `INSERT INTO alerts(day,domain,category,severity,first_seen,last_seen,lookups,last_address) VALUES(?,?,?,?,?,?,?,?)
	 ON CONFLICT(day,domain,category) DO UPDATE SET first_seen=MIN(first_seen,excluded.first_seen),last_address=CASE WHEN excluded.last_seen>=last_seen THEN excluded.last_address ELSE last_address END,last_seen=MAX(last_seen,excluded.last_seen),lookups=lookups+excluded.lookups`)
	if err != nil {
		return err
	}
	defer insert.Close()
	now := time.Now()
	for _, l := range b.lookups {
		at := l.At
		if at <= 0 || at > now.Unix()+300 {
			at = now.Unix()
		}
		if l.Count == 0 {
			continue
		}
		for _, m := range cache[l.Name] {
			if _, err := insert.ExecContext(ctx, time.Unix(at, 0).UTC().Truncate(24*time.Hour).Unix(), m.domain, m.category, m.severity, at, at, l.Count, l.Address); err != nil {
				return err
			}
		}
	}
	if err := trimAlerts(tx, now, days); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.mu.Lock()
	s.lastChecked = now.Unix()
	s.processError = ""
	s.mu.Unlock()
	return nil
}

func trimAlerts(tx *sql.Tx, now time.Time, days int) error {
	if days < 1 || days > 90 {
		days = 30
	}
	cutoff := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -days+1).Unix()
	if _, err := tx.Exec("DELETE FROM alerts WHERE day<? OR day>?", cutoff, now.Unix()+300); err != nil {
		return err
	}
	r, err := tx.Exec("DELETE FROM alerts WHERE id IN (SELECT id FROM alerts ORDER BY last_seen DESC,id DESC LIMIT -1 OFFSET ?)", maxAlerts)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	_, err = tx.Exec("UPDATE state SET removed_alerts=removed_alerts+? WHERE id=1", n)
	return err
}

func (s *Service) prune(now time.Time, days int) error {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := trimAlerts(tx, now, days); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) Clear() error {
	s.historyMu.Lock()
	defer s.historyMu.Unlock()
	s.generation.Add(1)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM alerts; UPDATE state SET removed_alerts=0 WHERE id=1"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.dropped.Store(0)
	s.mu.Lock()
	s.lastChecked = 0
	s.processError = ""
	s.mu.Unlock()
	return nil
}

func (s *Service) Summary(now time.Time) (Summary, error) {
	enabled, _, err := s.settings()
	if err != nil {
		return Summary{}, err
	}
	out := Summary{Available: true, Enabled: enabled, Sources: []FeedStatus{}}
	s.feedMu.RLock()
	for _, f := range s.feeds {
		status := f.status
		status.Stale = f.db == nil || now.Sub(time.Unix(status.UpdatedAt, 0)) > 48*time.Hour
		out.Sources = append(out.Sources, status)
	}
	s.feedMu.RUnlock()
	if !enabled {
		return out, nil
	}
	s.mu.Lock()
	out.LastCheckedAt = s.lastChecked
	out.Error = s.processError
	s.mu.Unlock()
	out.DroppedLookups = s.dropped.Load()
	if err := s.db.QueryRow(`SELECT count(*),COALESCE(SUM(CASE WHEN acknowledged_at=0 AND NOT EXISTS(SELECT 1 FROM exceptions e WHERE e.domain=a.domain AND e.category=a.category) THEN 1 ELSE 0 END),0) FROM alerts a`).Scan(&out.Total, &out.NewCount); err != nil {
		return out, err
	}
	err = s.db.QueryRow("SELECT removed_alerts FROM state WHERE id=1").Scan(&out.RemovedAlerts)
	return out, err
}

func validCategory(category string) bool {
	for _, src := range sources {
		if src.ID == category {
			return true
		}
	}
	return false
}
func ValidateCategory(category string) bool { return category == "" || validCategory(category) }
