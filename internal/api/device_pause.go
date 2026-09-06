package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
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
		writeDevicePauseError(w, err)
		return
	}
	writeGatewayJSON(w, http.StatusOK, map[string]interface{}{"pauses": pauses})
}

func (s *Server) handlePauseDevice(w http.ResponseWriter, r *http.Request) {
	var request devicePauseRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeDevicePauseRequestError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	pauses, err := s.engine.PauseDeviceInternet(ctx, request.IP, request.Seconds)
	if err != nil {
		writeDevicePauseError(w, err)
		return
	}
	s.appendAudit("device.internet_paused", auditActor(r.RemoteAddr), map[string]string{"ip": request.IP, "duration_seconds": strconv.Itoa(request.Seconds)})
	writeGatewayJSON(w, http.StatusOK, map[string]interface{}{"success": true, "pauses": pauses})
}

func (s *Server) handleResumeDevice(w http.ResponseWriter, r *http.Request) {
	var request devicePauseRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeDevicePauseRequestError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if request.Seconds != 0 {
		writeDevicePauseError(w, &apply.ActionError{Code: apply.ActionInvalid, Message: "resume does not accept a pause duration"})
		return
	}
	pauses, err := s.engine.ResumeDeviceInternet(ctx, request.IP)
	if err != nil {
		writeDevicePauseError(w, err)
		return
	}
	s.appendAudit("device.internet_resumed", auditActor(r.RemoteAddr), map[string]string{"ip": request.IP})
	writeGatewayJSON(w, http.StatusOK, map[string]interface{}{"success": true, "pauses": pauses})
}

func writeDevicePauseError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusServiceUnavailable, apply.ActionUnavailable, "Device pause helper unavailable; refresh status before retrying"
	var actionErr *apply.ActionError
	if errors.As(err, &actionErr) {
		code, message = actionErr.Code, actionErr.Message
		switch actionErr.Code {
		case apply.ActionInvalid:
			status = http.StatusBadRequest
		case apply.ActionConflict:
			status = http.StatusConflict
		case apply.ActionFailed:
			status = http.StatusInternalServerError
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusGatewayTimeout
	}
	writeGatewayJSON(w, status, map[string]interface{}{"error": message, "code": code, "recovery_required": code == apply.ActionRecoveryRequired})
}

func writeDevicePauseRequestError(w http.ResponseWriter, err error) {
	var oversized *http.MaxBytesError
	if errors.As(err, &oversized) {
		writeGatewayJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "Device pause request exceeds size limit", "code": apply.ActionInvalid})
		return
	}
	writeDevicePauseError(w, &apply.ActionError{Code: apply.ActionInvalid, Message: "Invalid device pause request"})
}
