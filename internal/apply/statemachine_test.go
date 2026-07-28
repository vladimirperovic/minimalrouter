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
	requests []ApplyRequest
}

func (c *testApplyClient) Apply(_ context.Context, req ApplyRequest) (*ApplyResponse, error) {
	c.requests = append(c.requests, req)
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
	client := &testApplyClient{
		response: &ApplyResponse{Success: true, Verified: true},
	}
	engine := NewEngineWithClient(initialCfg, store, client)

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
	if tx.Config.Revision != initialCfg.Revision+1 {
		t.Fatalf("expected pending revision %d, got %d", initialCfg.Revision+1, tx.Config.Revision)
	}
	if len(client.requests) != 1 || client.requests[0].Config.Revision != tx.Config.Revision {
		t.Fatalf("privileged helper and pending transaction used different revisions")
	}
	tx, err = engine.ConfirmTransaction(tx.ID)
	if err != nil {
		t.Fatalf("ConfirmTransaction failed: %v", err)
	}
	if tx.CurrentState != StateCommitted {
		t.Errorf("Expected confirmed state StateCommitted, got %s", tx.CurrentState)
	}
	if len(client.requests) != 2 || client.requests[1].Config.Revision != client.requests[0].Config.Revision {
		t.Fatalf("confirmation did not use the exact applied revision")
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
	engine := NewEngineWithClient(initialCfg, store, &testApplyClient{
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

func TestWiFiTopologyChangesRequireConfirmation(t *testing.T) {
	previous := config.DefaultConfig()

	enabled := previous
	enabled.WiFi.Enabled = true
	if !requiresConfirmation(previous, enabled) {
		t.Fatal("enabling the Wi-Fi LAN bridge must require confirmation")
	}

	newRadio := enabled
	newRadio.WiFi.Interface = "wlan1"
	if !requiresConfirmation(enabled, newRadio) {
		t.Fatal("changing the Wi-Fi bridge radio must require confirmation")
	}

	newSSID := enabled
	newSSID.WiFi.SSID = "Renamed-Network"
	if requiresConfirmation(enabled, newSSID) {
		t.Fatal("an SSID-only change does not alter management topology")
	}
}
