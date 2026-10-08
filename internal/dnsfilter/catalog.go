package dnsfilter

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Catalog struct {
	Category  string `json:"category"`
	Label     string `json:"label"`
	URL       string `json:"url"`
	Hash      string `json:"hash"`
	Entries   int    `json:"entries"`
	UpdatedAt int64  `json:"updated_at"`
	Error     string `json:"error,omitempty"`
}

func catalogPath(dir, id string, slot int) string {
	return filepath.Join(dir, fmt.Sprintf("%s-%d.db", id, slot))
}

func openCatalog(path string, readonly bool) (*sql.DB, error) {
	options := "?_pragma=trusted_schema(OFF)&_pragma=cache_size(-512)&_pragma=max_page_count(16384)&_pragma=busy_timeout(1000)"
	if readonly {
		options += "&mode=ro"
	} else {
		options += "&_pragma=journal_mode(OFF)&_pragma=synchronous(OFF)"
	}
	db, err := sql.Open("sqlite", path+options)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func readCatalog(path string, source Source) (Catalog, error) {
	meta := Catalog{Category: source.ID, Label: source.Label, URL: source.URL()}
	db, err := openCatalog(path, true)
	if err != nil {
		return meta, err
	}
	defer db.Close()
	err = db.QueryRow("SELECT hash,entries,updated_at FROM metadata").Scan(&meta.Hash, &meta.Entries, &meta.UpdatedAt)
	if err == nil && (meta.Entries < 1 || meta.Entries > MaxFeedDomains || len(meta.Hash) != 64) {
		err = errors.New("invalid catalog metadata")
	}
	return meta, err
}

func buildCatalog(ctx context.Context, path string, source Source, body io.Reader, allowWrite func() bool) (Catalog, error) {
	meta := Catalog{Category: source.ID, Label: source.Label, URL: source.URL()}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return meta, err
	}
	db, err := openCatalog(path, false)
	if err != nil {
		return meta, err
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE domains(domain TEXT PRIMARY KEY) WITHOUT ROWID; CREATE TABLE metadata(hash TEXT, entries INTEGER, updated_at INTEGER)"); err != nil {
		return meta, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return meta, err
	}
	defer tx.Rollback()
	insert, err := tx.PrepareContext(ctx, "INSERT OR IGNORE INTO domains(domain) VALUES (?)")
	if err != nil {
		return meta, err
	}
	defer insert.Close()
	limited := &io.LimitedReader{R: body, N: MaxFeedBytes + 1}
	scan := bufio.NewScanner(limited)
	scan.Buffer(make([]byte, 4096), 4096)
	bad, total := 0, 0
	for scan.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scan.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		total++
		if total > MaxFeedDomains*2 {
			return meta, errors.New("list exceeds row limit")
		}
		if total%2048 == 0 && allowWrite != nil && !allowWrite() {
			return meta, errors.New("list refresh paused by storage pressure")
		}
		d, ok := Domain(line)
		if !ok {
			bad++
			continue
		}
		result, err := insert.ExecContext(ctx, d)
		if err != nil {
			return meta, err
		}
		n, _ := result.RowsAffected()
		meta.Entries += int(n)
		if meta.Entries > MaxFeedDomains {
			return meta, errors.New("list exceeds appliance domain limit")
		}
	}
	if scan.Err() != nil {
		return meta, scan.Err()
	}
	if limited.N <= 0 {
		return meta, errors.New("list exceeds download limit")
	}
	if meta.Entries < 1 || bad*100 > total {
		return meta, errors.New("empty or invalid domain list")
	}
	rows, err := tx.QueryContext(ctx, "SELECT domain FROM domains ORDER BY domain")
	if err != nil {
		return meta, err
	}
	hash := sha256.New()
	for rows.Next() {
		var d string
		if err = rows.Scan(&d); err != nil {
			break
		}
		fmt.Fprintln(hash, d)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return meta, err
	}
	if rowErr != nil {
		return meta, rowErr
	}
	meta.Hash = hex.EncodeToString(hash.Sum(nil))
	meta.UpdatedAt = time.Now().Unix()
	if _, err = tx.Exec("INSERT INTO metadata VALUES (?,?,?)", meta.Hash, meta.Entries, meta.UpdatedAt); err != nil {
		return meta, err
	}
	if err = tx.Commit(); err != nil {
		return meta, err
	}
	if err = db.Close(); err != nil {
		return meta, err
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return meta, err
	}
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return meta, err
	}
	return meta, f.Sync()
}

func downloadCatalog(ctx context.Context, path string, source Source, previous Catalog, allowWrite func() bool) (Catalog, error) {
	client := &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != "https" || req.URL.Host != "cdn.jsdelivr.net" {
			return errors.New("list redirect refused")
		}
		return nil
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL(), nil)
	if err != nil {
		return Catalog{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return Catalog{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Catalog{}, fmt.Errorf("list download returned HTTP %d", response.StatusCode)
	}
	meta, err := buildCatalog(ctx, path, source, response.Body, allowWrite)
	if err == nil && previous.Entries > 0 && meta.Entries < previous.Entries/2 {
		err = errors.New("list shrank by more than half; previous list retained")
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return meta, err
}

func emitCatalog(ctx context.Context, path string, w io.Writer) error {
	db, err := openCatalog(path, true)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT domain FROM domains ORDER BY domain")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var domain string
		if err = rows.Scan(&domain); err != nil {
			return err
		}
		if _, err = fmt.Fprintln(w, domain); err != nil {
			return err
		}
	}
	return rows.Err()
}

func catalogMatch(ctx context.Context, path, name string) (string, error) {
	db, err := openCatalog(path, true)
	if err != nil {
		return "", err
	}
	defer db.Close()
	for candidate := name; strings.Contains(candidate, "."); candidate = candidate[strings.IndexByte(candidate, '.')+1:] {
		var found string
		err = db.QueryRowContext(ctx, "SELECT domain FROM domains WHERE domain=?", candidate).Scan(&found)
		if err == nil {
			return found, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	return "", nil
}
