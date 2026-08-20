package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
)

func (s *Server) handleServiceAction(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.URL.Path, "/api/v1/system/actions/")
	switch action {
	case apply.ServiceActionWANReconnect, apply.ServiceActionDNSDHCPRestart, apply.ServiceActionWireGuardRestart:
	default:
		http.Error(w, "Unsupported service recovery action", http.StatusNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if err := s.engine.RunServiceAction(ctx, action); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	s.appendAudit("service.recovery", auditActor(r.RemoteAddr), map[string]string{"action": action})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "action": action})
}
