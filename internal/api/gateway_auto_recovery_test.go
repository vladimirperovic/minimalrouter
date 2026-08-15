package api

import (
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/gateway"
)

func TestAutoRecoveryDueRequiresSustainedPPPoELinkOutage(t *testing.T) {
	now := time.Now()
	summary := gateway.Summary{Enabled: true, Timestamp: now, Link: gateway.LinkStatus{Connected: false}}
	if autoRecoveryDue(true, summary, apply.EngineStatus{}, false, now.Add(-2*time.Minute), time.Time{}, now) {
		t.Fatal("recovery must not run before the outage window")
	}
	if !autoRecoveryDue(true, summary, apply.EngineStatus{}, false, now.Add(-4*time.Minute), time.Time{}, now) {
		t.Fatal("recovery should run after a sustained PPPoE link outage")
	}
	summary.Link.Connected = true
	if autoRecoveryDue(true, summary, apply.EngineStatus{}, false, now.Add(-4*time.Minute), time.Time{}, now) {
		t.Fatal("healthy PPPoE link must never trigger recovery")
	}
}

func TestAutoRecoveryDueHonorsSafetyGatesAndCooldown(t *testing.T) {
	now := time.Now()
	summary := gateway.Summary{Enabled: true, Timestamp: now, Link: gateway.LinkStatus{Connected: false}}
	downSince := now.Add(-4 * time.Minute)
	if autoRecoveryDue(true, summary, apply.EngineStatus{Applying: true}, false, downSince, time.Time{}, now) {
		t.Fatal("active apply must suppress auto recovery")
	}
	if autoRecoveryDue(true, summary, apply.EngineStatus{RecoveryRequired: true}, false, downSince, time.Time{}, now) {
		t.Fatal("recovery-required state must suppress auto recovery")
	}
	if autoRecoveryDue(true, summary, apply.EngineStatus{}, true, downSince, time.Time{}, now) {
		t.Fatal("pending confirmation must suppress auto recovery")
	}
	if autoRecoveryDue(true, summary, apply.EngineStatus{}, false, downSince, now.Add(-5*time.Minute), now) {
		t.Fatal("cooldown must suppress repeated recovery")
	}
	if !autoRecoveryDue(true, summary, apply.EngineStatus{}, false, downSince, now.Add(-11*time.Minute), now) {
		t.Fatal("recovery should be allowed after cooldown")
	}
}
