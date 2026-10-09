package api

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

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
		summaries := make([]startup.BootSummary, 0, len(boots))
		for _, boot := range boots {
			summaries = append(summaries, startup.Summarize(boot, time.Now()))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"boots": summaries, "retained_boots": startup.MaxBoots,
			"capture_minutes": int(startup.Window.Minutes()),
			"generated_at":    time.Now().UTC(),
		})
	}
	mux.HandleFunc("GET /api/v1/startup/boots", s.securityHeadersMiddleware(s.trustedNetworksMiddleware(s.authMiddleware(handler))))
	detail := func(w http.ResponseWriter, r *http.Request) {
		boot, err := startup.LoadBoot(dataDir, r.PathValue("id"))
		if os.IsNotExist(err) {
			http.Error(w, "Startup capture not found or no longer retained", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "Startup diagnostics unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"boot": boot, "status": boot.CaptureStatus(time.Now()), "generated_at": time.Now().UTC()})
	}
	mux.HandleFunc("GET /api/v1/startup/boots/{id}", s.securityHeadersMiddleware(s.trustedNetworksMiddleware(s.authMiddleware(detail))))
}
