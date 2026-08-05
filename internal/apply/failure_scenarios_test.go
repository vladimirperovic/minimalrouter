package apply

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

type scenarioApplyStep struct {
	response ApplyResponse
	err      error
}

type scenarioApplyClient struct {
	steps    []scenarioApplyStep
	requests []ApplyRequest
}

func (c *scenarioApplyClient) Apply(_ context.Context, req ApplyRequest) (*ApplyResponse, error) {
	c.requests = append(c.requests, req)
	if len(c.steps) == 0 {
		return &ApplyResponse{ID: req.ID, Success: true, Verified: true}, nil
	}
	step := c.steps[0]
	c.steps = c.steps[1:]
	if step.err != nil {
		return nil, step.err
	}
	resp := step.response
	resp.ID = req.ID
	return &resp, nil
}

func successfulScenarioStep() scenarioApplyStep {
	return scenarioApplyStep{response: ApplyResponse{Success: true, Verified: true}}
}

func candidateWithNewLAN(initial config.SystemConfig) config.SystemConfig {
	candidate := initial
	candidate.LAN.IPAddress = "10.23.0.1"
	candidate.LAN.CIDR = "10.23.0.1/24"
	candidate.LAN.Netmask = "255.255.255.0"
	candidate.DHCP.RangeStart = "10.23.0.100"
	candidate.DHCP.RangeEnd = "10.23.0.200"
	return candidate
}

func validWireGuardConfig() config.WireGuardConfig {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	peerKeyBytes := make([]byte, 32)
	peerKeyBytes[0] = 1
	peerKey := base64.StdEncoding.EncodeToString(peerKeyBytes)
	return config.WireGuardConfig{
		Enabled:    true,
		Interface:  "wg0",
		PrivateKey: key,
		ListenPort: 51820,
		Address:    "10.8.0.1/24",
		Peers: []config.WireGuardPeer{{
			ID:         "phone",
			Name:       "Phone",
			PublicKey:  peerKey,
			AllowedIPs: []string{"10.8.0.2/32"},
			Enabled:    true,
		}},
	}
}

func TestAmbiguousPrivilegedApplyRetriesSameTransaction(t *testing.T) {
	initial := config.DefaultConfig()
	candidate := initial
	candidate.System.Hostname = "router-after-retry"
	client := &scenarioApplyClient{steps: []scenarioApplyStep{
		{err: errors.New("response connection closed")},
		successfulScenarioStep(),
	}}
	engine := NewEngineWithClient(initial, nil, client)

	tx, err := engine.ProcessTransaction("tx-ambiguous-response", candidate)
	if err != nil {
		t.Fatalf("ambiguous response should be recovered by an idempotent retry: %v", err)
	}
	if tx.CurrentState != StateCommitted {
		t.Fatalf("expected committed transaction, got %s", tx.CurrentState)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected two privileged attempts, got %d", len(client.requests))
	}
	if client.requests[0].ID != client.requests[1].ID {
		t.Fatalf("ambiguous retry changed transaction ID: %q != %q", client.requests[0].ID, client.requests[1].ID)
	}
	if client.requests[0].Revision != client.requests[1].Revision {
		t.Fatal("ambiguous retry changed the applied revision")
	}
}

func TestConfirmationResponseLossRetriesSameCanonicalAck(t *testing.T) {
	initial := config.DefaultConfig()
	client := &scenarioApplyClient{steps: []scenarioApplyStep{
		successfulScenarioStep(),
		{err: errors.New("canonical acknowledgement response lost")},
		successfulScenarioStep(),
		successfulScenarioStep(),
	}}
	engine := NewEngineWithClient(initial, nil, client)

	tx, err := engine.ProcessTransaction("tx-confirm-response-loss", candidateWithNewLAN(initial))
	if err != nil {
		t.Fatalf("create pending transaction: %v", err)
	}
	defer func() {
		if engine.pending != nil && engine.pending.timer != nil {
			engine.pending.timer.Stop()
		}
	}()

	confirmed, err := engine.ConfirmTransaction(tx.ID)
	if err != nil {
		t.Fatalf("confirmation should recover a lost canonical-ack response: %v", err)
	}
	if confirmed.CurrentState != StateCommitted {
		t.Fatalf("expected committed confirmation, got %s", confirmed.CurrentState)
	}
	if len(client.requests) != 4 {
		t.Fatalf("expected apply, two canonical-ack attempts, and LAN finalize reconcile, got %d requests", len(client.requests))
	}
	if client.requests[1].Op != OpCommitConfirmed || client.requests[2].Op != OpCommitConfirmed {
		t.Fatalf("canonical acknowledgement retry used unexpected operations: %s, %s", client.requests[1].Op, client.requests[2].Op)
	}
	if client.requests[1].ID != client.requests[2].ID {
		t.Fatalf("ambiguous acknowledgement retry changed transaction ID: %q != %q", client.requests[1].ID, client.requests[2].ID)
	}
	if !client.requests[1].SkipWANVerify || !client.requests[2].SkipWANVerify {
		t.Fatal("canonical acknowledgement retry must remain independent of transient WAN availability")
	}
	if client.requests[3].Op != OpReconcile {
		t.Fatalf("final helper operation=%s, want LAN finalize %s", client.requests[3].Op, OpReconcile)
	}
	for i := 1; i < len(client.requests); i++ {
		if client.requests[i].Revision != client.requests[0].Revision {
			t.Fatalf("request %d changed confirmed revision", i)
		}
	}
}

func TestWireGuardOnlyControlPlaneChangesRequireConfirmation(t *testing.T) {
	current := config.DefaultConfig()
	current.System.ManagementAccess = "wireguard_only"
	current.WireGuard = validWireGuardConfig()

	tests := map[string]func(*config.SystemConfig){
		"private key rotation": func(candidate *config.SystemConfig) {
			keyBytes := make([]byte, 32)
			keyBytes[0] = 2
			candidate.WireGuard.PrivateKey = base64.StdEncoding.EncodeToString(keyBytes)
		},
		"listen port change": func(candidate *config.SystemConfig) {
			candidate.WireGuard.ListenPort++
		},
		"tunnel address change": func(candidate *config.SystemConfig) {
			candidate.WireGuard.Address = "10.9.0.1/24"
			candidate.WireGuard.Peers[0].AllowedIPs = []string{"10.9.0.2/32"}
		},
		"peer removal": func(candidate *config.SystemConfig) {
			candidate.WireGuard.Peers = nil
		},
		"peer route change": func(candidate *config.SystemConfig) {
			candidate.WireGuard.Peers[0].AllowedIPs = []string{"10.8.0.3/32"}
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := current
			candidate.WireGuard.Peers = append([]config.WireGuardPeer(nil), current.WireGuard.Peers...)
			candidate.WireGuard.Peers[0].AllowedIPs = append([]string(nil), current.WireGuard.Peers[0].AllowedIPs...)
			mutate(&candidate)
			if !requiresConfirmation(current, candidate) {
				t.Fatal("WireGuard-only management change must remain provisional until connectivity is confirmed")
			}
		})
	}
}

func TestLANManagedWireGuardChangeDoesNotCreateUnnecessaryConfirmation(t *testing.T) {
	current := config.DefaultConfig()
	current.WAN.Enabled = true
	current.WAN.Username = "test-user"
	current.WAN.Password = "test-password"
	current.WireGuard = validWireGuardConfig()
	candidate := current
	candidate.WireGuard.ListenPort++

	if requiresConfirmation(current, candidate) {
		t.Fatal("LAN-managed WireGuard maintenance should not require commit-confirm")
	}
}

func TestFailedTimeoutRollbackKeepsCandidateReachableAndRetriesWithFreshID(t *testing.T) {
	initial := config.DefaultConfig()
	client := &scenarioApplyClient{steps: []scenarioApplyStep{
		successfulScenarioStep(),
		{response: ApplyResponse{Success: false, Verified: false, Error: "temporary rollback failure"}},
		successfulScenarioStep(),
	}}
	engine := NewEngineWithClient(initial, nil, client)

	tx, err := engine.ProcessTransaction("tx-timeout-retry", candidateWithNewLAN(initial))
	if err != nil {
		t.Fatalf("create pending transaction: %v", err)
	}
	if engine.pending == nil {
		t.Fatal("expected a pending transaction")
	}
	engine.pending.timer.Stop()

	engine.rollbackExpired(tx.ID)
	pending := engine.GetPendingTransaction()
	if pending == nil {
		t.Fatal("failed rollback must retain pending state so candidate management access remains available")
	}
	if !strings.Contains(pending.Error, "retry scheduled") {
		t.Fatalf("expected explicit retry status, got %q", pending.Error)
	}

	second := candidateWithNewLAN(initial)
	second.System.Hostname = "must-be-rejected"
	if rejected, processErr := engine.ProcessTransaction("tx-while-rollback-uncertain", second); processErr == nil || rejected.CurrentState != StateRejected {
		t.Fatal("new configuration must be rejected while rollback outcome is unresolved")
	}

	engine.rollbackExpired(tx.ID)
	if engine.GetPendingTransaction() != nil {
		t.Fatal("successful rollback retry must clear pending state")
	}
	if tx.CurrentState != StateRolledBack {
		t.Fatalf("expected rolled back state, got %s", tx.CurrentState)
	}
	if len(client.requests) != 3 {
		t.Fatalf("expected apply and two rollback attempts, got %d requests", len(client.requests))
	}
	if client.requests[1].ID == client.requests[2].ID {
		t.Fatalf("rollback retry reused cached failure transaction ID %q", client.requests[1].ID)
	}
}

func TestPowerLossDuringPendingChangeReconcilesCanonicalConfiguration(t *testing.T) {
	store := newScenarioStore(t)
	initial, err := store.GetLatestConfig()
	if err != nil {
		t.Fatalf("read canonical config: %v", err)
	}
	client := &scenarioApplyClient{steps: []scenarioApplyStep{successfulScenarioStep()}}
	engine := NewEngineWithClient(initial, store, client)

	tx, err := engine.ProcessTransaction("tx-power-loss-pending", candidateWithNewLAN(initial))
	if err != nil {
		t.Fatalf("create pending transaction: %v", err)
	}
	if tx.CurrentState != StateAwaitingConfirmation {
		t.Fatalf("expected awaiting confirmation, got %s", tx.CurrentState)
	}
	engine.pending.timer.Stop()

	canonicalAfterCrash, err := store.GetLatestConfig()
	if err != nil {
		t.Fatalf("read canonical config after simulated power loss: %v", err)
	}
	if canonicalAfterCrash.LAN.CIDR != initial.LAN.CIDR {
		t.Fatalf("unconfirmed LAN change reached canonical storage: %s", canonicalAfterCrash.LAN.CIDR)
	}

	reconcileClient := &scenarioApplyClient{steps: []scenarioApplyStep{successfulScenarioStep()}}
	restarted := NewEngineWithClient(canonicalAfterCrash, store, reconcileClient)
	if err := restarted.Reconcile(context.Background()); err != nil {
		t.Fatalf("boot reconcile canonical state: %v", err)
	}
	if len(reconcileClient.requests) != 1 {
		t.Fatalf("expected one boot reconcile request, got %d", len(reconcileClient.requests))
	}
	if got := reconcileClient.requests[0].Config.LAN.CIDR; got != initial.LAN.CIDR {
		t.Fatalf("boot reconcile used unconfirmed state %s instead of canonical %s", got, initial.LAN.CIDR)
	}
}

func TestUnverifiedPrivilegedResponseNeverCommits(t *testing.T) {
	initial := config.DefaultConfig()
	candidate := initial
	candidate.System.Hostname = "unverified-candidate"
	client := &scenarioApplyClient{steps: []scenarioApplyStep{{
		response: ApplyResponse{Success: true, Verified: false},
	}}}
	engine := NewEngineWithClient(initial, nil, client)

	tx, err := engine.ProcessTransaction("tx-unverified", candidate)
	if err == nil {
		t.Fatal("unverified privileged response must fail")
	}
	if tx.CurrentState != StateRecoveryRequired {
		t.Fatalf("expected recovery-required state, got %s", tx.CurrentState)
	}
	if engine.GetCurrentConfig().System.Hostname != initial.System.Hostname {
		t.Fatal("unverified configuration became canonical")
	}
}

func newScenarioStore(t *testing.T) *config.FileStore {
	t.Helper()
	store, err := config.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("create scenario store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
