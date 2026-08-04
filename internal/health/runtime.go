package health

import "github.com/vladimirperovic/minimalrouter/internal/config"

type RuntimeFacts struct {
	Available                  bool `json:"available"`
	RouterdHealthy             bool `json:"routerd_healthy"`
	ApplydHealthy              bool `json:"applyd_healthy"`
	ApplySocketAvailable       bool `json:"apply_socket_available"`
	DnsmasqStarted             bool `json:"dnsmasq_started"`
	PPPoEStarted               bool `json:"pppoe_started"`
	WireGuardInterfaceUp       bool `json:"wireguard_interface_up"`
	WireGuardClientInterfaceUp bool `json:"wireguard_client_interface_up"`
	// WireGuardClientLastHandshake is the epoch of the last completed wg1
	// handshake, or 0 when the tunnel never handshaked (interface up is not
	// the same as connected).
	WireGuardClientLastHandshake int64  `json:"wireguard_client_last_handshake,omitempty"`
	UpdateStateAvailable         bool   `json:"update_state_available"`
	UpdateCurrent                string `json:"update_current,omitempty"`
	UpdatePrevious               string `json:"update_previous,omitempty"`
	UpdatePending                string `json:"update_pending,omitempty"`
}

func InspectRuntimeFacts(cfg config.SystemConfig) RuntimeFacts {
	return inspectRuntimeFacts(cfg)
}
