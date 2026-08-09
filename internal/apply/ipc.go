package apply

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
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
	// OpWGTunnelStatus is a read-only telemetry query: router-applyd runs
	// `wg show ... dump` and returns only sanitized fields. WireGuard dump
	// lines carry private and preshared keys; they must never cross the
	// privilege boundary.
	OpWGTunnelStatus OperationType = "WG_TUNNEL_STATUS"
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
	// VerifyWGClient scopes the expensive/availability-sensitive wg1 handshake
	// proof to transactions that actually changed wg1. A temporary office VPN
	// outage must never block confirmation of an unrelated LAN/Wi-Fi/trust
	// change merely because wg1 happens to be enabled.
	VerifyWGClient bool `json:"verify_wg_client,omitempty"`
	// TunnelInterface selects the interface for the read-only WG telemetry RPC.
	// The helper validates the name and returns a sanitized projection only.
	TunnelInterface string `json:"tunnel_interface,omitempty"`
	// DeferLastGood withholds the helper's last-good write until the caller
	// has committed the canonical store; the transaction is then finalized
	// with OpCommitConfirmed. This keeps last-good from ever advancing ahead
	// of the canonical SQLite state.
	DeferLastGood bool `json:"defer_last_good,omitempty"`
	// SkipWANVerify relaxes the commit-ack verification for the automatic
	// two-phase commit of routine saves and user-confirmed commits. The apply
	// phase already verifies WAN when required; an ISP flap after canonical
	// commit must not turn a correct local configuration into RecoveryRequired.
	SkipWANVerify bool `json:"skip_wan_verify,omitempty"`
}

// UnmarshalJSON keeps the IPC trust boundary self-contained for the one
// read-only operation that intentionally bypasses full SystemConfig validation.
// A compromised routerd must not be able to widen router-applyd's root `wg show`
// scope by claiming arbitrary interface names in an otherwise fake config.
// The appliance contract has exactly wg0 (server) and wg1 (outbound client).
//
// Because defining UnmarshalJSON bypasses Decoder.DisallowUnknownFields on the
// outer decoder, this method deliberately re-applies strict unknown-field and
// single-object checks to preserve the existing IPC parser contract.
func (r *ApplyRequest) UnmarshalJSON(data []byte) error {
	type plainApplyRequest ApplyRequest
	var decoded plainApplyRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("apply request contains trailing data")
	}

	if decoded.Op == OpWGTunnelStatus {
		serverInterface := strings.TrimSpace(decoded.Config.WireGuard.Interface)
		if serverInterface != "" && serverInterface != "wg0" {
			return fmt.Errorf("WireGuard telemetry server interface must be wg0")
		}
		clientInterface := strings.TrimSpace(decoded.Config.WGClient.Interface)
		if clientInterface != "" && clientInterface != "wg1" {
			return fmt.Errorf("WireGuard telemetry client interface must be wg1")
		}
		tunnelInterface := strings.TrimSpace(decoded.TunnelInterface)
		if tunnelInterface != "" && tunnelInterface != "wg0" && tunnelInterface != "wg1" {
			return fmt.Errorf("WireGuard telemetry interface must be wg0 or wg1")
		}
	}

	*r = ApplyRequest(decoded)
	return nil
}

// TunnelPeerStatus is the sanitized per-peer WireGuard projection. Public keys
// are identities, not secrets; private and preshared keys never cross the root
// helper boundary.
type TunnelPeerStatus struct {
	PublicKey     string `json:"public_key,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	LastHandshake int64  `json:"last_handshake"`
	RxBytes       int64  `json:"rx_bytes"`
	TxBytes       int64  `json:"tx_bytes"`
}

// TunnelStatus is the sanitized projection of a WireGuard dump.
type TunnelStatus struct {
	Interface     string             `json:"interface"`
	Endpoint      string             `json:"endpoint,omitempty"`
	LastHandshake int64              `json:"last_handshake"`
	RxBytes       int64              `json:"rx_bytes"`
	TxBytes       int64              `json:"tx_bytes"`
	Peers         []TunnelPeerStatus `json:"peers,omitempty"`
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
	// TunnelStatus is populated only by OpWGTunnelStatus responses.
	TunnelStatus *TunnelStatus `json:"tunnel_status,omitempty"`
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
