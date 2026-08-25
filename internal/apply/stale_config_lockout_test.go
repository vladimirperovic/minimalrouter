package apply

import (
	"context"
	"strings"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// applydGuardClient reproduces the guard cmd/router-applyd/applyAll runs before
// it touches anything: the helper judges the incoming configuration against its
// own last-good record. If the two planes ever disagree, an edit the management
// plane accepted is refused here and the appliance cannot be saved at all.
type applydGuardClient struct {
	lastGood config.SystemConfig
	sawApply bool
}

func (c *applydGuardClient) Apply(_ context.Context, req ApplyRequest) (*ApplyResponse, error) {
	if req.Op == OpApplyAll {
		c.sawApply = true
		if err := req.Config.ValidateChangesFrom(&c.lastGood); err != nil {
			return &ApplyResponse{ID: req.ID, Success: false, Error: "privileged validation rejected configuration"}, nil
		}
		if err := req.Config.ValidateScenarioSafety(); err != nil {
			return &ApplyResponse{ID: req.ID, Success: false, Error: "privileged scenario safety rejected configuration"}, nil
		}
	}
	return &ApplyResponse{ID: req.ID, Success: true, Verified: true}, nil
}

// staleApplianceConfig is what an appliance upgraded in place is carrying: a
// configuration an older release wrote and applied, holding one value a newer,
// stricter rule rejects.
func staleApplianceConfig(t *testing.T) config.SystemConfig {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.TrustedNetworks = []string{"192.168.1.0/24"}
	cfg.WAN.Enabled = true
	cfg.WAN.Username = "isp-user"
	cfg.WAN.Password = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("fixture is supposed to be invalid under the current rules")
	}
	return cfg
}

// The regression this closes: delta validation in routerd alone left the helper
// judging the whole stored state, so every save was still refused -- just with
// an opaque error instead of a field-specific one.
func TestUnrelatedEditSavesBesideAStaleFault(t *testing.T) {
	stored := staleApplianceConfig(t)
	client := &applydGuardClient{lastGood: stored}
	engine := NewEngineWithClient(stored, nil, client)

	next := stored
	next.Accounting.Enabled = true

	tx, err := engine.ProcessTransaction("tx-stale-unrelated", next)
	if err != nil {
		t.Fatalf("an unrelated toggle must save beside an untouched stale fault: %v (state=%s, %s)", err, tx.CurrentState, tx.Error)
	}
	if !client.sawApply {
		t.Fatal("the transaction never reached the privileged helper")
	}
	if tx.CurrentState != StateCommitted && tx.CurrentState != StateVerified {
		t.Fatalf("unexpected terminal state %s: %s", tx.CurrentState, tx.Error)
	}
}

// Repairing the stale field must also go through, since that is the edit the
// operator actually needs to make.
func TestRepairingTheStaleFieldSaves(t *testing.T) {
	stored := staleApplianceConfig(t)
	client := &applydGuardClient{lastGood: stored}
	engine := NewEngineWithClient(stored, nil, client)

	next := stored
	next.WAN.Password = "a-real-pppoe-secret"

	if _, err := engine.ProcessTransaction("tx-stale-repair", next); err != nil {
		t.Fatalf("repairing the stale field must be accepted: %v", err)
	}
}

// Delta validation must not become a way in for a fault the change introduces.
func TestIntroducedFaultIsStillRejectedBesideAStaleFault(t *testing.T) {
	stored := staleApplianceConfig(t)
	client := &applydGuardClient{lastGood: stored}
	engine := NewEngineWithClient(stored, nil, client)

	next := stored
	next.LAN.IPAddress = "not-an-address"

	tx, err := engine.ProcessTransaction("tx-stale-introduced", next)
	if err == nil {
		t.Fatalf("an introduced fault must be rejected, got state %s", tx.CurrentState)
	}
	if !strings.Contains(tx.Error, "lan.ip_address") {
		t.Errorf("the rejection should name the field the change broke, got %q", tx.Error)
	}
}
