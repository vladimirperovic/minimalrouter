// Package dnsrisk classifies observed DNS names against public category lists.
// It never changes DNS answers, firewall policy, or the privileged helper.
package dnsrisk

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

const (
	maxFeedBytes = 128 << 20
	maxFeedRows  = 4000000
	feedInterval = 24 * time.Hour
	feedRetry    = 6 * time.Hour
)

type source struct{ ID, Label, List, Severity string }

var sources = []source{
	{"adult", "Adult", "porn", "warning"},
	{"phishing", "Phishing", "phishing", "high"},
	{"malware", "Malware", "malware", "high"},
	{"fraud", "Fraud", "fraud", "high"},
	{"gambling", "Gambling", "gambling", "warning"},
}

type FeedStatus struct {
	Category    string `json:"category"`
	Label       string `json:"label"`
	Source      string `json:"source"`
	URL         string `json:"url"`
	Entries     int64  `json:"entries"`
	UpdatedAt   int64  `json:"updated_at"`
	AttemptedAt int64  `json:"attempted_at"`
	Error       string `json:"error,omitempty"`
	Stale       bool   `json:"stale"`
	Updating    bool   `json:"updating"`
}

type feed struct {
	source source
	db     *sql.DB
	slot   int
	etag   string
	status FeedStatus
}

func sourceURL(src source) string {
	return "https://blocklistproject.github.io/Lists/alt-version/" + src.List + "-nl.txt"
}

func feedHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		// URLs are fixed by the program, never supplied by an API request.
		if len(via) >= 3 || req.URL.Scheme != "https" || req.URL.Host != "blocklistproject.github.io" {
			return errors.New("category source redirect refused")
		}
		return nil
	}}
}

func normalizeDomain(raw string) (string, bool) {
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if len(name) > 253 || !strings.Contains(name, ".") {
		return "", false
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return "", false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", false
			}
		}
	}
	suffix, _ := publicsuffix.PublicSuffix(name)
	if suffix == name || strings.HasSuffix(name, ".local") || strings.HasSuffix(name, ".lan") || strings.HasSuffix(name, ".arpa") {
		return "", false
	}
	return name, true
}

// Stop at the registrable domain, including private suffixes (github.io etc.).
// A malicious tenant must not taint all other tenants on a shared platform.
func candidates(name string) []string {
	name, ok := normalizeDomain(name)
	if !ok {
		return nil
	}
	base, err := publicsuffix.EffectiveTLDPlusOne(name)
	if err != nil {
		return nil
	}
	out := []string{name}
	for name != base {
		_, name, _ = strings.Cut(name, ".")
		out = append(out, name)
	}
	return out
}

func feedPath(dir, id string, slot int) string {
	return filepath.Join(dir, fmt.Sprintf("%s-%d.db", id, slot))
}

func openFeed(path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?mode=ro&_pragma=trusted_schema(OFF)&_pragma=cache_size(-512)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	var n int
	if err = db.QueryRow("SELECT count(*) FROM domains").Scan(&n); err != nil || n == 0 {
		db.Close()
		return nil, errors.New("category index unavailable")
	}
	return db, nil
}

// Build a new disk index while the previous index remains available. No feed
// is loaded into a giant Go map, and a partial/HTML/empty response is rejected.
func buildFeed(ctx context.Context, path string, body io.Reader, allowWrite func() bool) (int64, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=page_size(4096)&_pragma=max_page_count(65536)&_pragma=journal_mode(OFF)&_pragma=synchronous(OFF)&_pragma=cache_size(-2048)&_pragma=trusted_schema(OFF)")
	if err != nil {
		return 0, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("CREATE TABLE domains(domain TEXT PRIMARY KEY) WITHOUT ROWID"); err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	insert, err := tx.PrepareContext(ctx, "INSERT OR IGNORE INTO domains(domain) VALUES (?)")
	if err != nil {
		return 0, err
	}
	defer insert.Close()
	limited := &io.LimitedReader{R: body, N: maxFeedBytes + 1}
	scan := bufio.NewScanner(limited)
	scan.Buffer(make([]byte, 4096), 4096)
	var total, bad, count int64
	for scan.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scan.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		total++
		if total%4096 == 0 && allowWrite != nil && !allowWrite() {
			return 0, errors.New("category update paused by storage pressure")
		}
		if strings.ContainsAny(line, "<>\x00") {
			return 0, errors.New("category download is not a domain list")
		}
		name, ok := normalizeDomain(line)
		if !ok {
			bad++
			continue
		}
		result, err := insert.ExecContext(ctx, name)
		if err != nil {
			return 0, err
		}
		n, _ := result.RowsAffected()
		count += n
		if count > maxFeedRows {
			return 0, errors.New("category list exceeds entry limit")
		}
	}
	if err := scan.Err(); err != nil {
		return 0, err
	}
	if limited.N <= 0 {
		return 0, errors.New("category list exceeds download limit")
	}
	if count == 0 || bad*100 > total {
		return 0, fmt.Errorf("empty or invalid category list (%d rejected of %d entries)", bad, total)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	// The inactive file is now durable before publishing its slot in metadata.
	if err := db.Close(); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return count, f.Sync()
}

func (s *Service) refreshFeed(ctx context.Context, index int, now time.Time) error {
	s.feedMu.RLock()
	f := s.feeds[index]
	s.feedMu.RUnlock()
	slot := 1 - f.slot
	if !s.allowWrite() {
		return errors.New("category update paused by storage pressure")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL(f.source), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "MinimalRouter-DNS-Risk/0.2")
	if f.db != nil && f.etag != "" {
		req.Header.Set("If-None-Match", f.etag)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return errors.New("category download failed; previous list retained")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified && f.db != nil {
		s.feedMu.Lock()
		defer s.feedMu.Unlock()
		if _, err := s.db.ExecContext(ctx, "UPDATE feeds SET updated_at=?, error='' WHERE category=?", now.Unix(), f.source.ID); err != nil {
			return err
		}
		s.feeds[index].status.UpdatedAt = now.Unix()
		s.feeds[index].status.Error = ""
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("category download returned HTTP %d; previous list retained", resp.StatusCode)
	}
	if resp.ContentLength > maxFeedBytes {
		return errors.New("category download exceeds size limit")
	}
	path := feedPath(s.dir, f.source.ID, slot)
	// Only the inactive, fixed-name slot is disposable. Never remove the
	// active index when validation, cancellation or storage checks fail.
	published := false
	defer func() {
		if !published {
			_ = os.Remove(path)
		}
	}()
	count, err := buildFeed(ctx, path, resp.Body, s.allowWrite)
	if err != nil {
		return fmt.Errorf("category index rejected: %w", err)
	}
	// Refuse catastrophic upstream truncation; operators retain a working list.
	if f.status.Entries > 100 && count < f.status.Entries/2 {
		return errors.New("category list unexpectedly shrank; previous list retained")
	}
	if !s.allowWrite() {
		return errors.New("category update paused by storage pressure")
	}
	next, err := openFeed(path)
	if err != nil {
		return err
	}
	etag := resp.Header.Get("ETag")
	if len(etag) > 256 {
		etag = ""
	}
	s.feedMu.Lock()
	defer s.feedMu.Unlock()
	_, err = s.db.ExecContext(ctx, `INSERT INTO feeds(category,slot,entries,updated_at,attempted_at,etag,error) VALUES(?,?,?,?,?,?,'')
	 ON CONFLICT(category) DO UPDATE SET slot=excluded.slot,entries=excluded.entries,updated_at=excluded.updated_at,attempted_at=excluded.attempted_at,etag=excluded.etag,error=''`, f.source.ID, slot, count, now.Unix(), now.Unix(), etag)
	if err != nil {
		next.Close()
		return err
	}
	if old := s.feeds[index].db; old != nil {
		old.Close()
	}
	s.feeds[index].db, s.feeds[index].slot, s.feeds[index].etag = next, slot, etag
	s.feeds[index].status.Entries, s.feeds[index].status.UpdatedAt, s.feeds[index].status.Error = count, now.Unix(), ""
	published = true
	return nil
}

func (s *Service) updateFeeds(ctx context.Context, force bool) {
	for i := range s.feeds {
		enabled, _, err := s.settings()
		if err != nil || !enabled || ctx.Err() != nil {
			return
		}
		now := time.Now()
		s.feedMu.Lock()
		status := s.feeds[i].status
		due := status.UpdatedAt == 0 || now.Sub(time.Unix(status.UpdatedAt, 0)) >= feedInterval
		retry := status.AttemptedAt == 0 || now.Sub(time.Unix(status.AttemptedAt, 0)) >= feedRetry
		if !force && (!due || !retry) {
			s.feedMu.Unlock()
			continue
		}
		s.feeds[i].status.Updating = true
		s.feeds[i].status.AttemptedAt = now.Unix()
		s.feedMu.Unlock()
		err = s.refreshFeed(ctx, i, now)
		s.feedMu.Lock()
		s.feeds[i].status.Updating = false
		if err != nil {
			s.feeds[i].status.Error = "Category update failed; previous list retained. " + err.Error()
		}
		message := s.feeds[i].status.Error
		s.feedMu.Unlock()
		// Persist attempts even for sources which have never loaded successfully.
		if s.allowWrite() {
			_, _ = s.db.ExecContext(ctx, `INSERT INTO feeds(category,slot,entries,updated_at,attempted_at,etag,error) VALUES(?,0,0,0,?,'',?)
		 ON CONFLICT(category) DO UPDATE SET attempted_at=excluded.attempted_at,error=excluded.error`, s.feeds[i].source.ID, now.Unix(), message)
		}
	}
}
