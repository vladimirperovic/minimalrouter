package network

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// InterfaceInfo is a redacted hardware-oriented view suitable for the setup UI.
type InterfaceInfo struct {
	Name         string `json:"name"`
	MACAddress   string `json:"mac_address,omitempty"`
	Up           bool   `json:"up"`
	Carrier      bool   `json:"carrier"`
	Physical     bool   `json:"physical"`
	DefaultRoute bool   `json:"default_route"`
	Score        int    `json:"score"`
}

// RoleRecommendation contains deterministic WAN/LAN candidates. The operator
// still confirms the choice before a disruptive configuration is applied.
type RoleRecommendation struct {
	WAN        string          `json:"wan"`
	LAN        string          `json:"lan"`
	Interfaces []InterfaceInfo `json:"interfaces"`
	Warnings   []string        `json:"warnings,omitempty"`
}

// Discover returns usable interfaces and a deterministic WAN/LAN recommendation.
func Discover() (RoleRecommendation, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return RoleRecommendation{}, fmt.Errorf("list network interfaces: %w", err)
	}
	return DiscoverFrom("/sys/class/net", "/proc/net/route", interfaces)
}

// DiscoverFrom is exported for deterministic tests and recovery environments.
func DiscoverFrom(sysClassNet, routeFile string, interfaces []net.Interface) (RoleRecommendation, error) {
	defaultRoutes := readDefaultRoutes(routeFile)
	items := make([]InterfaceInfo, 0, len(interfaces))
	for _, iface := range interfaces {
		if !eligibleName(iface.Name) {
			continue
		}
		item := InterfaceInfo{
			Name:         iface.Name,
			MACAddress:   strings.ToLower(iface.HardwareAddr.String()),
			Up:           iface.Flags&net.FlagUp != 0,
			Carrier:      readBool(filepath.Join(sysClassNet, iface.Name, "carrier")),
			Physical:     physicalInterface(sysClassNet, iface.Name),
			DefaultRoute: defaultRoutes[iface.Name],
		}
		item.Score = score(item)
		items = append(items, item)
	}
	if len(items) < 2 {
		return RoleRecommendation{}, errors.New("at least two usable network interfaces are required")
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score == items[j].Score {
			return items[i].Name < items[j].Name
		}
		return items[i].Score > items[j].Score
	})

	wanIndex := 0
	for i := range items {
		if items[i].DefaultRoute {
			wanIndex = i
			break
		}
	}
	wan := items[wanIndex].Name
	lan := ""
	for i := range items {
		if i == wanIndex {
			continue
		}
		if lan == "" || (items[i].Physical && !physicalByName(items, lan)) {
			lan = items[i].Name
		}
		if items[i].Physical && items[i].Carrier {
			lan = items[i].Name
			break
		}
	}
	if lan == "" {
		return RoleRecommendation{}, errors.New("could not select a distinct LAN interface")
	}

	result := RoleRecommendation{WAN: wan, LAN: lan, Interfaces: items}
	if !items[wanIndex].DefaultRoute {
		result.Warnings = append(result.Warnings, "No existing default route was found; WAN is a scored recommendation only.")
	}
	if !physicalByName(items, wan) || !physicalByName(items, lan) {
		result.Warnings = append(result.Warnings, "One or more selected interfaces appear virtual; confirm roles on the local console.")
	}
	return result, nil
}

func score(item InterfaceInfo) int {
	value := 0
	if item.Physical {
		value += 100
	}
	if item.DefaultRoute {
		value += 80
	}
	if item.Carrier {
		value += 20
	}
	if item.Up {
		value += 10
	}
	if item.MACAddress != "" {
		value += 5
	}
	return value
}

func eligibleName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || lower == "lo" || len(lower) > 15 {
		return false
	}
	for _, prefix := range []string{"br-", "docker", "veth", "virbr", "tun", "tap", "wg", "ppp", "tailscale", "zt", "ifb"} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	return true
}

func physicalInterface(sysClassNet, name string) bool {
	path := filepath.Join(sysClassNet, name, "device")
	if _, err := os.Stat(path); err == nil {
		return true
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	return false
}

func readBool(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	value, err := strconv.Atoi(strings.TrimSpace(string(data)))
	return err == nil && value == 1
}

func readDefaultRoutes(path string) map[string]bool {
	result := make(map[string]bool)
	file, err := os.Open(path)
	if err != nil {
		return result
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	first := true
	for scanner.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 4 && fields[1] == "00000000" {
			flags, err := strconv.ParseUint(fields[3], 16, 64)
			if err == nil && flags&0x1 != 0 {
				result[fields[0]] = true
			}
		}
	}
	return result
}

func physicalByName(items []InterfaceInfo, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return item.Physical
		}
	}
	return false
}
