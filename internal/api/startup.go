package api

import (
	"encoding/json"
	"net/http"

	"github.com/vladimirperovic/minimalrouter/internal/startup"
)

// RegisterStartupRoutes exposes read-only boot diagnostics through the same
// trusted-network, authenticated and security-header boundaries as the rest of
// the management API. The data directory is captured by value so no mutable
// privilege or global path is introduced into Server.
func (s *Server) RegisterStartupRoutes(mux *http.ServeMux, dataDir string) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		boots, err := startup.Load(dataDir)
		if err != nil {
			http.Error(w, `{"error":"startup diagnostics unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"boots": boots, "retained_boots": startup.MaxBoots,
			"capture_minutes": int(startup.Window.Minutes()),
		})
	}
	mux.HandleFunc("GET /api/v1/startup/boots", s.securityHeadersMiddleware(s.trustedNetworksMiddleware(s.authMiddleware(handler))))
}
