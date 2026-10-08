package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/accounting"
	"github.com/vladimirperovic/minimalrouter/internal/dnsrisk"
)

var dnsRiskRegistry sync.Map

func (s *Server) ConfigureDNSRisk(service *dnsrisk.Service) {
	if service == nil {
		dnsRiskRegistry.Delete(s)
	} else {
		dnsRiskRegistry.Store(s, service)
	}
}

func (s *Server) dnsRisk() *dnsrisk.Service {
	value, _ := dnsRiskRegistry.Load(s)
	service, _ := value.(*dnsrisk.Service)
	return service
}

func (s *Server) registerDNSRiskRoutes(mux *http.ServeMux) {
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return s.securityHeadersMiddleware(s.trustedNetworksMiddleware(s.authMiddleware(next)))
	}
	mux.HandleFunc("GET /api/v1/dns-activity/alerts/summary", gate(s.handleDNSRiskSummary))
	mux.HandleFunc("GET /api/v1/dns-activity/alerts", gate(s.handleDNSRiskAlerts))
	mux.HandleFunc("POST /api/v1/dns-activity/alerts/{id}/acknowledge", gate(s.handleDNSRiskAcknowledge))
	mux.HandleFunc("POST /api/v1/dns-activity/alerts/{id}/ignore", gate(s.handleDNSRiskIgnore))
	mux.HandleFunc("GET /api/v1/dns-activity/alerts/exceptions", gate(s.handleDNSRiskExceptions))
	mux.HandleFunc("DELETE /api/v1/dns-activity/alerts/exceptions/{id}", gate(s.handleDNSRiskRemoveException))
	mux.HandleFunc("POST /api/v1/dns-activity/alerts/refresh", gate(s.handleDNSRiskRefresh))
}

func (s *Server) handleDNSRiskSummary(w http.ResponseWriter, r *http.Request) {
	result := struct {
		dnsrisk.Summary
		Collection accounting.DNSCollectionStatus `json:"collection"`
	}{Summary: dnsrisk.Summary{Sources: []dnsrisk.FeedStatus{}}, Collection: s.configuredDNSActivity().Status(time.Now())}
	if service := s.dnsRisk(); service != nil {
		summary, err := service.Summary(time.Now())
		if err != nil {
			http.Error(w, "DNS risk status unavailable", http.StatusServiceUnavailable)
			return
		}
		result.Summary = summary
	}
	writeDNSActivityJSON(w, result)
}

func (s *Server) requireDNSRisk(w http.ResponseWriter) *dnsrisk.Service {
	service := s.dnsRisk()
	if service == nil {
		http.Error(w, "DNS risk monitoring is unavailable", http.StatusServiceUnavailable)
	}
	return service
}

func (s *Server) handleDNSRiskAlerts(w http.ResponseWriter, r *http.Request) {
	service := s.requireDNSRisk(w)
	if service == nil {
		return
	}
	q := r.URL.Query()
	view := q.Get("view")
	if view == "" {
		view = "new"
	}
	category := q.Get("category")
	if (view != "new" && view != "all") || !dnsrisk.ValidateCategory(category) {
		http.Error(w, "Invalid alert filter", http.StatusBadRequest)
		return
	}
	offset, limit := 0, 50
	for key, target := range map[string]*int{"offset": &offset, "limit": &limit} {
		if raw := q.Get(key); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil {
				http.Error(w, "Invalid pagination", http.StatusBadRequest)
				return
			}
			*target = n
		}
	}
	if offset < 0 || offset > 10000 || limit < 1 || limit > 100 {
		http.Error(w, "Invalid pagination", http.StatusBadRequest)
		return
	}
	page, err := service.Alerts(view, category, offset, limit)
	if err != nil {
		http.Error(w, "DNS risk alerts unavailable", http.StatusServiceUnavailable)
		return
	}
	writeDNSActivityJSON(w, page)
}

func riskID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid alert ID", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func riskMutationResult(w http.ResponseWriter, err error) bool {
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Alert or exception not found", http.StatusNotFound)
		return false
	}
	if err != nil {
		http.Error(w, "DNS risk change could not be saved", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (s *Server) handleDNSRiskAcknowledge(w http.ResponseWriter, r *http.Request) {
	service := s.requireDNSRisk(w)
	if service == nil {
		return
	}
	id, ok := riskID(w, r)
	if !ok {
		return
	}
	if !riskMutationResult(w, service.Acknowledge(id)) {
		return
	}
	s.appendAudit("dns_risk.alert_reviewed", auditActor(r.RemoteAddr), nil)
	writeDNSActivityJSON(w, map[string]bool{"acknowledged": true})
}

func (s *Server) handleDNSRiskIgnore(w http.ResponseWriter, r *http.Request) {
	service := s.requireDNSRisk(w)
	if service == nil {
		return
	}
	id, ok := riskID(w, r)
	if !ok {
		return
	}
	if !riskMutationResult(w, service.Ignore(id)) {
		return
	}
	// Domain names never enter the general metadata-only audit log.
	s.appendAudit("dns_risk.exception_added", auditActor(r.RemoteAddr), nil)
	writeDNSActivityJSON(w, map[string]bool{"ignored": true})
}

func (s *Server) handleDNSRiskExceptions(w http.ResponseWriter, r *http.Request) {
	service := s.requireDNSRisk(w)
	if service == nil {
		return
	}
	entries, err := service.Exceptions()
	if err != nil {
		http.Error(w, "DNS risk exceptions unavailable", http.StatusServiceUnavailable)
		return
	}
	writeDNSActivityJSON(w, map[string]any{"exceptions": entries})
}

func (s *Server) handleDNSRiskRemoveException(w http.ResponseWriter, r *http.Request) {
	service := s.requireDNSRisk(w)
	if service == nil {
		return
	}
	id, ok := riskID(w, r)
	if !ok {
		return
	}
	if !riskMutationResult(w, service.RemoveException(id)) {
		return
	}
	s.appendAudit("dns_risk.exception_removed", auditActor(r.RemoteAddr), nil)
	writeDNSActivityJSON(w, map[string]bool{"removed": true})
}

func (s *Server) handleDNSRiskRefresh(w http.ResponseWriter, r *http.Request) {
	service := s.requireDNSRisk(w)
	if service == nil {
		return
	}
	summary, err := service.Summary(time.Now())
	if err != nil {
		http.Error(w, "DNS risk status unavailable", http.StatusServiceUnavailable)
		return
	}
	if !summary.Enabled {
		http.Error(w, "Enable DNS activity recording before downloading category lists", http.StatusConflict)
		return
	}
	if !service.RequestRefresh() {
		w.Header().Set("Retry-After", "300")
		http.Error(w, "A category update was already requested; retry in five minutes", http.StatusTooManyRequests)
		return
	}
	s.appendAudit("dns_risk.sources_refresh_requested", auditActor(r.RemoteAddr), nil)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
	writeDNSActivityJSON(w, map[string]bool{"queued": true})
}
