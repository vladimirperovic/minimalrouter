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
		if err := config.ValidateLiveCandidate(req.Config, &c.lastGood); err != nil {
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

// A candidate must satisfy the same rules at preview, apply, persistence and
// startup. Legacy faults are reported before any privileged side effect.
func TestUnrelatedEditRequiresLegacyRepairBeforeApply(t *testing.T) {
	stored := staleApplianceConfig(t)
	client := &applydGuardClient{lastGood: stored}
	engine := NewEngineWithClient(stored, nil, client)
	next := stored.DeepCopy()
	next.Accounting.Enabled = true
	if _, err := PreviewTransition(stored, next); err == nil {
		t.Fatal("preview accepted unrepaired legacy candidate")
	}
	tx, err := engine.ProcessTransaction("tx-stale-unrelated", next)
	if err == nil || tx.CurrentState != StateRejected || !strings.Contains(tx.Error, "wan.password") {
		t.Fatalf("expected early field-specific rejection: tx=%+v err=%v", tx, err)
	}
	if client.sawApply {
		t.Fatal("invalid candidate reached privileged helper")
	}
}

// A live repair cannot start when a failed commit could only roll back to an
// invalid configuration. Require local migration before any privileged apply.
func TestRepairingLegacyCandidateRequiresValidRollbackBaseline(t *testing.T) {
	stored := staleApplianceConfig(t)
	client := &applydGuardClient{lastGood: stored}
	engine := NewEngineWithClient(stored, nil, client)

	next := stored
	next.WAN.Password = "a-real-pppoe-secret"

	if _, err := engine.ProcessTransaction("tx-stale-repair", next); err == nil || !strings.Contains(err.Error(), "local configuration migration") {
		t.Fatalf("invalid baseline should require local migration: %v", err)
	}
	if client.sawApply {
		t.Fatal("repair with an invalid rollback target reached privileged apply")
	}
	// After explicit migration, a restarted engine has a valid recovery target.
	client.lastGood = next
	engine = NewEngineWithClient(next, nil, client)
	next.Accounting.Enabled = !next.Accounting.Enabled
	if _, err := engine.ProcessTransaction("tx-after-migration", next); err != nil {
		t.Fatalf("valid migrated baseline should accept subsequent edits: %v", err)
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
