package apply

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// MaxServiceActionResponseBytes covers 512 IPv4 entries with int64 expiries.
const MaxServiceActionResponseBytes = 64 << 10
const MaxDevicePauses = 512

const (
	ActionInvalid          = "invalid_request"
	ActionConflict         = "conflict"
	ActionUnavailable      = "unavailable"
	ActionRecoveryRequired = "recovery_required"
	ActionFailed           = "failed"
)

// ActionError carries a stable IPC/API classification without parsing messages.
type ActionError struct {
	Code    string
	Message string
}

func (e *ActionError) Error() string         { return e.Message }
func actionError(code, message string) error { return &ActionError{Code: code, Message: message} }

const ServiceActionSocketPath = "/run/minimalrouter/actions.sock"

const (
	ServiceActionWANReconnect     = "wan-reconnect"
	ServiceActionDNSDHCPRestart   = "dns-dhcp-restart"
	ServiceActionWireGuardRestart = "wireguard-restart"
	DeviceActionPause             = "device-pause"
	DeviceActionResume            = "device-resume"
	DeviceActionStatus            = "device-pause-status"
)

type DevicePause struct {
	IP        string `json:"ip"`
	UntilUnix int64  `json:"until_unix"`
}

type serviceActionRequest struct {
	Action  string `json:"action"`
	IP      string `json:"ip,omitempty"`
	Seconds int    `json:"seconds,omitempty"`
}

type serviceActionResponse struct {
	Success bool          `json:"success"`
	Error   string        `json:"error,omitempty"`
	Code    string        `json:"code,omitempty"`
	Pauses  []DevicePause `json:"pauses,omitempty"`
}

func validServiceAction(action string) bool {
	switch action {
	case ServiceActionWANReconnect, ServiceActionDNSDHCPRestart, ServiceActionWireGuardRestart:
		return true
	default:
		return false
	}
}

func callActionSocket(ctx context.Context, request serviceActionRequest) (serviceActionResponse, error) {
	return callServiceActionSocket(ctx, ServiceActionSocketPath, request)
}

func callServiceActionSocket(ctx context.Context, socketPath string, request serviceActionRequest) (serviceActionResponse, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return serviceActionResponse{}, fmt.Errorf("cannot connect to privileged action socket: %w", err)
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline := time.Now().Add(90 * time.Second)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return serviceActionResponse{}, err
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return serviceActionResponse{}, fmt.Errorf("send privileged action: %w", err)
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return serviceActionResponse{}, fmt.Errorf("privileged action IPC did not return a Unix connection")
	}
	if err := unixConn.CloseWrite(); err != nil {
		return serviceActionResponse{}, fmt.Errorf("finalize privileged action request: %w", err)
	}
	response, err := decodeServiceActionResponse(conn)
	if err != nil {
		if ctx.Err() != nil {
			return response, ctx.Err()
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return response, context.DeadlineExceeded
		}
	}
	return response, err
}

func decodeServiceActionResponse(reader io.Reader) (serviceActionResponse, error) {
	var response serviceActionResponse
	data, err := io.ReadAll(io.LimitReader(reader, MaxServiceActionResponseBytes+1))
	if err != nil {
		return response, fmt.Errorf("read privileged action response: %w", err)
	}
	if len(data) > MaxServiceActionResponseBytes {
		return response, errors.New("privileged action response exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return response, fmt.Errorf("read privileged action response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return response, errors.New("privileged action helper returned trailing data")
	}
	if len(response.Pauses) > MaxDevicePauses {
		return serviceActionResponse{}, errors.New("privileged action pause count exceeds limit")
	}
	if !response.Success {
		if response.Error == "" {
			response.Error = "privileged action failed"
		}
		if response.Code == "" {
			response.Code = ActionFailed
		}
		return response, actionError(response.Code, response.Error)
	}
	if response.Error != "" || response.Code != "" {
		return serviceActionResponse{}, errors.New("contradictory privileged action response")
	}
	seen := make(map[string]bool, len(response.Pauses))
	for _, pause := range response.Pauses {
		ip := net.ParseIP(pause.IP)
		if ip == nil || ip.To4() == nil || ip.To4().String() != pause.IP || pause.UntilUnix < 0 || seen[pause.IP] {
			return serviceActionResponse{}, errors.New("invalid privileged action pause state")
		}
		seen[pause.IP] = true
	}
	if response.Pauses == nil {
		response.Pauses = []DevicePause{}
	}
	return response, nil
}

func runServiceActionIPC(ctx context.Context, action string) error {
	_, err := callActionSocket(ctx, serviceActionRequest{Action: action})
	return err
}

func (e *Engine) actionAllowed() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return !e.applying && !e.recoveryRequired && e.pending == nil
}

// RunServiceAction serializes manual service recovery with configuration
// transactions and refuses it while the canonical runtime is pending or in
// recovery. The root helper independently validates the fixed action ID and
// uses its trusted last-good config.
func (e *Engine) RunServiceAction(ctx context.Context, action string) error {
	if !validServiceAction(action) {
		return actionError(ActionInvalid, "unsupported service action")
	}
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	if !e.actionAllowed() {
		return actionError(ActionConflict, "service recovery is unavailable while configuration recovery or confirmation is active")
	}
	return runServiceActionIPC(ctx, action)
}

// validateDeviceIP rejects a device address before it ever reaches the
// privileged socket. router-applyd validates again from its own last-good
// configuration and remains the authority; this is the caller-side half of that
// check, so a malformed address is refused at the API boundary with a useful
// message instead of travelling further into the privileged path.
func (e *Engine) validateDeviceIP(value string) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil || ip.To4() == nil {
		return "", actionError(ActionInvalid, "device address must be IPv4")
	}
	cfg := e.GetCurrentConfig()
	_, lan, err := net.ParseCIDR(strings.TrimSpace(cfg.LAN.CIDR))
	if err != nil || lan == nil {
		return "", actionError(ActionUnavailable, "trusted LAN range is unavailable")
	}
	if !lan.Contains(ip.To4()) {
		return "", actionError(ActionInvalid, "device address is outside the trusted LAN")
	}
	if ip.Equal(net.ParseIP(strings.TrimSpace(cfg.LAN.IPAddress))) {
		return "", actionError(ActionInvalid, "router LAN address cannot be paused")
	}
	return ip.To4().String(), nil
}

func (e *Engine) PauseDeviceInternet(ctx context.Context, ip string, seconds int) ([]DevicePause, error) {
	if seconds != 0 && seconds != 15*60 && seconds != 60*60 {
		return nil, actionError(ActionInvalid, "pause duration must be 15 minutes, 1 hour, or until resumed")
	}
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	address, err := e.validateDeviceIP(ip)
	if err != nil {
		return nil, err
	}
	if !e.actionAllowed() {
		return nil, actionError(ActionConflict, "device pause is unavailable while configuration recovery or confirmation is active")
	}
	response, err := callActionSocket(ctx, serviceActionRequest{Action: DeviceActionPause, IP: address, Seconds: seconds})
	return response.Pauses, err
}

func (e *Engine) ResumeDeviceInternet(ctx context.Context, ip string) ([]DevicePause, error) {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	address, err := e.validateDeviceIP(ip)
	if err != nil {
		return nil, err
	}
	if !e.actionAllowed() {
		return nil, actionError(ActionConflict, "device resume is unavailable while configuration recovery or confirmation is active")
	}
	response, err := callActionSocket(ctx, serviceActionRequest{Action: DeviceActionResume, IP: address})
	return response.Pauses, err
}

func (e *Engine) DeviceInternetPauses(ctx context.Context) ([]DevicePause, error) {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !e.actionAllowed() {
		return nil, actionError(ActionConflict, "device pause status is unavailable while configuration recovery or confirmation is active")
	}
	response, err := callActionSocket(ctx, serviceActionRequest{Action: DeviceActionStatus})
	return response.Pauses, err
}
