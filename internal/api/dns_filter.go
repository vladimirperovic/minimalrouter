package api

import (
	"net/http"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/dnsfilter"
	"github.com/vladimirperovic/minimalrouter/internal/services"
)

func (s *Server) ConfigureDNSFilter(service *dnsfilter.Service) { s.dnsFilter = service }

func (s *Server) registerDNSFilterRoutes(mux *http.ServeMux) {
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return s.securityHeadersMiddleware(s.trustedNetworksMiddleware(s.authMiddleware(next)))
	}
	mux.HandleFunc("GET /api/v1/dns-filter", gate(s.handleDNSFilterStatus))
	mux.HandleFunc("PUT /api/v1/dns-filter", gate(s.handleDNSFilterSave))
	mux.HandleFunc("POST /api/v1/dns-filter/refresh", gate(s.handleDNSFilterRefresh))
	mux.HandleFunc("POST /api/v1/dns-filter/check", gate(s.handleDNSFilterCheck))
}

func (s *Server) requireDNSFilter(w http.ResponseWriter) bool {
	if s.dnsFilter == nil {
		http.Error(w, "DNS filtering is unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (s *Server) handleDNSFilterStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireDNSFilter(w) {
		return
	}
	status, err := s.dnsFilter.Status(r.Context())
	if err != nil {
		http.Error(w, "DNS filter helper unavailable", 503)
		return
	}
	writeDNSActivityJSON(w, struct {
		dnsfilter.Status
		RouterTime string `json:"router_time"`
		Timezone   string `json:"timezone"`
	}{status, time.Now().Format(time.RFC3339), time.Now().Location().String()})
}
func (s *Server) handleDNSFilterSave(w http.ResponseWriter, r *http.Request) {
	if !s.requireDNSFilter(w) {
		return
	}
	var policy dnsfilter.Policy
	if err := decodeJSON(w, r, &policy); err != nil {
		http.Error(w, "Invalid DNS filter policy", 400)
		return
	}
	if err := policy.Validate(); err != nil {
		http.Error(w, err.Error(), 422)
		return
	}
	if err := s.dnsFilter.Start(policy, false); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	s.appendAudit("dns_filter.update_requested", auditActor(r.RemoteAddr), nil)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
	writeDNSActivityJSON(w, map[string]bool{"updating": true})
}
func (s *Server) handleDNSFilterRefresh(w http.ResponseWriter, r *http.Request) {
	if !s.requireDNSFilter(w) {
		return
	}
	status, err := s.dnsFilter.Status(r.Context())
	if err != nil {
		http.Error(w, "DNS filter unavailable", 503)
		return
	}
	if err = s.dnsFilter.Start(status.Policy, true); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	s.appendAudit("dns_filter.refresh_requested", auditActor(r.RemoteAddr), nil)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
	writeDNSActivityJSON(w, map[string]bool{"updating": true})
}
func (s *Server) handleDNSFilterCheck(w http.ResponseWriter, r *http.Request) {
	if !s.requireDNSFilter(w) {
		return
	}
	var request struct {
		Domain string `json:"domain"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		http.Error(w, "Invalid domain check", 400)
		return
	}
	result, err := s.dnsFilter.Check(r.Context(), request.Domain)
	if err != nil {
		http.Error(w, err.Error(), 422)
		return
	}
	// The bundled list is independent of the opt-in maintained categories.
	if s.engine.GetCurrentConfig().AdGuard.Enabled && !result.Exception {
		policy := dnsfilter.Policy{Exceptions: []dnsfilter.Exception{}}
		for _, domain := range services.BuiltinBlocklist() {
			policy.Exceptions = append(policy.Exceptions, dnsfilter.Exception{Domain: domain})
		}
		if policy.Allowed(result.Domain) {
			result.Action = "Block"
			result.Matches = append(result.Matches, dnsfilter.Match{Category: "bundled", Domain: result.Domain, Enabled: true})
		}
	}
	writeDNSActivityJSON(w, result)
}
