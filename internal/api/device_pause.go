package api

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

type devicePauseRequest struct {
	IP      string `json:"ip"`
	Seconds int    `json:"seconds"`
}

func (s *Server) handleGetDevicePauses(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	pauses, err := s.engine.DeviceInternetPauses(ctx)
	if err != nil {
		writeGatewayJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Device pause state unavailable"})
		return
	}
	writeGatewayJSON(w, http.StatusOK, map[string]interface{}{"pauses": pauses})
}

func (s *Server) handlePauseDevice(w http.ResponseWriter, r *http.Request) {
	var request devicePauseRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	pauses, err := s.engine.PauseDeviceInternet(ctx, request.IP, request.Seconds)
	if err != nil {
		writeGatewayJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	s.appendAudit("device.internet_paused", auditActor(r.RemoteAddr), map[string]string{"ip": request.IP, "duration_seconds": strconv.Itoa(request.Seconds)})
	writeGatewayJSON(w, http.StatusOK, map[string]interface{}{"success": true, "pauses": pauses})
}

func (s *Server) handleResumeDevice(w http.ResponseWriter, r *http.Request) {
	var request devicePauseRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	pauses, err := s.engine.ResumeDeviceInternet(ctx, request.IP)
	if err != nil {
		writeGatewayJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	s.appendAudit("device.internet_resumed", auditActor(r.RemoteAddr), map[string]string{"ip": request.IP})
	writeGatewayJSON(w, http.StatusOK, map[string]interface{}{"success": true, "pauses": pauses})
}
