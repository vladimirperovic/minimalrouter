package config

import (
	"strings"
	"testing"
)

func TestSnapshotIntegrityIsCheckedOnRead(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	snapshot, err := store.CreateSnapshot(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE snapshots SET config_json = ? WHERE id = ?`, `{"tampered":true}`, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSnapshot(snapshot.ID); err == nil ||
		!strings.Contains(err.Error(), "integrity check failed") {
		t.Fatalf("tampered snapshot was not rejected: %v", err)
	}
}

func TestAuditEventsAreBoundedMetadata(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.AppendAuditEvent("config.update", "192.0.2.10", map[string]string{
		"method": "PUT",
		"path":   "/api/v1/config",
		"status": "200",
	}); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListAuditEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != "config.update" || events[0].Details["status"] != "200" {
		t.Fatalf("unexpected audit events: %+v", events)
	}
	if err := store.AppendAuditEvent("bad", "actor", map[string]string{"body": strings.Repeat("x", 5000)}); err == nil {
		t.Fatal("oversized audit metadata was accepted")
	}
}

func TestSnapshotRetentionIsBounded(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := DefaultConfig()
	for i := 0; i < 25; i++ {
		if _, err := store.CreateSnapshot(cfg); err != nil {
			t.Fatal(err)
		}
	}
	snapshots, err := store.ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 20 {
		t.Fatalf("snapshot retention kept %d entries, want 20", len(snapshots))
	}
}
