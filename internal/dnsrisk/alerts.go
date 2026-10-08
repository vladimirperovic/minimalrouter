package dnsrisk

import (
	"database/sql"
	"errors"
	"time"
)

type Alert struct {
	ID             int64  `json:"id"`
	Domain         string `json:"domain"`
	Category       string `json:"category"`
	Severity       string `json:"severity"`
	FirstSeen      int64  `json:"first_seen"`
	LastSeen       int64  `json:"last_seen"`
	Lookups        uint64 `json:"lookups"`
	LastAddress    string `json:"last_address"`
	AcknowledgedAt int64  `json:"acknowledged_at"`
	Ignored        bool   `json:"ignored"`
}

type AlertPage struct {
	Alerts []Alert `json:"alerts"`
	Total  int     `json:"total"`
	Offset int     `json:"offset"`
	Limit  int     `json:"limit"`
}

type Exception struct {
	ID       int64  `json:"id"`
	Domain   string `json:"domain"`
	Category string `json:"category"`
}

func (s *Service) Alerts(view, category string, offset, limit int) (AlertPage, error) {
	out := AlertPage{Alerts: []Alert{}, Offset: offset, Limit: limit}
	enabled, _, err := s.settings()
	if err != nil {
		return out, err
	}
	if !enabled {
		return out, nil
	}
	where := "1=1"
	args := []any{}
	if view == "new" {
		where += " AND acknowledged_at=0 AND NOT EXISTS(SELECT 1 FROM exceptions e WHERE e.domain=a.domain AND e.category=a.category)"
	}
	if category != "" {
		where += " AND category=?"
		args = append(args, category)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM alerts a WHERE "+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := s.db.Query(`SELECT id,domain,category,severity,first_seen,last_seen,lookups,last_address,acknowledged_at,EXISTS(SELECT 1 FROM exceptions e WHERE e.domain=a.domain AND e.category=a.category) FROM alerts a WHERE `+where+` ORDER BY last_seen DESC,id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.ID, &a.Domain, &a.Category, &a.Severity, &a.FirstSeen, &a.LastSeen, &a.Lookups, &a.LastAddress, &a.AcknowledgedAt, &a.Ignored); err != nil {
			return out, err
		}
		out.Alerts = append(out.Alerts, a)
	}
	return out, rows.Err()
}

func (s *Service) Acknowledge(id int64) error {
	r, err := s.db.Exec("UPDATE alerts SET acknowledged_at=? WHERE id=?", time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Service) Ignore(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var domain, category string
	if err := tx.QueryRow("SELECT domain,category FROM alerts WHERE id=?", id).Scan(&domain, &category); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM exceptions WHERE domain<>? OR category<>?", domain, category).Scan(&n); err != nil {
		return err
	}
	if n >= 500 {
		return errors.New("exception limit reached")
	}
	if _, err := tx.Exec("INSERT OR IGNORE INTO exceptions(domain,category) VALUES(?,?)", domain, category); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) Exceptions() ([]Exception, error) {
	out := []Exception{}
	rows, err := s.db.Query("SELECT id,domain,category FROM exceptions ORDER BY domain,category")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Exception
		if err := rows.Scan(&e.ID, &e.Domain, &e.Category); err != nil {
			return out, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) RemoveException(id int64) error {
	r, err := s.db.Exec("DELETE FROM exceptions WHERE id=?", id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
