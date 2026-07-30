package recovery

import (
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/auth"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestResetAuthenticationChangesPasswordClearsTOTPAndSessions(t *testing.T) {
	store := testStore(t)
	if err := store.SetAdminHash("old-hash"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAdminTOTPSecret("JBSWY3DPEHPK3PXP"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.CreateSession("session", "csrf", false, now, now); err != nil {
		t.Fatal(err)
	}
	manager := Manager{Store: store}
	password := "a-recovery-password-longer-than-fifteen"
	if err := manager.ResetAuthentication(password, true); err != nil {
		t.Fatal(err)
	}
	hash, err := store.GetAdminHash()
	if err != nil {
		t.Fatal(err)
	}
	ok, err := auth.VerifyPassword(password, hash)
	if err != nil || !ok {
		t.Fatal("new password was not stored")
	}
	secret, err := store.GetAdminTOTPSecret()
	if err != nil || secret != "" {
		t.Fatal("TOTP was not cleared")
	}
	if _, _, _, _, err := store.GetSession("session"); err == nil {
		t.Fatal("session survived credential recovery")
	}
}

func TestSetLANCreatesSnapshotAndAdvancesRevision(t *testing.T) {
	store := testStore(t)
	manager := Manager{Store: store}
	snapshot, err := manager.SetLAN("enp2s0", "10.20.30.1/24")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ID == "" {
		t.Fatal("missing pre-change snapshot")
	}
	cfg, err := store.GetLatestConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Revision != 2 || cfg.LAN.Interface != "enp2s0" || cfg.LAN.IPAddress != "10.20.30.1" {
		t.Fatalf("unexpected recovered LAN: %+v", cfg.LAN)
	}
	if cfg.DHCP.RangeStart != "10.20.30.100" || cfg.DHCP.RangeEnd != "10.20.30.200" {
		t.Fatalf("unexpected DHCP range: %s-%s", cfg.DHCP.RangeStart, cfg.DHCP.RangeEnd)
	}
}

func TestRestoreSnapshotCreatesUndoPoint(t *testing.T) {
	store := testStore(t)
	manager := Manager{Store: store}
	original, err := store.GetLatestConfig()
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateSnapshot(original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetLAN("eth2", "10.0.50.1/24"); err != nil {
		t.Fatal(err)
	}
	undo, err := manager.RestoreSnapshot(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if undo.ID == "" {
		t.Fatal("restore did not create an undo snapshot")
	}
	restored, err := store.GetLatestConfig()
	if err != nil {
		t.Fatal(err)
	}
	if restored.LAN.IPAddress != original.LAN.IPAddress || restored.Revision != 3 {
		t.Fatalf("snapshot not restored: %+v", restored.LAN)
	}
}

func TestFactoryResetPreservesPreResetSnapshot(t *testing.T) {
	store := testStore(t)
	manager := Manager{Store: store}
	snapshot, err := manager.FactoryReset("enp1s0", "enp2s0", "factory-reset-password-long-enough")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ID == "" {
		t.Fatal("factory reset did not create recovery snapshot")
	}
	cfg, err := store.GetLatestConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WAN.Interface != "enp1s0" || cfg.LAN.Interface != "enp2s0" || cfg.Revision != 2 {
		t.Fatalf("unexpected factory defaults: WAN=%s LAN=%s revision=%d", cfg.WAN.Interface, cfg.LAN.Interface, cfg.Revision)
	}
}

func testStore(t *testing.T) *config.SQLiteStore {
	t.Helper()
	store, err := config.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
