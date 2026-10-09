package apply

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestAuditRecordsTimerRollbackAndExcludesConfiguration(t *testing.T) {
	store, err := config.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	initial := config.DefaultConfig()
	engine := NewEngineWithClient(initial, store, &testApplyClient{response: &ApplyResponse{Success: true, Verified: true}})
	next := initial
	next.LAN.IPAddress = "192.168.1.2"
	next.LAN.CIDR = "192.168.1.2/24"
	tx, err := engine.ProcessTransaction("test-audit-rollback", next)
	if err != nil {
		t.Fatal(err)
	}
	engine.rollbackExpired(tx.ID)
	events, err := store.ListAuditEvents(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected apply and rollback, got %+v", events)
	}
	if events[0].Details["state"] != "RolledBack" || events[1].Details["state"] != "AwaitingConfirmation" || events[0].Details["transaction_id"] != tx.ID {
		t.Fatalf("missing linked outcomes: %+v", events)
	}
	if engine.GetPendingTransaction() != nil || engine.GetCurrentConfig().LAN.IPAddress != initial.LAN.IPAddress {
		t.Fatal("rollback changed")
	}
	tx.Error = "TEST-SECRET-helper-output"
	tx.Diff = "TEST-SECRET-config-diff"
	engine.auditTransaction(tx, initial.Revision, "test")
	events, err = store.ListAuditEvents(100)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(events)
	if strings.Contains(string(data), "TEST-SECRET") {
		t.Fatal("raw transaction error or diff leaked")
	}
}
