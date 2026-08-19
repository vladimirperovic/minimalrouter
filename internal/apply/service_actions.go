package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

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
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", ServiceActionSocketPath)
	if err != nil {
		return serviceActionResponse{}, fmt.Errorf("cannot connect to privileged action socket: %w", err)
	}
	defer conn.Close()
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
	var response serviceActionResponse
	decoder := json.NewDecoder(io.LimitReader(conn, 16384))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return serviceActionResponse{}, fmt.Errorf("read privileged action response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return serviceActionResponse{}, fmt.Errorf("privileged action helper returned trailing data")
	}
	if !response.Success {
		if response.Error == "" {
			response.Error = "privileged action failed"
		}
		return response, fmt.Errorf("%s", response.Error)
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
		return fmt.Errorf("unsupported service action")
	}
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	if !e.actionAllowed() {
		return fmt.Errorf("service recovery is unavailable while configuration recovery or confirmation is active")
	}
	return runServiceActionIPC(ctx, action)
}

func (e *Engine) PauseDeviceInternet(ctx context.Context, ip string, seconds int) ([]DevicePause, error) {
	if seconds != 0 && seconds != 15*60 && seconds != 60*60 {
		return nil, fmt.Errorf("pause duration must be 15 minutes, 1 hour, or until resumed")
	}
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	if !e.actionAllowed() {
		return nil, fmt.Errorf("device pause is unavailable while configuration recovery or confirmation is active")
	}
	response, err := callActionSocket(ctx, serviceActionRequest{Action: DeviceActionPause, IP: ip, Seconds: seconds})
	return response.Pauses, err
}

func (e *Engine) ResumeDeviceInternet(ctx context.Context, ip string) ([]DevicePause, error) {
	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	if !e.actionAllowed() {
		return nil, fmt.Errorf("device resume is unavailable while configuration recovery or confirmation is active")
	}
	response, err := callActionSocket(ctx, serviceActionRequest{Action: DeviceActionResume, IP: ip})
	return response.Pauses, err
}

func (e *Engine) DeviceInternetPauses(ctx context.Context) ([]DevicePause, error) {
	response, err := callActionSocket(ctx, serviceActionRequest{Action: DeviceActionStatus})
	return response.Pauses, err
}
