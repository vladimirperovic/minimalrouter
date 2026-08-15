package api

import (
	"context"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/gateway"
)

const (
	autoRecoveryOutageWindow = 3 * time.Minute
	autoRecoveryCooldown     = 10 * time.Minute
	autoRecoveryPollInterval = 30 * time.Second
)

var gatewayAutoRecoveryRegistry sync.Map // map[*Server]context.CancelFunc

func (s *Server) configureGatewayAutoRecovery(monitor *gateway.Monitor) {
	if previous, ok := gatewayAutoRecoveryRegistry.LoadAndDelete(s); ok {
		if cancel, ok := previous.(context.CancelFunc); ok {
			cancel()
		}
	}
	if monitor == nil || s.engine == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	gatewayAutoRecoveryRegistry.Store(s, context.CancelFunc(cancel))
	go s.runGatewayAutoRecovery(ctx, monitor)
}

func autoRecoveryDue(wanEnabled bool, summary gateway.Summary, status apply.EngineStatus, hasPending bool, disconnectedSince, lastAttempt, now time.Time) bool {
	if !wanEnabled || !summary.Enabled || summary.Timestamp.IsZero() || summary.Link.Connected || disconnectedSince.IsZero() {
		return false
	}
	if status.Applying || status.RecoveryRequired || hasPending {
		return false
	}
	if now.Sub(disconnectedSince) < autoRecoveryOutageWindow {
		return false
	}
	return lastAttempt.IsZero() || now.Sub(lastAttempt) >= autoRecoveryCooldown
}

// runGatewayAutoRecovery is deliberately conservative: it never reacts to
// packet loss, DNS failures, or a remote host being down. It only reconciles
// the canonical configuration after the PPPoE link itself has remained down
// continuously for three minutes, and then enforces a ten-minute cooldown.
// Reconcile uses the existing privileged helper and verified apply path; no new
// root capability is introduced here.
func (s *Server) runGatewayAutoRecovery(ctx context.Context, monitor *gateway.Monitor) {
	ticker := time.NewTicker(autoRecoveryPollInterval)
	defer ticker.Stop()
	var disconnectedSince time.Time
	var lastAttempt time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		cfg := s.engine.GetCurrentConfig()
		summary := monitor.Summary()
		if !cfg.WAN.Enabled || !summary.Enabled || summary.Timestamp.IsZero() || summary.Link.Connected {
			disconnectedSince = time.Time{}
			continue
		}
		if disconnectedSince.IsZero() {
			disconnectedSince = time.Now()
			continue
		}
		now := time.Now()
		status := s.engine.GetStatus()
		if !autoRecoveryDue(cfg.WAN.Enabled, summary, status, s.engine.GetPendingTransaction() != nil, disconnectedSince, lastAttempt, now) {
			continue
		}

		lastAttempt = now
		reconcileCtx, cancel := context.WithTimeout(ctx, apply.ReconcileBudget)
		err := s.engine.Reconcile(reconcileCtx)
		cancel()
		if err != nil {
			s.appendAudit("gateway.auto_recovery_failed", "local", map[string]string{"reason": "pppoe_link_down"})
			continue
		}
		s.appendAudit("gateway.auto_recovery_completed", "local", map[string]string{"reason": "pppoe_link_down"})
		disconnectedSince = time.Time{}
	}
}
