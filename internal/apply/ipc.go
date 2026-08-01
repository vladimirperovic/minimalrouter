package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// DefaultSocketPath defines the location of the privileged Unix domain socket.
const DefaultSocketPath = "/run/minimalrouter/apply.sock"

const (
	ProtocolVersion    = 1
	MaxRequestBytes    = 2 << 20
	MaxResponseBytes   = 256 << 10
	defaultIPCDeadline = 2 * time.Minute
)

// OperationType enumerates the allowlisted privileged RPC operations.
type OperationType string

const (
	OpApplyAll        OperationType = "APPLY_ALL"
	OpConfirm         OperationType = "CONFIRM"
	OpCommitConfirmed OperationType = "COMMIT_CONFIRMED"
	OpReconcile       OperationType = "RECONCILE"
	OpLoadNftables    OperationType = "LOAD_NFTABLES"
	OpReloadService   OperationType = "RELOAD_SERVICE"
	OpRestoreSnapshot OperationType = "RESTORE_SNAPSHOT"
)

// ApplyRequest represents a size-limited RPC payload sent from routerd to router-applyd.
type ApplyRequest struct {
	Version             int                 `json:"version"`
	ID                  string              `json:"id"`
	Op                  OperationType       `json:"op"`
	Revision            config.Revision     `json:"revision"`
	Config              config.SystemConfig `json:"config"`
	Nftables            string              `json:"nftables"`
	PPPoEPeer           string              `json:"pppoe_peer,omitempty"`
	PPPoESecret         string              `json:"pppoe_secret,omitempty"`
	Dnsmasq             string              `json:"dnsmasq,omitempty"`
	Hostapd             string              `json:"hostapd,omitempty"`
	WireGuard           string              `json:"wireguard,omitempty"`
	ServiceName         string              `json:"service_name,omitempty"`
	RequireConfirmation bool                `json:"require_confirmation,omitempty"`
}

// ApplyResponse represents the structured execution output from router-applyd.
type ApplyResponse struct {
	ID               string `json:"id"`
	Success          bool   `json:"success"`
	Error            string `json:"error,omitempty"`
	Logs             string `json:"logs,omitempty"`
	Verified         bool   `json:"verified"`
	RolledBack       bool   `json:"rolled_back,omitempty"`
	RecoveryRequired bool   `json:"recovery_required,omitempty"`
	Timestamp        int64  `json:"timestamp"`
}

// Validate rejects contradictory privileged outcomes. The management plane must
// never interpret a malformed or internally inconsistent response as proof that
// either the candidate or the previous runtime is active.
func (r ApplyResponse) Validate() error {
	if r.Success {
		if !r.Verified {
			return fmt.Errorf("successful response is not verified")
		}
		if r.RolledBack {
			return fmt.Errorf("successful response also reports rollback")
		}
		if r.RecoveryRequired {
			return fmt.Errorf("successful response also requires recovery")
		}
		return nil
	}
	if r.Verified {
		return fmt.Errorf("failed response cannot report verified success")
	}
	if r.RolledBack && r.RecoveryRequired {
		return fmt.Errorf("response cannot report both rollback and recovery required")
	}
	return nil
}

// Client is the only interface the unprivileged control plane uses to request
// privileged configuration application.
type Client interface {
	Apply(ctx context.Context, req ApplyRequest) (*ApplyResponse, error)
}

type UnixClient struct {
	SocketPath string
}

func NewUnixClient(socketPath string) *UnixClient {
	if socketPath == "" {
		socketPath = DefaultSocketPath
	}
	return &UnixClient{SocketPath: socketPath}
}

func (c *UnixClient) Apply(ctx context.Context, req ApplyRequest) (*ApplyResponse, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to router-applyd at %s: %w", c.SocketPath, err)
	}
	defer conn.Close()

	deadline := time.Now().Add(defaultIPCDeadline)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("failed to set IPC deadline: %w", err)
	}

	req.Version = ProtocolVersion
	if err := json.NewEncoder(conn).Encode(&req); err != nil {
		return nil, fmt.Errorf("failed to send IPC request: %w", err)
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, fmt.Errorf("router-applyd IPC did not return a Unix connection")
	}
	// Signal the end of the one-object request without closing the read side.
	// The helper intentionally verifies EOF to reject JSON smuggling/trailing
	// objects before it performs a privileged operation.
	if err := unixConn.CloseWrite(); err != nil {
		return nil, fmt.Errorf("failed to finalize IPC request: %w", err)
	}

	var resp ApplyResponse
	decoder := json.NewDecoder(io.LimitReader(conn, MaxResponseBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to read IPC response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("router-applyd returned trailing data")
	}
	if resp.ID != req.ID {
		return nil, fmt.Errorf("router-applyd response ID mismatch")
	}

	return &resp, nil
}
