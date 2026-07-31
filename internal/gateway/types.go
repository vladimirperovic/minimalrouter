package gateway

import "time"

// HealthState is the operator-facing quality classification for the active WAN.
type HealthState string

const (
	StateUnknown  HealthState = "unknown"
	StateHealthy  HealthState = "healthy"
	StateDegraded HealthState = "degraded"
	StateOffline  HealthState = "offline"
	StateFlapping HealthState = "flapping"
)

// TargetResult is one bounded probe result for a configured IPv4 target.
type TargetResult struct {
	Target            string  `json:"target"`
	Reachable         bool    `json:"reachable"`
	PacketsSent       int     `json:"packets_sent"`
	PacketsReceived   int     `json:"packets_received"`
	PacketLossPercent float64 `json:"packet_loss_percent"`
	LatencyMS         float64 `json:"latency_ms,omitempty"`
	JitterMS          float64 `json:"jitter_ms,omitempty"`
	Error             string  `json:"error,omitempty"`
}

// LinkStatus captures the local PPPoE data-plane state without making changes.
type LinkStatus struct {
	Connected bool   `json:"connected"`
	Interface string `json:"interface"`
	LocalIP   string `json:"local_ip,omitempty"`
	PeerIP    string `json:"peer_ip,omitempty"`
}

// Sample is the persisted, bounded gateway quality record.
type Sample struct {
	Timestamp         time.Time      `json:"timestamp"`
	State             HealthState    `json:"state"`
	Link              LinkStatus     `json:"link"`
	Targets           []TargetResult `json:"targets"`
	PeerProbe         *TargetResult  `json:"peer_probe,omitempty"`
	LatencyMS         float64        `json:"latency_ms,omitempty"`
	JitterMS          float64        `json:"jitter_ms,omitempty"`
	PacketLossPercent float64        `json:"packet_loss_percent"`
	PPPoEUptime       int64          `json:"pppoe_uptime_seconds"`
}

// Summary is the compact current status returned to the dashboard.
type Summary struct {
	Available         bool           `json:"available"`
	Enabled           bool           `json:"enabled"`
	State             HealthState    `json:"state"`
	Timestamp         time.Time      `json:"timestamp,omitempty"`
	Link              LinkStatus     `json:"link"`
	Targets           []TargetResult `json:"targets,omitempty"`
	PeerProbe         *TargetResult  `json:"peer_probe,omitempty"`
	LatencyMS         float64        `json:"latency_ms,omitempty"`
	JitterMS          float64        `json:"jitter_ms,omitempty"`
	PacketLossPercent float64        `json:"packet_loss_percent"`
	PPPoEUptime       int64          `json:"pppoe_uptime_seconds"`
	Reconnects1H      int            `json:"reconnects_1h"`
	Reconnects24H     int            `json:"reconnects_24h"`
}

// HistoryPoint is a storage-efficient point for 1h/24h/7d charts.
type HistoryPoint struct {
	Timestamp         time.Time   `json:"timestamp"`
	State             HealthState `json:"state"`
	LatencyMS         float64     `json:"latency_ms,omitempty"`
	JitterMS          float64     `json:"jitter_ms,omitempty"`
	PacketLossPercent float64     `json:"packet_loss_percent"`
	PPPoEUptime       int64       `json:"pppoe_uptime_seconds"`
}
