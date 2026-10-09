package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBackupExportHistoryMigratesBeyond500AndSurvivesAuditDeletion(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	at := time.Now().UTC().Add(-48 * time.Hour)
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 601; i++ {
		eventType, details := "auth.login", `{}`
		if i == 0 {
			eventType = "api.mutation"
			details = `{"path":"/api/v1/backup/export","status":"200"}`
		}
		if _, err := tx.Exec(`INSERT INTO audit_events(id,event_type,actor,timestamp,details_json) VALUES(?,?,?,?,?)`, fmt.Sprint(i), eventType, "test", at.Add(time.Duration(i)*time.Second), details); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(store.db); err != nil {
		t.Fatal(err)
	}
	last, err := store.LastBackupExport()
	if err != nil || last == nil || !last.Equal(at) {
		t.Fatalf("older export lost: %v %v", last, err)
	}
	if _, err := store.db.Exec(`DELETE FROM audit_events`); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAuditEvent("api.mutation", "test", map[string]string{"path": "/api/v1/backup/export", "status": "401"}); err != nil {
		t.Fatal(err)
	}
	last, err = store.LastBackupExport()
	if err != nil || last == nil || !last.Equal(at) {
		t.Fatal("failed export/pruning changed durable metadata")
	}
	if err := store.AppendAuditEvent("api.mutation", "test", map[string]string{"path": "/api/v1/backup/export", "status": "200"}); err != nil {
		t.Fatal(err)
	}
	last, err = store.LastBackupExport()
	if err != nil || last == nil || !last.After(at) {
		t.Fatal("successful export not recorded")
	}
}

func TestNamedSnapshotRetentionAndIntegrity(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i := 0; i < 23; i++ {
		if _, err := store.CreateManualSnapshot(DefaultConfig(), fmt.Sprintf("Restore point %02d", i)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateSnapshot(DefaultConfig()); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.ListSnapshots()
	if err != nil || len(items) != 40 {
		t.Fatalf("retention: %d %v", len(items), err)
	}
	counts := map[string]int{}
	for _, item := range items {
		counts[item.Kind]++
		if item.Kind == SnapshotKindManual && !strings.HasPrefix(item.Label, "Restore point ") {
			t.Fatal("name lost")
		}
		saved, err := store.GetSnapshot(item.ID)
		if err != nil || saved.Label != item.Label {
			t.Fatal("get snapshot name lost")
		}
	}
	if counts[SnapshotKindManual] != 20 || counts[SnapshotKindAutomatic] != 20 {
		t.Fatalf("pools: %v", counts)
	}
	for _, invalid := range []string{strings.Repeat("x", 81), "name\nwith newline"} {
		if _, err := store.CreateManualSnapshot(DefaultConfig(), invalid); err == nil {
			t.Fatal("invalid name accepted")
		}
	}
	if _, err := store.db.Exec(`UPDATE snapshots SET config_json='{}' WHERE id=?`, items[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSnapshot(items[0].ID); err != ErrSnapshotCorrupt {
		t.Fatalf("corruption misreported: %v", err)
	}
	if _, err := store.GetSnapshot("absent"); err != ErrSnapshotNotFound {
		t.Fatalf("not found: %v", err)
	}
}
