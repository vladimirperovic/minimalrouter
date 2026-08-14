package api

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/gateway"
)

var gatewayMonitorRegistry sync.Map // map[*Server]*gateway.Monitor

// ConfigureGatewayMonitor attaches the read-only WAN quality subsystem without
// expanding Server's core configuration/recovery responsibilities.
func (s *Server) ConfigureGatewayMonitor(monitor *gateway.Monitor) {
	if monitor == nil {
		gatewayMonitorRegistry.Delete(s)
		return
	}
	gatewayMonitorRegistry.Store(s, monitor)
}

func (s *Server) configuredGatewayMonitor() *gateway.Monitor {
	monitor, _ := gatewayMonitorRegistry.Load(s)
	result, _ := monitor.(*gateway.Monitor)
	return result
}

// RegisterGatewayRoutes keeps the optional telemetry subsystem separate from
// the canonical router API route table. All routes still pass through the same
// authentication, read-only-session, Origin and CSRF protections.
func (s *Server) RegisterGatewayRoutes(mux *http.ServeMux) {
	sh := s.securityHeadersMiddleware
	gate := func(next http.HandlerFunc) http.HandlerFunc {
		return sh(s.trustedNetworksMiddleware(s.authMiddleware(next)))
	}
	mux.HandleFunc("GET /api/v1/gateway/summary", gate(s.handleGetGatewaySummary))
	mux.HandleFunc("GET /api/v1/gateway/history", gate(s.handleGetGatewayHistory))
	mux.HandleFunc("GET /api/v1/gateway/settings", gate(s.handleGetGatewaySettings))
	mux.HandleFunc("PUT /api/v1/gateway/settings", gate(s.handlePutGatewaySettings))
}

func (s *Server) handleGetGatewaySummary(w http.ResponseWriter, _ *http.Request) {
	monitor := s.configuredGatewayMonitor()
	if monitor == nil {
		http.Error(w, "Gateway monitoring is unavailable", http.StatusServiceUnavailable)
		return
	}
	writeGatewayJSON(w, http.StatusOK, monitor.Summary())
}

func (s *Server) handleGetGatewayHistory(w http.ResponseWriter, r *http.Request) {
	monitor := s.configuredGatewayMonitor()
	if monitor == nil {
		http.Error(w, "Gateway monitoring is unavailable", http.StatusServiceUnavailable)
		return
	}

	windowName := r.URL.Query().Get("window")
	if windowName == "" {
		windowName = "1h"
	}
	var window time.Duration
	var maxPoints int
	switch windowName {
	case "1h":
		window, maxPoints = time.Hour, 120
	case "24h":
		window, maxPoints = 24*time.Hour, 288
	case "7d":
		window, maxPoints = 7*24*time.Hour, 336
	case "30d":
		window, maxPoints = 30*24*time.Hour, 360
	default:
		http.Error(w, "window must be one of 1h, 24h, 7d, or 30d", http.StatusBadRequest)
		return
	}
	points, err := monitor.History(window, maxPoints)
	if err != nil {
		http.Error(w, "Gateway history is unavailable", http.StatusServiceUnavailable)
		return
	}
	writeGatewayJSON(w, http.StatusOK, map[string]interface{}{"window": windowName, "points": points})
}

func (s *Server) handleGetGatewaySettings(w http.ResponseWriter, _ *http.Request) {
	monitor := s.configuredGatewayMonitor()
	if monitor == nil {
		http.Error(w, "Gateway monitoring is unavailable", http.StatusServiceUnavailable)
		return
	}
	settings, err := monitor.Settings()
	if err != nil {
		http.Error(w, "Gateway settings are unavailable", http.StatusServiceUnavailable)
		return
	}
	writeGatewayJSON(w, http.StatusOK, settings)
}

func (s *Server) handlePutGatewaySettings(w http.ResponseWriter, r *http.Request) {
	monitor := s.configuredGatewayMonitor()
	if monitor == nil {
		http.Error(w, "Gateway monitoring is unavailable", http.StatusServiceUnavailable)
		return
	}
	var settings gateway.Settings
	if err := decodeJSON(w, r, &settings); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if err := monitor.UpdateSettings(settings); err != nil {
		writeGatewayJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeGatewayJSON(w, http.StatusOK, settings)
}

func writeGatewayJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
