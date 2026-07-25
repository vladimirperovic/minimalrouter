package apply

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// DefaultSocketPath defines the location of the privileged Unix domain socket.
const DefaultSocketPath = "/run/minimalrouter/applyd.sock"

// OperationType enumerates the allowlisted privileged RPC operations.
type OperationType string

const (
	OpApplyAll        OperationType = "APPLY_ALL"
	OpLoadNftables    OperationType = "LOAD_NFTABLES"
	OpReloadService   OperationType = "RELOAD_SERVICE"
	OpRestoreSnapshot OperationType = "RESTORE_SNAPSHOT"
)

// ApplyRequest represents a size-limited RPC payload sent from routerd to router-applyd.
type ApplyRequest struct {
	ID          string              `json:"id"`
	Op          OperationType       `json:"op"`
	Revision    config.Revision     `json:"revision"`
	Config      config.SystemConfig `json:"config"`
	Nftables    string              `json:"nftables"`
	PPPoE       string              `json:"pppoe,omitempty"`
	Dnsmasq     string              `json:"dnsmasq,omitempty"`
	Hostapd     string              `json:"hostapd,omitempty"`
	ServiceName string              `json:"service_name,omitempty"`
}

// ApplyResponse represents the structured execution output from router-applyd.
type ApplyResponse struct {
	ID        string `json:"id"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
	Logs      string `json:"logs,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// sendIPCRequest connects to router-applyd over Unix domain socket,
// sends the apply request, and returns the response.
// Returns error if socket is unavailable (e.g., development mode without router-applyd running).
func sendIPCRequest(req ApplyRequest) (*ApplyResponse, error) {
	conn, err := net.DialTimeout("unix", DefaultSocketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to router-applyd at %s: %w", DefaultSocketPath, err)
	}
	defer conn.Close()

	// Set read/write deadline to prevent hanging
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("failed to send IPC request: %w", err)
	}

	var resp ApplyResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to read IPC response: %w", err)
	}

	return &resp, nil
}
