package apply

import (
	"os"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

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
	engine := NewEngine(initialCfg, store)

	if engine.GetStore() == nil {
		t.Errorf("Expected store to be attached to engine")
	}

	// Process valid transaction
	newCfg := initialCfg
	newCfg.LAN.IPAddress = "10.0.0.1"

	tx, err := engine.ProcessTransaction("tx-test-1", newCfg)
	if err != nil {
		t.Fatalf("ProcessTransaction failed: %v", err)
	}

	if tx.CurrentState != StateCommitted {
		t.Errorf("Expected state StateCommitted, got %s", tx.CurrentState)
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
	engine := NewEngine(initialCfg, store)

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
