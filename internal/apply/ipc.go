package apply

import "github.com/vladimirperovic/minimalrouter/internal/config"

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
	ID         string               `json:"id"`
	Op         OperationType        `json:"op"`
	Revision   config.Revision      `json:"revision"`
	Config     config.SystemConfig  `json:"config"`
	Nftables   string               `json:"nftables"`
	PPPoE      string               `json:"pppoe,omitempty"`
	Dnsmasq    string               `json:"dnsmasq,omitempty"`
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
