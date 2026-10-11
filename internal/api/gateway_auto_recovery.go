package api

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/gateway"
)

const (
	autoRecoveryOutageWindow = 3 * time.Minute
	autoRecoveryCooldown     = 10 * time.Minute
	autoRecoveryPollInterval = 30 * time.Second
	// autoRecoverySampleMaxAge bounds how old the evidence may be. The monitor
	// keeps its last summary when a collection round fails, so without this an
	// hour-old offline reading — evidence only that measurement stopped — would
	// still look like a link that is down right now.
	autoRecoverySampleMaxAge = 2 * time.Minute
)

var gatewayAutoRecoveryRegistry sync.Map // map[*Server]context.CancelFunc
var gatewayRecoveryStates sync.Map       // map[*Server]gatewayRecoveryStatus

type gatewayRecoveryStatus struct {
	State             string     `json:"state"`
	Reason            string     `json:"reason"`
	UpdatedAt         time.Time  `json:"updated_at"`
	DisconnectedSince *time.Time `json:"disconnected_since,omitempty"`
	NextAttemptAt     *time.Time `json:"next_attempt_at,omitempty"`
	LastAttemptAt     *time.Time `json:"last_attempt_at,omitempty"`
	LastResult        string     `json:"last_result,omitempty"`
}

func recoveryStatus(wan bool, summary gateway.Summary, status apply.EngineStatus, pending bool, down, last, now time.Time) gatewayRecoveryStatus {
	result := gatewayRecoveryStatus{State: "armed", Reason: "Watching the PPPoE link", UpdatedAt: now}
	if !last.IsZero() {
		result.LastAttemptAt = &last
	}
	switch {
	case !wan || !summary.Enabled:
		result.State, result.Reason = "paused", "WAN or monitoring is disabled"
	case !summary.Available || summary.Timestamp.IsZero() || now.Sub(summary.Timestamp) < 0 || now.Sub(summary.Timestamp) > autoRecoverySampleMaxAge || summary.State == gateway.StateUnknown:
		result.State, result.Reason = "waiting", "Waiting for fresh link evidence"
	case status.Applying || status.RecoveryRequired || pending:
		result.State, result.Reason = "blocked", "A configuration change, confirmation or recovery is in progress"
	case !summary.Link.Connected:
		result.State, result.Reason = "verifying", "Verifying a continuous 3-minute link outage"
		if !down.IsZero() {
			result.DisconnectedSince = &down
			due := down.Add(autoRecoveryOutageWindow)
			result.NextAttemptAt = &due
		}
		if !last.IsZero() && now.Sub(last) < autoRecoveryCooldown {
			result.State, result.Reason = "cooldown", "Waiting before another recovery attempt"
			due := last.Add(autoRecoveryCooldown)
			result.NextAttemptAt = &due
		}
	}
	return result
}

func (s *Server) handleGatewayRecoveryStatus(w http.ResponseWriter, _ *http.Request) {
	monitor := s.configuredGatewayMonitor()
	if monitor == nil || s.engine == nil {
		http.Error(w, "Recovery status unavailable", http.StatusServiceUnavailable)
		return
	}
	value, ok := gatewayRecoveryStates.Load(s)
	previous, _ := value.(gatewayRecoveryStatus)
	if !ok {
		previous = gatewayRecoveryStatus{State: "waiting", Reason: "Recovery observer is starting", UpdatedAt: time.Now()}
	}
	var down, last time.Time
	if previous.DisconnectedSince != nil {
		down = *previous.DisconnectedSince
	}
	if previous.LastAttemptAt != nil {
		last = *previous.LastAttemptAt
	}
	result := recoveryStatus(s.engine.GetCurrentConfig().WAN.Enabled, monitor.Summary(), s.engine.GetStatus(), s.engine.GetPendingTransaction() != nil, down, last, time.Now())
	result.LastResult = previous.LastResult
	if previous.State == "recovering" {
		result = previous
	}
	writeGatewayJSON(w, http.StatusOK, result)
}

func (s *Server) configureGatewayAutoRecovery(monitor *gateway.Monitor) {
	if previous, ok := gatewayAutoRecoveryRegistry.LoadAndDelete(s); ok {
		if cancel, ok := previous.(context.CancelFunc); ok {
			cancel()
		}
	}
	gatewayRecoveryStates.Delete(s)
	if monitor == nil || s.engine == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	gatewayAutoRecoveryRegistry.Store(s, context.CancelFunc(cancel))
	go s.runGatewayAutoRecovery(ctx, monitor)
}

// usableOutageEvidence reports whether a summary is trustworthy proof that the
// WAN link is down right now. "Unknown" is not "down": a monitor that could not
// measure, or whose last successful measurement has aged out, says nothing
// about the link, and must never be the reason to mutate the runtime.
func usableOutageEvidence(summary gateway.Summary, now time.Time) bool {
	if !summary.Enabled || !summary.Available || summary.Timestamp.IsZero() {
		return false
	}
	if summary.State == gateway.StateUnknown || summary.State == "" {
		return false
	}
	if age := now.Sub(summary.Timestamp); age < 0 || age > autoRecoverySampleMaxAge {
		return false
	}
	return !summary.Link.Connected
}

func autoRecoveryDue(wanEnabled bool, summary gateway.Summary, status apply.EngineStatus, hasPending bool, disconnectedSince, lastAttempt, now time.Time) bool {
	if !wanEnabled || disconnectedSince.IsZero() || !usableOutageEvidence(summary, now) {
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
// packet loss, DNS failures, or a remote host being down. It only restarts
// the PPPoE service after the link itself has remained down
// continuously for three minutes, and then enforces a ten-minute cooldown.
// The existing allowlisted WAN reconnect action restarts the service and
// verifies a fresh PPP address and default route before reporting completion.
func (s *Server) runGatewayAutoRecovery(ctx context.Context, monitor *gateway.Monitor) {
	ticker := time.NewTicker(autoRecoveryPollInterval)
	defer ticker.Stop()
	var disconnectedSince time.Time
	var lastAttempt time.Time
	var lastResult string

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		cfg := s.engine.GetCurrentConfig()
		summary := monitor.Summary()
		now := time.Now()
		status := s.engine.GetStatus()
		pending := s.engine.GetPendingTransaction() != nil
		view := recoveryStatus(cfg.WAN.Enabled, summary, status, pending, disconnectedSince, lastAttempt, now)
		view.LastResult = lastResult
		gatewayRecoveryStates.Store(s, view)
		// An unmeasurable or stale sample restarts the outage clock rather than
		// letting it accumulate: three minutes of "we don't know" is not three
		// minutes of proven outage.
		if !cfg.WAN.Enabled || !usableOutageEvidence(summary, now) {
			disconnectedSince = time.Time{}
			view.DisconnectedSince, view.NextAttemptAt = nil, nil
			gatewayRecoveryStates.Store(s, view)
			continue
		}
		if disconnectedSince.IsZero() {
			disconnectedSince = now
			start := now
			view.DisconnectedSince = &start
			gatewayRecoveryStates.Store(s, view)
			continue
		}
		if !autoRecoveryDue(cfg.WAN.Enabled, summary, status, pending, disconnectedSince, lastAttempt, now) {
			continue
		}

		lastAttempt = now
		view.State, view.Reason = "recovering", "Reconnecting WAN and verifying the address and route"
		attempt := lastAttempt
		view.LastAttemptAt = &attempt
		gatewayRecoveryStates.Store(s, view)
		reconcileCtx, cancel := context.WithTimeout(ctx, apply.ReconcileBudget)
		err := s.engine.RunServiceAction(reconcileCtx, apply.ServiceActionWANReconnect)
		cancel()
		if err != nil {
			lastResult = "failed"
			view.State, view.LastResult = "cooldown", lastResult
			gatewayRecoveryStates.Store(s, view)
			s.appendAudit("gateway.auto_recovery_failed", "local", map[string]string{"reason": "pppoe_link_down", "duration_seconds": strconv.FormatInt(int64(time.Since(now).Seconds()), 10)})
			continue
		}
		lastResult = "completed"
		view.State, view.LastResult = "cooldown", lastResult
		view.DisconnectedSince, view.NextAttemptAt = nil, nil
		gatewayRecoveryStates.Store(s, view)
		s.appendAudit("gateway.auto_recovery_completed", "local", map[string]string{"reason": "pppoe_link_down", "duration_seconds": strconv.FormatInt(int64(time.Since(now).Seconds()), 10)})
		disconnectedSince = time.Time{}
	}
}
