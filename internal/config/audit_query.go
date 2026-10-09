package config

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Classification is shared by API consumers rather than guessed by each UI.
// Only api.mutation uses its route; rejected access stays a security event.
const auditCategorySQL = `CASE
 WHEN event_type GLOB 'auth.*' OR event_type GLOB 'access.*' OR event_type GLOB 'audit.*' THEN 'security'
 WHEN event_type GLOB 'recovery.*' OR event_type GLOB 'firmware.*' OR event_type GLOB 'service.*' OR event_type GLOB 'gateway.auto_recovery*'
   OR (event_type = 'config.transaction' AND json_extract(details_json, '$.state') IN ('RolledBack','RecoveryRequired')) THEN 'recovery'
 WHEN event_type GLOB 'dns*' OR event_type GLOB 'device.*' OR event_type GLOB 'gateway.*'
   OR event_type GLOB 'wireguard.*' OR event_type GLOB 'firewall.*' OR event_type GLOB 'dhcp.*'
   OR event_type GLOB 'wifi.*' OR event_type GLOB 'qos.*' OR event_type GLOB 'squid.*' THEN 'network'
 WHEN event_type IN ('api.mutation','config.request') THEN CASE
   WHEN json_extract(details_json, '$.path') GLOB '/api/v1/auth/*' THEN 'security'
   WHEN json_extract(details_json, '$.path') GLOB '/api/v1/backup/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/recovery/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/import/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/snapshots*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/firmware/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/system/actions/*' THEN 'recovery'
   WHEN json_extract(details_json, '$.path') GLOB '/api/v1/dns*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/devices/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/wireguard/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/firewall/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/gateway/*'
     OR json_extract(details_json, '$.path') GLOB '/api/v1/qos/*' THEN 'network'
   ELSE 'configuration' END
 ELSE 'configuration' END`

type AuditQuery struct {
	Limit     int        `json:"limit"`
	Category  string     `json:"category,omitempty"`
	Search    string     `json:"q,omitempty"`
	Actor     string     `json:"actor,omitempty"`
	EventType string     `json:"event_type,omitempty"`
	Since     *time.Time `json:"since,omitempty"`
	Until     *time.Time `json:"until,omitempty"`
	Cursor    string     `json:"cursor,omitempty"`
}

type AuditPage struct {
	Events         []AuditEvent   `json:"events"`
	Filters        AuditQuery     `json:"filters"`
	HasMore        bool           `json:"has_more"`
	NextCursor     string         `json:"next_cursor,omitempty"`
	MatchingCount  int            `json:"matching_count"`
	CategoryCounts map[string]int `json:"category_counts"`
	RetainedCount  int            `json:"retained_count"`
	RetentionLimit int            `json:"retention_limit"`
	OldestAt       *time.Time     `json:"oldest_at,omitempty"`
	NewestAt       *time.Time     `json:"newest_at,omitempty"`
	GeneratedAt    time.Time      `json:"generated_at"`
	Notice         string         `json:"notice"`
}

// Raw SQLite timestamp preserves the indexed key exactly, including fractional
// precision. No OFFSET means concurrent inserts do not duplicate older pages.
type auditCursor struct {
	Timestamp string `json:"t"`
	ID        string `json:"id"`
}

func decodeAuditCursor(value string) (auditCursor, error) {
	var cursor auditCursor
	if len(value) > 512 {
		return cursor, fmt.Errorf("invalid cursor")
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor, fmt.Errorf("invalid cursor")
	}
	if json.Unmarshal(data, &cursor) != nil || len(cursor.Timestamp) == 0 || len(cursor.Timestamp) > 96 || !strings.HasPrefix(cursor.ID, "audit-") || len(cursor.ID) > 128 {
		return cursor, fmt.Errorf("invalid cursor")
	}
	return cursor, nil
}

func (q AuditQuery) Validate() error {
	if q.Limit < 1 || q.Limit > 500 {
		return fmt.Errorf("limit must be between 1 and 500")
	}
	switch q.Category {
	case "", "all", "security", "configuration", "network", "recovery":
	default:
		return fmt.Errorf("invalid category")
	}
	if len(q.Search) > 256 || len(q.Actor) > 255 || len(q.EventType) > 96 {
		return fmt.Errorf("audit filter is too long")
	}
	if q.Since != nil && q.Until != nil && q.Since.After(*q.Until) {
		return fmt.Errorf("since must be before until")
	}
	if q.Cursor != "" {
		_, err := decodeAuditCursor(q.Cursor)
		return err
	}
	return nil
}

func (s *SQLiteStore) QueryAuditEvents(ctx context.Context, q AuditQuery) (AuditPage, error) {
	page := AuditPage{Events: []AuditEvent{}, Filters: q, CategoryCounts: map[string]int{}, RetentionLimit: auditRetention, GeneratedAt: time.Now().UTC(),
		Notice: "Local bounded history; older events may have been pruned. Request rejections are rate limited and share a 1000-event retention pool; audit.throttled and audit.suppressed describe suppression. Counts are stored records, not incident totals."}
	if err := q.Validate(); err != nil {
		return page, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	where := []string{"1=1"}
	args := []any{}
	if q.Search != "" {
		// instr treats SQL wildcards literally and searches every metadata field.
		where = append(where, `(instr(lower(event_type || ' ' || actor), lower(?)) > 0 OR EXISTS
		 (SELECT 1 FROM json_each(details_json) WHERE instr(lower(key || ' ' || coalesce(value,'')), lower(?)) > 0))`)
		args = append(args, q.Search, q.Search)
	}
	if q.Actor != "" {
		where = append(where, "actor = ?")
		args = append(args, q.Actor)
	}
	if q.EventType != "" {
		where = append(where, "event_type = ?")
		args = append(args, q.EventType)
	}
	if q.Since != nil {
		where = append(where, "timestamp >= ?")
		args = append(args, q.Since.UTC())
	}
	if q.Until != nil {
		where = append(where, "timestamp <= ?")
		args = append(args, q.Until.UTC())
	}
	base := " FROM (SELECT *, " + auditCategorySQL + " AS category FROM audit_events) WHERE "
	rows, err := tx.QueryContext(ctx, "SELECT category, count(*)"+base+strings.Join(where, " AND ")+" GROUP BY category", args...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var category string
		var count int
		if err := rows.Scan(&category, &count); err != nil {
			rows.Close()
			return page, err
		}
		page.CategoryCounts[category] = count
		if q.Category == "" || q.Category == "all" || q.Category == category {
			page.MatchingCount += count
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM audit_events").Scan(&page.RetainedCount); err != nil {
		return page, err
	}
	if page.RetainedCount > 0 {
		var oldest, newest time.Time
		if err := tx.QueryRowContext(ctx, "SELECT timestamp FROM audit_events ORDER BY timestamp, id LIMIT 1").Scan(&oldest); err != nil {
			return page, err
		}
		if err := tx.QueryRowContext(ctx, "SELECT timestamp FROM audit_events ORDER BY timestamp DESC, id DESC LIMIT 1").Scan(&newest); err != nil {
			return page, err
		}
		page.OldestAt, page.NewestAt = &oldest, &newest
	}
	if q.Category != "" && q.Category != "all" {
		where = append(where, "category = ?")
		args = append(args, q.Category)
	}
	if q.Cursor != "" {
		cursor, _ := decodeAuditCursor(q.Cursor)
		where = append(where, "(timestamp < ? OR (timestamp = ? AND id < ?))")
		args = append(args, cursor.Timestamp, cursor.Timestamp, cursor.ID)
	}
	args = append(args, q.Limit+1)
	rows, err = tx.QueryContext(ctx, "SELECT id,event_type,actor,timestamp,details_json,category,CAST(timestamp AS TEXT)"+base+strings.Join(where, " AND ")+" ORDER BY timestamp DESC,id DESC LIMIT ?", args...)
	if err != nil {
		return page, err
	}
	var last auditCursor
	for rows.Next() {
		var event AuditEvent
		var details, rawTime string
		if err := rows.Scan(&event.ID, &event.EventType, &event.Actor, &event.Timestamp, &details, &event.Category, &rawTime); err != nil {
			rows.Close()
			return page, err
		}
		if len(page.Events) == q.Limit {
			page.HasMore = true
			break
		}
		if err := json.Unmarshal([]byte(details), &event.Details); err != nil {
			rows.Close()
			return page, err
		}
		if event.Details == nil {
			event.Details = map[string]string{}
		}
		page.Events = append(page.Events, event)
		last = auditCursor{rawTime, event.ID}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if page.HasMore {
		data, _ := json.Marshal(last)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return page, tx.Commit()
}
