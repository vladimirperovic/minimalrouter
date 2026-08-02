package telemetry

import "github.com/vladimirperovic/minimalrouter/internal/storage"

type DHCPLease struct {
	ExpiresAt int64  `json:"expires_at"`
	MAC       string `json:"mac"`
	IPAddress string `json:"ip_address"`
	Hostname  string `json:"hostname,omitempty"`
}

type RuntimeStatus struct {
	Available             bool           `json:"available"`
	OS                    string         `json:"os"`
	Architecture          string         `json:"architecture"`
	WANConnected          bool           `json:"wan_connected"`
	PublicIP              string         `json:"public_ip,omitempty"`
	UptimeSeconds         int64          `json:"uptime_seconds,omitempty"`
	CPUCount              int            `json:"cpu_count"`
	CPULoadPercent        float64        `json:"cpu_load_percent,omitempty"`
	LoadAverage           []float64      `json:"load_average,omitempty"`
	MemoryUsedBytes       uint64         `json:"memory_used_bytes,omitempty"`
	MemoryTotalBytes      uint64         `json:"memory_total_bytes,omitempty"`
	DiskUsedBytes         uint64         `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes        uint64         `json:"disk_total_bytes,omitempty"`
	Storage               storage.Status `json:"storage"`
	RXBytes               uint64         `json:"rx_bytes,omitempty"`
	TXBytes               uint64         `json:"tx_bytes,omitempty"`
	TemperatureC          float64        `json:"temperature_c,omitempty"`
	TimeSynchronized      bool           `json:"time_synchronized"`
	ConntrackCount        uint64         `json:"conntrack_count,omitempty"`
	ConntrackMax          uint64         `json:"conntrack_max,omitempty"`
	ConntrackUsagePercent float64        `json:"conntrack_usage_percent,omitempty"`
	DHCPLeases            []DHCPLease    `json:"dhcp_leases"`
	WireguardActivePeers  int            `json:"wireguard_active_peers,omitempty"`
}
