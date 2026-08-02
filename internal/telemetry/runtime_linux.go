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
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/storage"
	"golang.org/x/sys/unix"
)

func readUint(path string) uint64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	value, _ := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	return value
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
		if len(status.LoadAverage) > 0 && status.CPUCount > 0 {
			status.CPULoadPercent = status.LoadAverage[0] / float64(status.CPUCount) * 100
			if status.CPULoadPercent > 100 {
				status.CPULoadPercent = 100
			}
		}
	}
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
	if raw := readUint("/sys/class/thermal/thermal_zone0/temp"); raw > 0 {
		status.TemperatureC = float64(raw) / 1000
	}
	status.ConntrackCount = readUint("/proc/sys/net/netfilter/nf_conntrack_count")
	status.ConntrackMax = readUint("/proc/sys/net/netfilter/nf_conntrack_max")
	if status.ConntrackMax > 0 {
		status.ConntrackUsagePercent = float64(status.ConntrackCount) / float64(status.ConntrackMax) * 100
		if status.ConntrackUsagePercent > 100 {
			status.ConntrackUsagePercent = 100
		}
	}
	status.DHCPLeases = readDHCPLeases(dnsmasqLeasePath)
	status.WireguardActivePeers = countActiveWireGuardPeers()
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

func countActiveWireGuardPeers() int {
	cmd := exec.Command("doas", "/usr/bin/wg", "show", "wg0", "latest-handshakes")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}

	active := 0
	now := time.Now().Unix()
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			ts, err := strconv.ParseInt(parts[1], 10, 64)
			if err == nil && ts > 0 && now-ts < 180 {
				active++
			}
		}
	}
	return active
}
