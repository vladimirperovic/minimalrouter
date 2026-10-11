package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func (s *Server) handleGatewayProbe(w http.ResponseWriter, r *http.Request) {
	monitor := s.configuredGatewayMonitor()
	if monitor == nil {
		http.Error(w, "Gateway monitoring unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	summary, err := monitor.ProbeNow(ctx)
	if err != nil {
		writeGatewayJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeGatewayJSON(w, http.StatusOK, summary)
}

func (s *Server) handleGatewayEvents(w http.ResponseWriter, r *http.Request) {
	monitor := s.configuredGatewayMonitor()
	if monitor == nil {
		http.Error(w, "Gateway monitoring unavailable", http.StatusServiceUnavailable)
		return
	}
	outages, err := monitor.Outages()
	if err != nil {
		http.Error(w, "Outage history unavailable", http.StatusServiceUnavailable)
		return
	}
	store := s.store
	if store == nil && s.engine != nil {
		store = s.engine.GetStore()
	}
	recoveries := []config.AuditEvent{}
	available := store != nil
	if store != nil {
		for _, kind := range []string{"gateway.auto_recovery_completed", "gateway.auto_recovery_failed", "service.recovery", "service.recovery_failed"} {
			page, err := store.QueryAuditEvents(r.Context(), config.AuditQuery{Limit: 20, EventType: kind})
			if err != nil {
				available = false
				recoveries = nil
				break
			}
			for _, event := range page.Events {
				if kind == "service.recovery" || kind == "service.recovery_failed" {
					if event.Details["action"] != "wan-reconnect" {
						continue
					}
				}
				recoveries = append(recoveries, event)
			}
		}
	}
	sort.Slice(recoveries, func(i, j int) bool { return recoveries[i].Timestamp.After(recoveries[j].Timestamp) })
	if len(recoveries) > 20 {
		recoveries = recoveries[:20]
	}
	writeGatewayJSON(w, http.StatusOK, map[string]interface{}{"outages": outages, "outage_window_days": 7, "recoveries": recoveries, "recovery_history_available": available})
}
