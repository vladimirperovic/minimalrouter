//go:build linux

package telemetry

import (
	"bufio"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"

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

func RuntimeSnapshot(wanInterface, dataDir string) RuntimeStatus {
	status := RuntimeStatus{
		Available:    true,
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		CPUCount:     runtime.NumCPU(),
	}
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
	var stat unix.Statfs_t
	if dataDir != "" && unix.Statfs(dataDir, &stat) == nil {
		status.DiskTotalBytes = stat.Blocks * uint64(stat.Bsize)
		status.DiskUsedBytes = (stat.Blocks - stat.Bavail) * uint64(stat.Bsize)
	}
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
	status.RXBytes = readUint("/sys/class/net/" + statsInterface + "/statistics/rx_bytes")
	status.TXBytes = readUint("/sys/class/net/" + statsInterface + "/statistics/tx_bytes")
	if raw := readUint("/sys/class/thermal/thermal_zone0/temp"); raw > 0 {
		status.TemperatureC = float64(raw) / 1000
	}
	status.DHCPLeases = readDHCPLeases(dnsmasqLeasePath)
	return status
}
