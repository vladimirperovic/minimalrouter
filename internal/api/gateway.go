package api

import (
	"encoding/json"
	"net"
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
	mux.HandleFunc("GET /api/v1/gateway/summary", sh(s.authMiddleware(s.handleGetGatewaySummary)))
	mux.HandleFunc("GET /api/v1/gateway/history", sh(s.authMiddleware(s.handleGetGatewayHistory)))
	mux.HandleFunc("GET /api/v1/gateway/settings", sh(s.authMiddleware(s.handleGetGatewaySettings)))
	mux.HandleFunc("PUT /api/v1/gateway/settings", sh(s.authMiddleware(s.handlePutGatewaySettings)))
	mux.HandleFunc("POST /api/v1/network/wol", sh(s.authMiddleware(s.handleWakeOnLAN)))
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
	default:
		http.Error(w, "window must be one of 1h, 24h, or 7d", http.StatusBadRequest)
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

func (s *Server) handleWakeOnLAN(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MAC string `json:"mac"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	hwAddr, err := net.ParseMAC(req.MAC)
	if err != nil {
		http.Error(w, "Invalid MAC address", http.StatusBadRequest)
		return
	}
	packet := make([]byte, 102)
	for i := 0; i < 6; i++ {
		packet[i] = 0xFF
	}
	for i := 1; i <= 16; i++ {
		copy(packet[i*6:], hwAddr)
	}
	addr, err := net.ResolveUDPAddr("udp", "255.255.255.255:9")
	if err != nil {
		http.Error(w, "Wake-on-LAN address unavailable", http.StatusInternalServerError)
		return
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		http.Error(w, "Wake-on-LAN socket unavailable", http.StatusInternalServerError)
		return
	}
	defer conn.Close()
	if err := conn.SetWriteBuffer(len(packet)); err != nil {
		http.Error(w, "Wake-on-LAN socket unavailable", http.StatusInternalServerError)
		return
	}
	if _, err := conn.Write(packet); err != nil {
		http.Error(w, "Wake-on-LAN send failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeGatewayJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
