package apply

import (
	"context"
	"os"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

type testApplyClient struct {
	response *ApplyResponse
	err      error
}

func (c testApplyClient) Apply(_ context.Context, req ApplyRequest) (*ApplyResponse, error) {
	if c.err != nil {
		return nil, c.err
	}
	resp := *c.response
	resp.ID = req.ID
	return &resp, nil
}

func TestEngineTransactionLifecycle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "engine-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := config.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	initialCfg := config.DefaultConfig()
	engine := NewEngineWithClient(initialCfg, store, testApplyClient{
		response: &ApplyResponse{Success: true, Verified: true},
	})

	if engine.GetStore() == nil {
		t.Errorf("Expected store to be attached to engine")
	}

	// Process valid transaction
	newCfg := initialCfg
	newCfg.LAN.IPAddress = "10.0.0.1"
	newCfg.LAN.CIDR = "10.0.0.1/24"
	newCfg.DHCP.RangeStart = "10.0.0.100"
	newCfg.DHCP.RangeEnd = "10.0.0.200"

	tx, err := engine.ProcessTransaction("tx-test-1", newCfg)
	if err != nil {
		t.Fatalf("ProcessTransaction failed: %v", err)
	}

	if tx.CurrentState != StateAwaitingConfirmation {
		t.Errorf("Expected state StateAwaitingConfirmation, got %s", tx.CurrentState)
	}
	tx, err = engine.ConfirmTransaction(tx.ID)
	if err != nil {
		t.Fatalf("ConfirmTransaction failed: %v", err)
	}
	if tx.CurrentState != StateCommitted {
		t.Errorf("Expected confirmed state StateCommitted, got %s", tx.CurrentState)
	}

	if engine.GetCurrentConfig().LAN.IPAddress != "10.0.0.1" {
		t.Errorf("Expected current config LAN IP to be 10.0.0.1")
	}
}

func TestEngineInvalidTransactionRejection(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "engine-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := config.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	initialCfg := config.DefaultConfig()
	engine := NewEngineWithClient(initialCfg, store, testApplyClient{
		response: &ApplyResponse{Success: true, Verified: true},
	})

	// Process invalid transaction (collision WAN = LAN)
	invalidCfg := initialCfg
	invalidCfg.WAN.Interface = "eth0"
	invalidCfg.LAN.Interface = "eth0"

	tx, err := engine.ProcessTransaction("tx-invalid-1", invalidCfg)
	if err == nil {
		t.Fatalf("Expected validation error for collision WAN=LAN")
	}

	if tx.CurrentState != StateRejected {
		t.Errorf("Expected state StateRejected, got %s", tx.CurrentState)
	}
}
