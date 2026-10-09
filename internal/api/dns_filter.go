package api

import (
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/dnsfilter"
	"github.com/vladimirperovic/minimalrouter/internal/telemetry"
)

func (s *Server) ConfigureDNSFilter(service *dnsfilter.Service) {
	s.dnsFilter = service
	if service != nil {
		service.SetObserver(func(op dnsfilter.Operation) {
			event := "dns_filter." + op.State
			if op.State == "running" {
				event = "dns_filter." + op.Kind + "_requested"
			}
			s.appendAudit(event, op.Actor, map[string]string{"operation_id": op.ID, "kind": op.Kind, "phase": op.Phase, "base_revision": strconv.FormatUint(op.BaseRevision, 10), "applied_revision": strconv.FormatUint(op.AppliedRevision, 10), "error": op.Error})
		})
	}
}

func (s *Server) registerDNSFilterRoutes(mux *http.ServeMux) {
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return s.securityHeadersMiddleware(s.trustedNetworksMiddleware(s.authMiddleware(next)))
	}
	mux.HandleFunc("GET /api/v1/dns-filter", gate(s.handleDNSFilterStatus))
	mux.HandleFunc("PUT /api/v1/dns-filter", gate(s.handleDNSFilterSave))
	mux.HandleFunc("POST /api/v1/dns-filter/refresh", gate(s.handleDNSFilterRefresh))
	mux.HandleFunc("POST /api/v1/dns-filter/check", gate(s.handleDNSFilterCheck))
	mux.HandleFunc("GET /api/v1/dns-filter/check", gate(s.handleDNSFilterCheck))
	mux.HandleFunc("GET /api/v1/dns-filter/profiles", gate(s.handleDNSFilterProfiles))
	mux.HandleFunc("GET /api/v1/dns-filter/operations", gate(s.handleDNSFilterOperations))
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
		dnsFilterContext
	}{status, s.dnsFilterContext()})
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
	if blockers := s.dnsFilterContext().Blockers; len(blockers) > 0 {
		http.Error(w, blockers[0], 409)
		return
	}
	op, err := s.dnsFilter.StartOperation(policy, false, auditActor(r.RemoteAddr))
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
	writeDNSActivityJSON(w, map[string]any{"updating": true, "operation": op})
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
	if blockers := s.dnsFilterContext().Blockers; len(blockers) > 0 {
		http.Error(w, blockers[0], 409)
		return
	}
	op, err := s.dnsFilter.StartOperation(status.Policy, true, auditActor(r.RemoteAddr))
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusAccepted)
	writeDNSActivityJSON(w, map[string]any{"updating": true, "operation": op})
}
func (s *Server) handleDNSFilterCheck(w http.ResponseWriter, r *http.Request) {
	if !s.requireDNSFilter(w) {
		return
	}
	var request struct {
		Domain   string `json:"domain"`
		DeviceIP string `json:"device_ip"`
	}
	if r.Method == http.MethodGet {
		request.Domain, request.DeviceIP = r.URL.Query().Get("domain"), r.URL.Query().Get("device_ip")
	} else if err := decodeJSON(w, r, &request); err != nil {
		http.Error(w, "Invalid domain check", 400)
		return
	}
	if request.DeviceIP != "" {
		ip, err := netip.ParseAddr(request.DeviceIP)
		if err != nil || !ip.Is4() {
			http.Error(w, "device_ip must be an IPv4 address", 422)
			return
		}
	}
	request.Domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(request.Domain), "."))
	cfg, leases := s.engine.GetCurrentConfig(), telemetry.CurrentDHCPLeases()
	var result dnsfilter.Check
	var err error
	if kind, _ := localDNSMatch(cfg, leases, request.Domain); kind != "" {
		status, statusErr := s.dnsFilter.Status(r.Context())
		if statusErr != nil {
			http.Error(w, "DNS filter status unavailable", 503)
			return
		}
		result = dnsfilter.Check{Domain: request.Domain, Action: "Local DNS record", Matches: []dnsfilter.Match{}, Healthy: status.Healthy, PolicyRevision: status.Policy.Revision, CheckedAt: time.Now().UTC()}
	} else {
		result, err = s.dnsFilter.Check(r.Context(), request.Domain)
	}
	if err != nil {
		http.Error(w, err.Error(), 422)
		return
	}
	writeDNSActivityJSON(w, explainDNSDomain(cfg, leases, result, request.DeviceIP, time.Now()))
}
