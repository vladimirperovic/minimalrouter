package api

import (
	"encoding/json"
	"net/http"
	"strconv"
)

func (s *Server) handleGetAuditEvents(w http.ResponseWriter, r *http.Request) {
	session, err := s.sessionMgr.ValidateSession(r)
	if err != nil || session.ReadOnly {
		http.Error(w, "Administrator session required", http.StatusForbidden)
		return
	}
	store := s.store
	if store == nil {
		store = s.engine.GetStore()
	}
	if store == nil {
		http.Error(w, "Audit store unavailable", http.StatusServiceUnavailable)
		return
	}
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 500 {
			http.Error(w, "limit must be between 1 and 500", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	events, err := store.ListAuditEvents(limit)
	if err != nil {
		http.Error(w, "Could not read audit events", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"events": events})
}
