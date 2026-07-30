package api

import (
	"encoding/json"
	"net/http"

	networkinfo "github.com/vladimirperovic/minimalrouter/internal/network"
)

func (s *Server) handleDiscoverInterfaces(w http.ResponseWriter, _ *http.Request) {
	result, err := networkinfo.Discover()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}
