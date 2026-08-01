package health

import "github.com/vladimirperovic/minimalrouter/internal/config"

type RuntimeFacts struct {
	Available            bool   `json:"available"`
	RouterdHealthy       bool   `json:"routerd_healthy"`
	ApplydHealthy        bool   `json:"applyd_healthy"`
	ApplySocketAvailable bool   `json:"apply_socket_available"`
	DnsmasqStarted       bool   `json:"dnsmasq_started"`
	PPPoEStarted         bool   `json:"pppoe_started"`
	WireGuardInterfaceUp bool   `json:"wireguard_interface_up"`
	UpdateStateAvailable bool   `json:"update_state_available"`
	UpdateCurrent        string `json:"update_current,omitempty"`
	UpdatePrevious       string `json:"update_previous,omitempty"`
	UpdatePending        string `json:"update_pending,omitempty"`
}

func InspectRuntimeFacts(cfg config.SystemConfig) RuntimeFacts {
	return inspectRuntimeFacts(cfg)
}
