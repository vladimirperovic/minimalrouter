package telemetry

type DHCPLease struct {
	ExpiresAt int64  `json:"expires_at"`
	MAC       string `json:"mac"`
	IPAddress string `json:"ip_address"`
	Hostname  string `json:"hostname,omitempty"`
}

type NetworkInterfaceStatus struct {
	Name      string `json:"name"`
	MAC       string `json:"mac,omitempty"`
	State     string `json:"state,omitempty"`
	Carrier   bool   `json:"carrier"`
	SpeedMbps int    `json:"speed_mbps,omitempty"`
	Driver    string `json:"driver,omitempty"`
	BusPath   string `json:"bus_path,omitempty"`
	Kind      string `json:"kind"`
	Loopback  bool   `json:"loopback"`
}

type RuntimeStatus struct {
	Available        bool                     `json:"available"`
	OS               string                   `json:"os"`
	Architecture     string                   `json:"architecture"`
	WANConnected     bool                     `json:"wan_connected"`
	PublicIP         string                   `json:"public_ip,omitempty"`
	UptimeSeconds    int64                    `json:"uptime_seconds,omitempty"`
	CPUCount         int                      `json:"cpu_count"`
	CPULoadPercent   float64                  `json:"cpu_load_percent,omitempty"`
	LoadAverage      []float64                `json:"load_average,omitempty"`
	MemoryUsedBytes  uint64                   `json:"memory_used_bytes,omitempty"`
	MemoryTotalBytes uint64                   `json:"memory_total_bytes,omitempty"`
	DiskUsedBytes    uint64                   `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes   uint64                   `json:"disk_total_bytes,omitempty"`
	RXBytes          uint64                   `json:"rx_bytes,omitempty"`
	TXBytes          uint64                   `json:"tx_bytes,omitempty"`
	TemperatureC     float64                  `json:"temperature_c,omitempty"`
	DHCPLeases       []DHCPLease              `json:"dhcp_leases"`
	Interfaces       []NetworkInterfaceStatus `json:"interfaces"`
}
