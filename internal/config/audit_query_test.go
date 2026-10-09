package config

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAuditSearchPagesEntireRetainedHistory(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stamp := time.Date(2026, 10, 9, 12, 0, 0, 123456789, time.UTC)
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 620; i++ {
		details := `{"a":"one","b":"two","c":"three","d":"four","e":"five","f":"six","g":"seven","h":"eight","reason":"needle%_&"}`
		if _, err := tx.Exec(`INSERT INTO audit_events(id,event_type,actor,timestamp,details_json) VALUES(?,?,?,?,?)`, fmt.Sprintf("audit-%04d", i), "auth.login_failed", "192.0.2.10", stamp, details); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	q := AuditQuery{Limit: 100, Search: "needle%_&", Category: "security", Actor: "192.0.2.10", Since: &stamp, Until: &stamp}
	seen := map[string]bool{}
	for {
		page, err := store.QueryAuditEvents(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		if page.MatchingCount != 620 || page.RetainedCount != 620 {
			t.Fatalf("wrong totals: %+v", page)
		}
		for _, event := range page.Events {
			if seen[event.ID] {
				t.Fatalf("duplicate %s", event.ID)
			}
			seen[event.ID] = true
		}
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" {
			t.Fatal("missing cursor")
		}
		q.Cursor = page.NextCursor
	}
	if len(seen) != 620 {
		t.Fatalf("only %d events reached", len(seen))
	}
	q.Search = "needleX"
	q.Cursor = ""
	page, err := store.QueryAuditEvents(context.Background(), q)
	if err != nil || len(page.Events) != 0 {
		t.Fatalf("wildcard search was not literal: %+v %v", page, err)
	}
}

func TestAuditCursorSurvivesNewEventsAndDeletedAnchor(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i := 0; i < 5; i++ {
		if err := store.AppendAuditEvent("config.request", "local", nil); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.QueryAuditEvents(context.Background(), AuditQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("DELETE FROM audit_events WHERE id=?", first.Events[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAuditEvent("auth.login_succeeded", "local", nil); err != nil {
		t.Fatal(err)
	}
	next, err := store.QueryAuditEvents(context.Background(), AuditQuery{Limit: 5, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Events) != 3 || next.HasMore {
		t.Fatalf("unstable older page: %+v", next)
	}
	for _, event := range next.Events {
		if event.EventType != "config.request" || event.ID == first.Events[0].ID {
			t.Fatalf("wrong page: %+v", next)
		}
	}
}

func TestAuditCategoriesAndLegacyNullMetadata(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cases := []struct {
		event, category string
		details         map[string]string
	}{
		{"access.trusted_network_rejected", "security", map[string]string{"path": "/api/v1/dns-filter"}},
		{"firmware.activation_start_failed", "recovery", nil},
		{"dns_filter.update_requested", "network", nil},
		{"service.recovery", "recovery", nil},
		{"device.internet_paused", "network", nil},
		{"audit.suppressed", "security", map[string]string{"count": "18"}},
		{"config.transaction", "recovery", map[string]string{"state": "RolledBack"}},
		{"config.request", "configuration", nil},
		{"api.mutation", "recovery", map[string]string{"path": "/api/v1/backup/restore"}},
	}
	for _, tc := range cases {
		if err := store.AppendAuditEvent(tc.event, "local", tc.details); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec("UPDATE audit_events SET details_json='null' WHERE event_type='dns_filter.update_requested'"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		page, err := store.QueryAuditEvents(context.Background(), AuditQuery{Limit: 100, Category: tc.category, EventType: tc.event})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Events) != 1 || page.Events[0].Category != tc.category || page.Events[0].Details == nil {
			t.Fatalf("bad %s page: %+v", tc.event, page)
		}
		data, _ := json.Marshal(page)
		if strings.Contains(string(data), `"details":null`) {
			t.Fatal("legacy null details escaped")
		}
	}
	for _, q := range []AuditQuery{{Limit: 501}, {Limit: 10, Category: "oops"}, {Limit: 10, Cursor: "bad"}, {Limit: 10, Search: strings.Repeat("x", 257)}} {
		if _, err := store.QueryAuditEvents(context.Background(), q); err == nil {
			t.Fatalf("accepted %+v", q)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.QueryAuditEvents(ctx, AuditQuery{Limit: 10}); err == nil {
		t.Fatal("cancelled query succeeded")
	}
}
