package telemetry

import (
	"strconv"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/storage"
)

type DHCPLease struct {
	ExpiresAt int64  `json:"expires_at"`
	MAC       string `json:"mac"`
	IPAddress string `json:"ip_address"`
	Hostname  string `json:"hostname,omitempty"`
}

type RuntimeStatus struct {
	Available             bool                  `json:"available"`
	OS                    string                `json:"os"`
	Architecture          string                `json:"architecture"`
	WANConnected          bool                  `json:"wan_connected"`
	WANMAC                string                `json:"wan_mac,omitempty"`
	LANMAC                string                `json:"lan_mac,omitempty"`
	PublicIP              string                `json:"public_ip,omitempty"`
	UptimeSeconds         int64                 `json:"uptime_seconds,omitempty"`
	CPUCount              int                   `json:"cpu_count"`
	CPULoadPercent        float64               `json:"cpu_load_percent,omitempty"`
	LoadAverage           []float64             `json:"load_average,omitempty"`
	MemoryUsedBytes       uint64                `json:"memory_used_bytes,omitempty"`
	MemoryTotalBytes      uint64                `json:"memory_total_bytes,omitempty"`
	DiskUsedBytes         uint64                `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes        uint64                `json:"disk_total_bytes,omitempty"`
	Storage               storage.Status        `json:"storage"`
	RXBytes               uint64                `json:"rx_bytes,omitempty"`
	TXBytes               uint64                `json:"tx_bytes,omitempty"`
	TemperatureC          float64               `json:"temperature_c,omitempty"`
	TimeSynchronized      bool                  `json:"time_synchronized"`
	ConntrackCount        uint64                `json:"conntrack_count,omitempty"`
	ConntrackMax          uint64                `json:"conntrack_max,omitempty"`
	ConntrackUsagePercent float64               `json:"conntrack_usage_percent,omitempty"`
	DHCPLeases            []DHCPLease           `json:"dhcp_leases"`
	WireguardActivePeers  int                   `json:"wireguard_active_peers,omitempty"`
	WireguardPeers        []WireGuardPeerStatus `json:"wireguard_peers,omitempty"`
	DDNS                  DDNSStatus            `json:"ddns"`
}

// WireGuardPeerStatus mirrors one peer line of `wg show wg0 dump`: the live
// endpoint, allowed IPs, time of the last handshake, and cumulative transfer.
// Online is true when a handshake has happened in the last 3 minutes.
type WireGuardPeerStatus struct {
	PublicKey     string `json:"public_key"`
	Endpoint      string `json:"endpoint,omitempty"`
	AllowedIPs    string `json:"allowed_ips,omitempty"`
	LastHandshake int64  `json:"last_handshake_epoch,omitempty"`
	RXBytes       uint64 `json:"rx_bytes,omitempty"`
	TXBytes       uint64 `json:"tx_bytes,omitempty"`
	Online        bool   `json:"online"`
}

// parseWireGuardDump parses `wg show wg0 dump`, the tab-separated machine
// format. The first line describes the interface; every following line is one
// peer: public_key, preshared_key, endpoint, allowed_ips, latest_handshake,
// rx_bytes, tx_bytes, keepalive.
func parseWireGuardDump(out string) []WireGuardPeerStatus {
	now := time.Now().Unix()
	peers := make([]WireGuardPeerStatus, 0, 8)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		if fields[0] == "interface" {
			continue
		}
		status := WireGuardPeerStatus{
			PublicKey:  fields[0],
			Endpoint:   fields[2],
			AllowedIPs: fields[3],
		}
		if status.Endpoint == "(none)" {
			status.Endpoint = ""
		}
		if ts, err := strconv.ParseInt(fields[4], 10, 64); err == nil && ts > 0 {
			status.LastHandshake = ts
			status.Online = now-ts < 180
		}
		if rx, err := strconv.ParseUint(fields[5], 10, 64); err == nil {
			status.RXBytes = rx
		}
		if tx, err := strconv.ParseUint(fields[6], 10, 64); err == nil {
			status.TXBytes = tx
		}
		peers = append(peers, status)
	}
	return peers
}

// countActive returns how many peers are currently online.
func countActive(peers []WireGuardPeerStatus) int {
	active := 0
	for _, peer := range peers {
		if peer.Online {
			active++
		}
	}
	return active
}

// DDNSStatus reports the live state of the in-router inadyn daemon, mirroring
// the status pfSense shows for a Dynamic DNS client. LastUpdate is the last
// successful provider update epoch; LastIP is the address most recently sent.
type DDNSStatus struct {
	Running    bool   `json:"running"`
	Hostname   string `json:"hostname,omitempty"`
	LastUpdate int64  `json:"last_update_epoch,omitempty"`
	LastIP     string `json:"last_ip,omitempty"`
}
