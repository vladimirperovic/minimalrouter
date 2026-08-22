//go:build linux

package telemetry

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/storage"
	"golang.org/x/sys/unix"
)

type cpuTicks struct {
	total uint64
	idle  uint64
}

var (
	cpuSampleMu   sync.Mutex
	lastCPUSample cpuTicks
	lastCPUAt     time.Time
	lastCPUPct    float64
)

const wireGuardTelemetryTTL = 30 * time.Second

type cachedWireGuardPeers struct {
	peers   []WireGuardPeerStatus
	takenAt time.Time
}

var wireGuardTelemetryCache = struct {
	sync.Mutex
	items map[string]cachedWireGuardPeers
}{items: make(map[string]cachedWireGuardPeers)}

func readUint(path string) uint64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	value, _ := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	return value
}

func readCPULoadPercent() float64 {
	cpuSampleMu.Lock()
	defer cpuSampleMu.Unlock()
	if lastCPUSample.total != 0 && time.Since(lastCPUAt) < 200*time.Millisecond {
		return lastCPUPct
	}
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	line := strings.Fields(strings.SplitN(string(data), "\n", 2)[0])
	if len(line) < 5 || line[0] != "cpu" {
		return 0
	}
	var total, idle uint64
	for i, field := range line[1:] {
		value, _ := strconv.ParseUint(field, 10, 64)
		total += value
		if i == 3 || i == 4 {
			idle += value
		}
	}
	if lastCPUSample.total == 0 || total < lastCPUSample.total {
		lastCPUSample = cpuTicks{total: total, idle: idle}
		lastCPUAt = time.Now()
		return 0
	}
	dTotal := total - lastCPUSample.total
	dIdle := idle - lastCPUSample.idle
	lastCPUSample = cpuTicks{total: total, idle: idle}
	lastCPUAt = time.Now()
	if dTotal == 0 {
		return 0
	}
	pct := float64(dTotal-dIdle) / float64(dTotal) * 100
	if pct > 100 {
		pct = 100
	}
	lastCPUPct = pct
	return pct
}

func RuntimeSnapshot(wanInterface, lanInterface, dataDir string) RuntimeStatus {
	status := RuntimeStatus{Available: true, OS: runtime.GOOS, Architecture: runtime.GOARCH, CPUCount: runtime.NumCPU()}
	var timex unix.Timex
	clockState, clockErr := unix.Adjtimex(&timex)
	status.TimeSynchronized = clockErr == nil && clockState != unix.TIME_ERROR
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			uptime, _ := strconv.ParseFloat(fields[0], 64)
			status.UptimeSeconds = int64(uptime)
		}
	}
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(data))
		for i := 0; i < len(fields) && i < 3; i++ {
			value, parseErr := strconv.ParseFloat(fields[i], 64)
			if parseErr == nil {
				status.LoadAverage = append(status.LoadAverage, value)
			}
		}
	}
	status.CPULoadPercent = readCPULoadPercent()
	if file, err := os.Open("/proc/meminfo"); err == nil {
		scanner := bufio.NewScanner(file)
		var totalKB, availableKB uint64
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 2 {
				continue
			}
			value, _ := strconv.ParseUint(fields[1], 10, 64)
			switch strings.TrimSuffix(fields[0], ":") {
			case "MemTotal":
				totalKB = value
			case "MemAvailable":
				availableKB = value
			}
		}
		_ = file.Close()
		status.MemoryTotalBytes = totalKB * 1024
		if totalKB >= availableKB {
			status.MemoryUsedBytes = (totalKB - availableKB) * 1024
		}
	}
	status.AppMemoryBytes = readProcessMemoryBytes()
	status.Storage = storage.Inspect(dataDir)
	status.DiskTotalBytes = status.Storage.TotalBytes
	status.DiskUsedBytes = status.Storage.UsedBytes
	pppName := "ppp0"
	iface, err := net.InterfaceByName(pppName)
	if err == nil && iface.Flags&net.FlagUp != 0 {
		addresses, _ := iface.Addrs()
		for _, address := range addresses {
			ip, _, parseErr := net.ParseCIDR(address.String())
			if parseErr == nil && ip.To4() != nil {
				status.WANConnected = true
				status.PublicIP = ip.String()
				break
			}
		}
	}
	statsInterface := pppName
	if !status.WANConnected {
		statsInterface = wanInterface
	}
	status.WANMAC = interfaceMAC(wanInterface)
	status.LANMAC = interfaceMAC(lanInterface)
	status.RXBytes = readUint("/sys/class/net/" + statsInterface + "/statistics/rx_bytes")
	status.TXBytes = readUint("/sys/class/net/" + statsInterface + "/statistics/tx_bytes")
	status.ConntrackCount = readUint("/proc/sys/net/netfilter/nf_conntrack_count")
	status.ConntrackMax = readUint("/proc/sys/net/netfilter/nf_conntrack_max")
	if status.ConntrackMax > 0 {
		status.ConntrackUsagePercent = float64(status.ConntrackCount) / float64(status.ConntrackMax) * 100
		if status.ConntrackUsagePercent > 100 {
			status.ConntrackUsagePercent = 100
		}
	}
	status.DHCPLeases = readDHCPLeases("/var/lib/minimalrouter-dhcp/dnsmasq.leases")
	status.WireguardPeers = readWireGuardPeers()
	status.WireguardActivePeers = countActive(status.WireguardPeers)
	status.WireGuardClient = readWireGuardClientStatus()
	status.DDNS = inspectDDNS()
	return status
}

func interfaceMAC(name string) string {
	iface, err := net.InterfaceByName(name)
	if err != nil || iface.HardwareAddr == nil {
		return ""
	}
	return iface.HardwareAddr.String()
}

func inspectDDNS() DDNSStatus {
	st := DDNSStatus{}
	if _, err := os.Stat("/run/supervise-inadyn.pid"); err == nil {
		st.Running = true
	}
	entries, err := os.ReadDir("/var/cache/inadyn")
	if err != nil {
		return st
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".cache") {
			continue
		}
		cachePath := filepath.Join("/var/cache/inadyn", entry.Name())
		data, readErr := os.ReadFile(cachePath)
		if readErr != nil {
			continue
		}
		for _, field := range strings.Fields(string(data)) {
			if ip := net.ParseIP(field); ip != nil {
				st.LastIP = field
				continue
			}
			if epoch, parseErr := strconv.ParseInt(field, 10, 64); parseErr == nil && epoch > 1_000_000_000 {
				st.LastUpdate = epoch
			}
		}
		if st.LastUpdate == 0 {
			if info, statErr := os.Stat(cachePath); statErr == nil {
				st.LastUpdate = info.ModTime().Unix()
			}
		}
		break
	}
	st.Hostname = inadynHostname()
	return st
}

func inadynHostname() string {
	data, err := os.ReadFile("/etc/inadyn/inadyn.conf")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "hostname = ") {
			return strings.Trim(line[len("hostname = "):], `"`)
		}
	}
	return ""
}

// wgShowTimeout bounds each privileged status read. wg show is a local netlink
// query that returns in milliseconds; anything slower means the interface or
// doas is wedged, and telemetry must degrade rather than block the API.
const wgShowTimeout = 3 * time.Second

// safeWGShow executes only one of the installer-authorized, non-secret
// WireGuard status projections. Unlike `wg show ... dump`, none returns an
// interface private key or peer preshared key. The doas policy matches the
// complete command+argument vector, so routerd cannot turn this telemetry path
// into general CAP_NET_ADMIN/root command execution.
func safeWGShow(interfaceName, field string) (string, error) {
	if interfaceName != "wg0" && interfaceName != "wg1" {
		return "", os.ErrPermission
	}
	switch field {
	case "endpoints", "allowed-ips", "latest-handshakes", "transfer":
	default:
		return "", os.ErrPermission
	}
	ctx, cancel := context.WithTimeout(context.Background(), wgShowTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "doas", "/usr/bin/wg", "show", interfaceName, field)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func ensureWGPeer(peers map[string]*WireGuardPeerStatus, order *[]string, publicKey string) *WireGuardPeerStatus {
	if peer, ok := peers[publicKey]; ok {
		return peer
	}
	peer := &WireGuardPeerStatus{PublicKey: publicKey}
	peers[publicKey] = peer
	*order = append(*order, publicKey)
	return peer
}

func cloneWGPeers(peers []WireGuardPeerStatus) []WireGuardPeerStatus {
	if len(peers) == 0 {
		return nil
	}
	return append([]WireGuardPeerStatus(nil), peers...)
}

func readWireGuardInterface(interfaceName string) []WireGuardPeerStatus {
	// A disabled tunnel is by far the common case. Avoid spawning doas/wg at
	// all when the interface does not exist instead of discovering that four
	// times through failed privileged child processes.
	if _, err := net.InterfaceByName(interfaceName); err != nil {
		return nil
	}

	now := time.Now()
	wireGuardTelemetryCache.Lock()
	cached, ok := wireGuardTelemetryCache.items[interfaceName]
	wireGuardTelemetryCache.Unlock()
	if ok && now.Sub(cached.takenAt) < wireGuardTelemetryTTL {
		return cloneWGPeers(cached.peers)
	}

	peers := make(map[string]*WireGuardPeerStatus)
	order := make([]string, 0, 8)

	if out, err := safeWGShow(interfaceName, "allowed-ips"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			peer := ensureWGPeer(peers, &order, fields[0])
			peer.AllowedIPs = strings.Join(fields[1:], ", ")
		}
	}
	if out, err := safeWGShow(interfaceName, "endpoints"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			peer := ensureWGPeer(peers, &order, fields[0])
			if fields[1] != "(none)" {
				peer.Endpoint = fields[1]
			}
		}
	}
	if out, err := safeWGShow(interfaceName, "latest-handshakes"); err == nil {
		nowUnix := now.Unix()
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			peer := ensureWGPeer(peers, &order, fields[0])
			if ts, parseErr := strconv.ParseInt(fields[1], 10, 64); parseErr == nil && ts > 0 {
				peer.LastHandshake = ts
				peer.Online = nowUnix >= ts && nowUnix-ts < 180
			}
		}
	}
	if out, err := safeWGShow(interfaceName, "transfer"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			peer := ensureWGPeer(peers, &order, fields[0])
			peer.RXBytes, _ = strconv.ParseUint(fields[1], 10, 64)
			peer.TXBytes, _ = strconv.ParseUint(fields[2], 10, 64)
		}
	}

	result := make([]WireGuardPeerStatus, 0, len(order))
	for _, publicKey := range order {
		if peer := peers[publicKey]; peer != nil {
			result = append(result, *peer)
		}
	}
	wireGuardTelemetryCache.Lock()
	wireGuardTelemetryCache.items[interfaceName] = cachedWireGuardPeers{peers: cloneWGPeers(result), takenAt: now}
	wireGuardTelemetryCache.Unlock()
	return result
}

func readWireGuardPeers() []WireGuardPeerStatus {
	return readWireGuardInterface("wg0")
}

func readWireGuardClientStatus() *WireGuardClientStatus {
	peers := readWireGuardInterface("wg1")
	if len(peers) == 0 {
		return nil
	}
	peer := peers[0]
	return &WireGuardClientStatus{
		Endpoint:      peer.Endpoint,
		LastHandshake: peer.LastHandshake,
		RXBytes:       peer.RXBytes,
		TXBytes:       peer.TXBytes,
		Online:        peer.Online,
	}
}

// readProcessMemoryBytes sums the resident memory of every userspace process
// from /proc/<pid>/stat (field 24, resident pages). Top-level /proc entries are
// thread-group leaders only, so threads are never counted twice. This separates
// real application footprint from the kernel file cache that inflates the
// MemTotal-minus-MemAvailable figure on a quiet appliance.
func readProcessMemoryBytes() uint64 {
	pageSize := uint64(os.Getpagesize())
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	var total uint64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid := entry.Name()
		if pid[0] < '0' || pid[0] > '9' {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", pid, "stat"))
		if err != nil {
			continue
		}
		// The comm field may contain spaces; everything after the final ')'
		// starts at field 3 (state), so RSS is field 24 = offset 21 from there.
		closing := strings.LastIndex(string(data), ")")
		if closing < 0 {
			continue
		}
		fields := strings.Fields(string(data[closing+1:]))
		if len(fields) <= 21 {
			continue
		}
		pages, parseErr := strconv.ParseUint(fields[21], 10, 64)
		if parseErr != nil {
			continue
		}
		total += pages * pageSize
	}
	return total
}
