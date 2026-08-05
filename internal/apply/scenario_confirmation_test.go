package apply

import (
	"context"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

type recordingScenarioClient struct {
	requests []ApplyRequest
	status   *TunnelStatus
}

func (c *recordingScenarioClient) Apply(_ context.Context, req ApplyRequest) (*ApplyResponse, error) {
	c.requests = append(c.requests, req)
	if req.Op == OpWGTunnelStatus {
		return &ApplyResponse{ID: req.ID, Success: true, Verified: true, TunnelStatus: c.status}, nil
	}
	return &ApplyResponse{ID: req.ID, Success: true, Verified: true}, nil
}

func validScenarioWGClient(cfg *config.SystemConfig) {
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "test-user"
	cfg.WAN.Password = "test-password-long-enough"
	cfg.WGClient.Enabled = true
	cfg.WGClient.Interface = "wg1"
	cfg.WGClient.PrivateKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	cfg.WGClient.PublicKey = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBA="
	cfg.WGClient.Endpoint = "office.example.com:51820"
	cfg.WGClient.Address = "10.7.0.2/32"
	cfg.WGClient.AllowedIPs = []string{"10.9.0.0/24"}
	cfg.WGClient.PersistentKeepalive = 25
}

func TestWiFiCredentialChangeRequiresConfirmationWhenAPIsActive(t *testing.T) {
	current := config.DefaultConfig()
	current.WiFi.Enabled = true
	current.WiFi.Passphrase = "old-passphrase-123"
	candidate := current.DeepCopy()
	candidate.WiFi.Passphrase = "new-passphrase-456"
	if !requiresConfirmation(current, candidate) {
		t.Fatal("active Wi-Fi credential change can disconnect the operator and must require confirmation")
	}
}

func TestUnrelatedConfirmationDoesNotDependOnWG1Health(t *testing.T) {
	initial := config.DefaultConfig()
	validScenarioWGClient(&initial)
	candidate := initial.DeepCopy()
	candidate.TrustedNetworks = append(candidate.TrustedNetworks, "10.255.255.0/24")

	client := &recordingScenarioClient{}
	engine := NewEngineWithClient(initial, nil, client)
	tx, err := engine.ProcessTransaction("tx-trust-with-office-down", candidate)
	if err != nil {
		t.Fatalf("apply candidate: %v", err)
	}
	if tx.CurrentState != StateAwaitingConfirmation {
		t.Fatalf("state=%s, want AwaitingConfirmation", tx.CurrentState)
	}
	if _, err := engine.ConfirmTransaction(tx.ID); err != nil {
		t.Fatalf("unrelated confirmation was coupled to wg1 health: %v", err)
	}
	for _, req := range client.requests {
		if req.Op == OpWGTunnelStatus {
			t.Fatal("unchanged wg1 was queried during an unrelated trusted-network confirmation")
		}
	}
}

func TestWG1ChangeRequiresFreshHandshakeBeforeCanonicalCommit(t *testing.T) {
	initial := config.DefaultConfig()
	validScenarioWGClient(&initial)
	candidate := initial.DeepCopy()
	candidate.WGClient.Endpoint = "office2.example.com:51820"

	client := &recordingScenarioClient{status: &TunnelStatus{Interface: "wg1", LastHandshake: time.Now().Unix()}}
	engine := NewEngineWithClient(initial, nil, client)
	tx, err := engine.ProcessTransaction("tx-wg1-change", candidate)
	if err != nil {
		t.Fatalf("apply wg1 candidate: %v", err)
	}
	if _, err := engine.ConfirmTransaction(tx.ID); err != nil {
		t.Fatalf("fresh wg1 handshake rejected: %v", err)
	}
	want := []OperationType{OpApplyAll, OpWGTunnelStatus, OpCommitConfirmed}
	if len(client.requests) != len(want) {
		t.Fatalf("request count=%d, want %d", len(client.requests), len(want))
	}
	for i, op := range want {
		if client.requests[i].Op != op {
			t.Fatalf("request[%d]=%s, want %s", i, client.requests[i].Op, op)
		}
	}
}

func TestWG1StaleHandshakeCannotBeConfirmed(t *testing.T) {
	initial := config.DefaultConfig()
	validScenarioWGClient(&initial)
	candidate := initial.DeepCopy()
	candidate.WGClient.PublicKey = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCA="

	client := &recordingScenarioClient{status: &TunnelStatus{Interface: "wg1", LastHandshake: time.Now().Add(-10 * time.Minute).Unix()}}
	engine := NewEngineWithClient(initial, nil, client)
	tx, err := engine.ProcessTransaction("tx-wg1-stale", candidate)
	if err != nil {
		t.Fatalf("apply wg1 candidate: %v", err)
	}
	if _, err := engine.ConfirmTransaction(tx.ID); err == nil {
		t.Fatal("stale wg1 handshake was accepted")
	}
	if pending := engine.GetPendingTransaction(); pending == nil {
		t.Fatal("failed wg1 proof removed the pending transaction instead of preserving rollback")
	}
}
