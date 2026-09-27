package config

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Request rejections are written at a rate the requester chooses. They must
// never push the rest of the audit history out of the retained window.
func TestHighVolumeAuditEventsCannotDisplaceIncidentHistory(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.AppendAuditEvent("auth.login_failed", "192.168.1.9", map[string]string{"result": "invalid_credentials"}); err != nil {
		t.Fatal(err)
	}
	// Seed the flood in one transaction; the append below runs the prune.
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()
	for i := 0; i < highVolumeAuditRetention+50; i++ {
		if _, err := tx.Exec(`INSERT INTO audit_events (id, event_type, actor, timestamp, details_json) VALUES (?, 'auth.unauthorized', '192.168.1.66', ?, '{}')`,
			fmt.Sprintf("audit-flood-%05d", i), base.Add(time.Duration(i)*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendAuditEvent("auth.unauthorized", "192.168.1.66", map[string]string{"path": "/api/v1/system"}); err != nil {
		t.Fatal(err)
	}

	var highVolume, incident int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE event_type = 'auth.unauthorized'`).Scan(&highVolume); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE event_type = 'auth.login_failed'`).Scan(&incident); err != nil {
		t.Fatal(err)
	}
	if highVolume != highVolumeAuditRetention || incident != 1 {
		t.Fatalf("high-volume=%d incident=%d, want %d and 1", highVolume, incident, highVolumeAuditRetention)
	}
	if err := store.MaintainStorage(); err != nil {
		t.Fatal(err)
	}
	if !IsHighVolumeAuditEvent("auth.unauthorized") || IsHighVolumeAuditEvent("auth.login_failed") {
		t.Fatal("audit event classification changed unexpectedly")
	}
}

func TestSnapshotKindsAreRetainedIndependently(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg, err := store.GetLatestConfig()
	if err != nil {
		t.Fatal(err)
	}
	manual, err := store.CreateManualSnapshot(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < snapshotRetentionPerKind+5; i++ {
		if _, err := store.CreateSnapshot(cfg); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MaintainStorage(); err != nil {
		t.Fatal(err)
	}
	snapshots, err := store.ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	foundManual := false
	for _, snapshot := range snapshots {
		counts[snapshot.Kind]++
		foundManual = foundManual || snapshot.ID == manual.ID
	}
	if !foundManual || counts[SnapshotKindManual] != 1 || counts[SnapshotKindAutomatic] != snapshotRetentionPerKind {
		t.Fatalf("manual retained=%v counts=%v", foundManual, counts)
	}
	restored, err := store.GetSnapshot(manual.ID)
	if err != nil || restored.Kind != SnapshotKindManual {
		t.Fatalf("manual snapshot lookup = %+v, %v", restored, err)
	}
}

func TestDeepCopyDetachesLegacyFilterServices(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AdGuard.FilterDevices = []FilterDeviceRule{{ID: "legacy", BlockedServices: []string{"youtube"}}}
	copied := cfg.DeepCopy()
	copied.AdGuard.FilterDevices[0].BlockedServices[0] = "changed"
	if cfg.AdGuard.FilterDevices[0].BlockedServices[0] != "youtube" {
		t.Fatal("DeepCopy shared a legacy filter service list with the original")
	}
}

// Installations upgraded from a release without snapshot kinds keep their
// restore points; they become automatic snapshots.
func TestSnapshotKindMigrationKeepsExistingSnapshots(t *testing.T) {
	dir := t.TempDir()
	legacy, err := sql.Open("sqlite", filepath.Join(dir, "minimalrouter.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`CREATE TABLE snapshots (
		id TEXT PRIMARY KEY, revision INTEGER NOT NULL, created_at DATETIME NOT NULL,
		checksum TEXT NOT NULL, config_json TEXT NOT NULL);
		INSERT INTO snapshots VALUES ('snap-legacy', 1, '2026-09-01T10:00:00+02:00', 'abc', '{}');`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshots, err := store.ListSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].ID != "snap-legacy" || snapshots[0].Kind != SnapshotKindAutomatic {
		t.Fatalf("legacy snapshots after migration = %+v", snapshots)
	}
}

// A backup exported before extra LANs carried a router address must restore
// exactly as the same configuration loads from the canonical store.
func TestBackupRestoreAppliesLegacyMigration(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Firewall.ExtraLANs = []ExtraLANConfig{{
		ID: "legacy-extra", Name: "Legacy extra", Interface: "eth2",
		CIDR: "10.20.30.0/24", DstIP: "10.20.30.10", DstPort: 443,
		AllowFrom: []string{"192.168.1.0/24"}, Enabled: true,
	}}
	const passphrase = "legacy-backup-passphrase-1"
	encrypted, err := EncryptConfigBackup(cfg, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecryptConfigBackup(encrypted, passphrase)
	if err != nil {
		t.Fatalf("legacy backup was rejected: %v", err)
	}
	if got := restored.Firewall.ExtraLANs[0].RouterAddress; got != "10.20.30.1/24" {
		t.Fatalf("legacy extra LAN router address = %q", got)
	}
}
