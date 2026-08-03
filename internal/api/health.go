package api

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/gateway"
	"github.com/vladimirperovic/minimalrouter/internal/health"
	"github.com/vladimirperovic/minimalrouter/internal/telemetry"
)

func (s *Server) RegisterHealthRoutes(mux *http.ServeMux) {
	sh := s.securityHeadersMiddleware
	mux.HandleFunc("GET /api/v1/health", sh(s.authMiddleware(s.handleGetHealth)))
}

func (s *Server) handleGetHealth(w http.ResponseWriter, _ *http.Request) {
	cfg := s.engine.GetCurrentConfig()
	dataDir := os.Getenv("MINIMALROUTER_DATA_DIR")
	if dataDir == "" {
		dataDir = "/var/lib/minimalrouter"
	}

	runtimeStatus := telemetry.RuntimeSnapshot(cfg.WAN.Interface, cfg.RuntimeLANInterface(), dataDir)
	facts := health.InspectRuntimeFacts(cfg)

	gatewaySummary := gateway.Summary{}
	gatewayConfigured := false
	if monitor := s.configuredGatewayMonitor(); monitor != nil {
		gatewayConfigured = true
		gatewaySummary = monitor.Summary()
	}

	s.mu.RLock()
	updateTrustConfigured := len(s.firmwareKey) == ed25519.PublicKeySize
	s.mu.RUnlock()

	var lastBackupAt *time.Time
	store := s.store
	if store == nil && s.engine != nil {
		store = s.engine.GetStore()
	}
	if store != nil {
		if events, err := store.ListAuditEvents(500); err == nil {
			for _, event := range events {
				if event.EventType != "api.mutation" || event.Details["path"] != "/api/v1/backup/export" {
					continue
				}
				if !strings.HasPrefix(event.Details["status"], "2") {
					continue
				}
				at := event.Timestamp.UTC()
				lastBackupAt = &at
				break
			}
		}
	}

	var dnsResolves *bool
	var dnsError string
	if cfg.DHCP.Enabled || cfg.DHCP.DNSEnabled {
		resolves, err := health.ProbeFunctionalDNS(4 * time.Second)
		dnsResolves = &resolves
		if err != nil {
			dnsError = err.Error()
		}
	}

	snapshot := health.Build(health.Input{
		Config:                cfg,
		Runtime:               runtimeStatus,
		Engine:                s.engine.GetStatus(),
		Gateway:               gatewaySummary,
		GatewayConfigured:     gatewayConfigured,
		UpdateTrustConfigured: updateTrustConfigured,
		Facts:                 facts,
		LastBackupAt:          lastBackupAt,
		DNSResolves:           dnsResolves,
		DNSError:              dnsError,
		Now:                   time.Now().UTC(),
	})

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(snapshot)
}
