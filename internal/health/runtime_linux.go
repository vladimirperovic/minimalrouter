//go:build linux

package health

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/config"
)

type updateSlotState struct {
	Current  string `json:"current"`
	Previous string `json:"previous"`
	Pending  string `json:"pending"`
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func serviceStarted(name string) bool {
	return pathExists(filepath.Join("/run/openrc/started", name))
}

func supervisedChildHealthy(service string) bool {
	data, err := os.ReadFile(filepath.Join("/run", service+".supervisor.pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		return false
	}
	childrenPath := filepath.Join("/proc", strconv.Itoa(pid), "task", strconv.Itoa(pid), "children")
	children, err := os.ReadFile(childrenPath)
	if err != nil {
		return false
	}
	for _, raw := range strings.Fields(string(children)) {
		child, parseErr := strconv.Atoi(raw)
		if parseErr == nil && child > 1 && pathExists(filepath.Join("/proc", strconv.Itoa(child))) {
			return true
		}
	}
	return false
}

func interfaceUp(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	iface, err := net.InterfaceByName(name)
	return err == nil && iface.Flags&net.FlagUp != 0
}

func inspectRuntimeFacts(cfg config.SystemConfig) RuntimeFacts {
	facts := RuntimeFacts{Available: pathExists("/run/openrc")}
	if !facts.Available {
		return facts
	}

	facts.RouterdHealthy = serviceStarted("routerd") && supervisedChildHealthy("routerd")
	facts.ApplydHealthy = serviceStarted("router-applyd") && supervisedChildHealthy("router-applyd")
	if info, err := os.Stat(apply.DefaultSocketPath); err == nil {
		facts.ApplySocketAvailable = info.Mode()&os.ModeSocket != 0
	}
	facts.DnsmasqStarted = serviceStarted("dnsmasq")
	facts.PPPoEStarted = serviceStarted("pppoe-wan")
	if cfg.WireGuard.Enabled {
		facts.WireGuardInterfaceUp = interfaceUp(cfg.WireGuard.Interface)
	}
	if cfg.WGClient.Enabled {
		facts.WireGuardClientInterfaceUp = interfaceUp(cfg.WGClient.Interface)
	}

	if data, err := os.ReadFile("/var/lib/minimalrouter-update/state.json"); err == nil {
		var state updateSlotState
		if json.Unmarshal(data, &state) == nil {
			facts.UpdateStateAvailable = true
			facts.UpdateCurrent = state.Current
			facts.UpdatePrevious = state.Previous
			facts.UpdatePending = state.Pending
		}
	}
	return facts
}
