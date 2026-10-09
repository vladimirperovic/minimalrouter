package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func (s *Server) auditConfigRequest(r *http.Request, tx *apply.Transaction) {
	if tx == nil {
		return
	}
	s.appendAudit("config.request", auditActor(r.RemoteAddr), map[string]string{
		"transaction_id": tx.ID, "candidate_revision": fmt.Sprint(tx.Config.Revision),
		"method": r.Method, "path": r.URL.Path,
	})
}

func (s *Server) handleGetAuditEvents(w http.ResponseWriter, r *http.Request) {
	// Read-only sessions (the MCP bridge) may read the metadata-only audit
	// log; they authenticate with the administrator password and the auth
	// middleware still rejects every mutation they attempt.
	if _, err := s.requestSession(r); err != nil {
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
	values := r.URL.Query()
	query := config.AuditQuery{Limit: limit, Category: values.Get("category"), Search: strings.TrimSpace(values.Get("q")), Actor: values.Get("actor"), EventType: values.Get("event_type"), Cursor: values.Get("cursor")}
	for name, target := range map[string]**time.Time{"since": &query.Since, "until": &query.Until} {
		if value := values.Get(name); value != "" {
			parsed, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				http.Error(w, name+" must be an RFC3339 timestamp", http.StatusBadRequest)
				return
			}
			*target = &parsed
		}
	}
	if err := query.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	page, err := store.QueryAuditEvents(r.Context(), query)
	if err != nil {
		http.Error(w, "Could not read audit events", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}
