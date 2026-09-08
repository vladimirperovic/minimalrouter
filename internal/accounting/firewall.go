package accounting

import (
	"context"
	"database/sql"
	"time"
)

type FirewallPoint struct {
	Start   time.Time `json:"start"`
	Allowed uint64    `json:"allowed"`
	Blocked uint64    `json:"blocked"`
	Samples int       `json:"samples"`
}
type FirewallActivity struct {
	Available   bool            `json:"available"`
	From        time.Time       `json:"from"`
	Until       time.Time       `json:"until"`
	CollectedAt *time.Time      `json:"collected_at"`
	Points      []FirewallPoint `json:"points"`
	Allowed     uint64          `json:"allowed"`
	Blocked     uint64          `json:"blocked"`
}

func migrateFirewall(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS firewall_clock(id INTEGER PRIMARY KEY CHECK(id=1), at INTEGER NOT NULL, generation TEXT NOT NULL, seen INTEGER NOT NULL, accepted INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS firewall_minute(minute INTEGER PRIMARY KEY,allowed INTEGER NOT NULL,blocked INTEGER NOT NULL,samples INTEGER NOT NULL);`)
	return err
}

// RecordFirewall starts from a baseline after startup/reset or a collection
// gap. No pre-upgrade totals are assigned to the last 24 hours. Aggregate
// packet history is independent of opt-in per-device byte accounting.
func (s *Store) RecordFirewall(now time.Time, generation string, seen, accepted uint64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var at int64
	var previous string
	var oldSeen, oldAccepted uint64
	err = tx.QueryRow(`SELECT at,generation,seen,accepted FROM firewall_clock WHERE id=1`).Scan(&at, &previous, &oldSeen, &oldAccepted)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	elapsed := now.Unix() - at
	if generation != "" && generation == previous && elapsed > 0 && elapsed <= 180 && seen >= oldSeen && accepted >= oldAccepted && seen-oldSeen >= accepted-oldAccepted {
		allowed := accepted - oldAccepted
		blocked := seen - oldSeen - allowed
		_, err = tx.Exec(`INSERT INTO firewall_minute(minute,allowed,blocked,samples) VALUES(?,?,?,1) ON CONFLICT(minute) DO UPDATE SET allowed=allowed+excluded.allowed,blocked=blocked+excluded.blocked,samples=samples+1`, now.UTC().Truncate(time.Minute).Unix(), allowed, blocked)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO firewall_clock(id,at,generation,seen,accepted) VALUES(1,?,?,?,?) ON CONFLICT(id) DO UPDATE SET at=excluded.at,generation=excluded.generation,seen=excluded.seen,accepted=excluded.accepted`, now.Unix(), generation, seen, accepted)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM firewall_minute WHERE minute<? OR minute>?`, now.Add(-25*time.Hour).Unix(), now.Add(time.Minute).Unix())
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) FirewallActivity(now time.Time) (FirewallActivity, error) {
	until := now.UTC().Truncate(time.Minute).Add(time.Minute)
	from := until.Add(-24 * time.Hour)
	out := FirewallActivity{From: from, Until: now.UTC(), Points: []FirewallPoint{}}
	if s == nil || s.db == nil {
		return out, nil
	}
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var last int64
	err = tx.QueryRow(`SELECT at FROM firewall_clock WHERE id=1`).Scan(&last)
	if err != nil && err != sql.ErrNoRows {
		return out, err
	}
	if last > 0 {
		at := time.Unix(last, 0).UTC()
		out.CollectedAt = &at
		out.Available = now.Unix()-last >= 0 && now.Unix()-last <= 180
	}
	for i := 0; i < 48; i++ {
		out.Points = append(out.Points, FirewallPoint{Start: from.Add(time.Duration(i) * 30 * time.Minute)})
	}
	rows, err := tx.Query(`SELECT minute,allowed,blocked,samples FROM firewall_minute WHERE minute>=? AND minute<? ORDER BY minute`, from.Unix(), until.Unix())
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var minute int64
		var allowed, blocked uint64
		var samples int
		if err := rows.Scan(&minute, &allowed, &blocked, &samples); err != nil {
			rows.Close()
			return out, err
		}
		i := (minute - from.Unix()) / 1800
		p := &out.Points[i]
		p.Allowed += allowed
		p.Blocked += blocked
		p.Samples += samples
		out.Allowed += allowed
		out.Blocked += blocked
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
