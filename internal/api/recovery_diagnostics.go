package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/startup"
	"github.com/vladimirperovic/minimalrouter/internal/telemetry"
)

func (s *Server) buildRecoveryDiagnostics(ctx context.Context, cfg config.SystemConfig) ([]byte, error) {
	base, err := telemetry.BuildDiagnosticBundle(cfg)
	if err != nil {
		return nil, err
	}
	var bundle map[string]any
	if err := json.Unmarshal(base, &bundle); err != nil {
		return nil, err
	}
	delete(bundle, "service_health")
	collectedErrors := []string{}
	bundle["health"] = s.healthSnapshot()
	runtime := runtimeSnapshot(cfg.WAN.Interface, cfg.RuntimeLANInterface(), applianceDataDir())
	bundle["runtime"] = map[string]any{"available": runtime.Available, "os": runtime.OS, "architecture": runtime.Architecture,
		"uptime_seconds": runtime.UptimeSeconds, "cpu_percent": runtime.CPULoadPercent, "memory_used_bytes": runtime.MemoryUsedBytes,
		"memory_total_bytes": runtime.MemoryTotalBytes, "storage": runtime.Storage}
	bundle["engine"] = s.engine.GetStatus()
	if pending := s.engine.GetPendingTransaction(); pending != nil {
		bundle["pending_transaction"] = map[string]any{"id": pending.ID, "state": pending.CurrentState, "confirmation_deadline": pending.ConfirmationDeadline}
	}
	s.recoveryMu.Lock()
	op, err := s.recoveryOperation(ctx)
	s.recoveryMu.Unlock()
	if err != nil {
		collectedErrors = append(collectedErrors, "Recovery operation unavailable")
	} else if op != nil {
		// The diagnostic copy needs outcomes, not DNS exception domains or reasons.
		op.DNSPolicy = nil
		bundle["recovery_operation"] = op
	}
	if store := s.engine.GetStore(); store != nil {
		if last, err := store.LastBackupExport(); err == nil {
			bundle["last_backup_export_at"] = last
		} else {
			collectedErrors = append(collectedErrors, "Backup export history unavailable")
		}
		if page, err := store.QueryAuditEvents(ctx, config.AuditQuery{Limit: 100, Category: "recovery"}); err == nil {
			bundle["recovery_events"] = page
		} else {
			collectedErrors = append(collectedErrors, "Recovery audit history unavailable")
		}
	}
	if boots, err := startup.Load(applianceDataDir()); err == nil {
		summaries := make([]startup.BootSummary, 0, len(boots))
		for _, boot := range boots {
			summaries = append(summaries, startup.Summarize(boot, time.Now()))
		}
		bundle["startup"] = summaries
	} else {
		collectedErrors = append(collectedErrors, "Startup history unavailable")
	}
	if s.dnsFilter != nil {
		if status, err := s.dnsFilter.Status(ctx); err == nil {
			bundle["dns_filter"] = map[string]any{"revision": status.Policy.Revision, "categories": status.Policy.Categories, "exception_count": len(status.Policy.Exceptions), "healthy": status.Healthy, "updating": status.Updating, "error": status.Error, "applied_at": status.AppliedAt}
		} else {
			collectedErrors = append(collectedErrors, "DNS status unavailable")
		}
	}
	bundle["collection_errors"] = collectedErrors
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(bundle, "", "  ")
}
