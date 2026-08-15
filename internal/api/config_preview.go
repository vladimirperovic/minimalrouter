package api

import (
	"net/http"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func (s *Server) handleConfigPreview(w http.ResponseWriter, r *http.Request) {
	var candidate config.SystemConfig
	if err := decodeJSON(w, r, &candidate); err != nil {
		writeGatewayJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid configuration payload."})
		return
	}
	preview, err := apply.PreviewTransition(s.engine.GetCurrentConfig(), candidate)
	if err != nil {
		writeGatewayJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeGatewayJSON(w, http.StatusOK, preview)
}
