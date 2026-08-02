//go:build linux

package telemetry

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

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
)

func readUint(path string) uint64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	value, _ := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	return value
}

// readCPULoadPercent returns real CPU utilization since the previous call,
// computed as the busy-jiffies delta from /proc/stat (matching what Proxmox
// reports), falling back to 0 on the first sample.
func readCPULoadPercent() float64 {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	line := strings.Fields(strings.SplitN(string(data), "\n", 2)[0])
	if len(line) < 5 || line[0] != "cpu" {
		return 0
	}
	// cpu user nice system idle iowait irq softirq steal
	var total, idle uint64
	for i, field := range line[1:] {
		value, _ := strconv.ParseUint(field, 10, 64)
		total += value
		if i == 3 || i == 4 { // idle + iowait
			idle += value
		}
	}
	cpuSampleMu.Lock()
	defer cpuSampleMu.Unlock()
	if lastCPUSample.total == 0 || total < lastCPUSample.total {
		lastCPUSample = cpuTicks{total: total, idle: idle}
		return 0
	}
	dTotal := total - lastCPUSample.total
	dIdle := idle - lastCPUSample.idle
	lastCPUSample = cpuTicks{total: total, idle: idle}
	if dTotal == 0 {
		return 0
	}
	pct := float64(dTotal-dIdle) / float64(dTotal) * 100
	if pct > 100 {
		return 100
	}
	return pct
}

func RuntimeSnapshot(wanInterface, lanInterface, dataDir string) RuntimeStatus {
	status := RuntimeStatus{
		Available:    true,
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		CPUCount:     runtime.NumCPU(),
	}
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
	status.DHCPLeases = readDHCPLeases(dnsmasqLeasePath)
	status.WireguardPeers = readWireGuardPeers()
	status.WireguardActivePeers = countActive(status.WireguardPeers)
	status.DDNS = inspectDDNS()
	return status
}

// interfaceMAC returns the hardware address for the named interface, or ""
// when the interface does not exist or has no address assigned.
func interfaceMAC(name string) string {
	iface, err := net.InterfaceByName(name)
	if err != nil || iface.HardwareAddr == nil {
		return ""
	}
	return iface.HardwareAddr.String()
}

// inspectDDNS reads the live inadyn state so the dashboard can show the
// pfSense-style status card. Alpine's inadyn runs under OpenRC
// supervise-daemon (pidfile /run/supervise-inadyn.pid); its cache holds the
// last registered IP (v1) or "<epoch> <ip>" (v2), one file per host.
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

// inadynHostname pulls the update target from the generated configuration,
// which is authoritative (the cache filename is "<user>@<provider>-<host>").
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

// readWireGuardPeers runs `wg show wg0 dump` and parses the per-peer status.
func readWireGuardPeers() []WireGuardPeerStatus {
	cmd := exec.Command("doas", "/usr/bin/wg", "show", "wg0", "dump")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseWireGuardDump(string(out))
}
