package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
)

const (
	devicePauseStatePath = "/var/lib/minimalrouter-applyd/device-pauses.json"
	devicePauseNftPath   = "/run/minimalrouter/device-pauses.nft"
	maxDevicePauses      = 512
)

var actionProcessStartedAt = time.Now()

type serviceActionRequest struct {
	Action  string `json:"action"`
	IP      string `json:"ip,omitempty"`
	Seconds int    `json:"seconds,omitempty"`
}

type serviceActionResponse struct {
	Success bool                `json:"success"`
	Error   string              `json:"error,omitempty"`
	Pauses  []apply.DevicePause `json:"pauses,omitempty"`
}

// main hardens the process before removing/recreating the primary apply socket.
// The mtime guard below prevents a stale socket left by a crashed previous
// process from making this listener reachable before that sequence completes.
func init() {
	go startServiceActionListenerAfterApplySocket()
}

func startServiceActionListenerAfterApplySocket() {
	for {
		info, err := os.Stat(apply.DefaultSocketPath)
		if err == nil && !info.ModTime().Before(actionProcessStartedAt) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = os.Remove(apply.ServiceActionSocketPath)
	listener, err := net.Listen("unix", apply.ServiceActionSocketPath)
	if err != nil {
		log.Printf("service action socket unavailable: %v", err)
		return
	}
	defer listener.Close()
	if err := secureSocketForRouterd(apply.ServiceActionSocketPath); err != nil {
		log.Printf("cannot secure service action socket: %v", err)
		_ = os.Remove(apply.ServiceActionSocketPath)
		return
	}
	applyMu.Lock()
	if pauses, err := loadActiveDevicePauses(); err != nil {
		log.Printf("device pause restore unavailable: %v", err)
	} else if err := applyDevicePauseFirewall(pauses); err != nil {
		log.Printf("device pause firewall restore failed: %v", err)
	}
	applyMu.Unlock()
	log.Printf("router-applyd fixed actions listening on unix://%s", apply.ServiceActionSocketPath)
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("service action accept: %v", err)
			continue
		}
		go handleServiceActionConnection(conn)
	}
}

func handleServiceActionConnection(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(90 * time.Second))
	if err := validatePeer(conn); err != nil {
		writeServiceActionResponse(conn, serviceActionResponse{Error: "unauthorized local peer"})
		return
	}
	decoder := json.NewDecoder(io.LimitReader(conn, 4096))
	decoder.DisallowUnknownFields()
	var request serviceActionRequest
	if err := decoder.Decode(&request); err != nil {
		writeServiceActionResponse(conn, serviceActionResponse{Error: "invalid action request"})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeServiceActionResponse(conn, serviceActionResponse{Error: "action request must contain exactly one object"})
		return
	}

	applyMu.Lock()
	defer applyMu.Unlock()

	switch request.Action {
	case apply.DeviceActionStatus:
		pauses, err := loadActiveDevicePauses()
		if err != nil {
			writeServiceActionResponse(conn, serviceActionResponse{Error: "device pause state unavailable"})
			return
		}
		writeServiceActionResponse(conn, serviceActionResponse{Success: true, Pauses: pauses})
		return
	case apply.DeviceActionPause:
		pauses, err := pauseDevice(request.IP, request.Seconds)
		if err != nil {
			log.Printf("device pause failed: %v", err)
			writeServiceActionResponse(conn, serviceActionResponse{Error: "device pause failed"})
			return
		}
		writeServiceActionResponse(conn, serviceActionResponse{Success: true, Pauses: pauses})
		return
	case apply.DeviceActionResume:
		pauses, err := resumeDevice(request.IP)
		if err != nil {
			log.Printf("device resume failed: %v", err)
			writeServiceActionResponse(conn, serviceActionResponse{Error: "device resume failed"})
			return
		}
		writeServiceActionResponse(conn, serviceActionResponse{Success: true, Pauses: pauses})
		return
	}

	if err := runFixedServiceAction(request.Action); err != nil {
		log.Printf("service action %q failed: %v", request.Action, err)
		writeServiceActionResponse(conn, serviceActionResponse{Error: "service recovery failed"})
		return
	}
	log.Printf("service action %q completed", request.Action)
	writeServiceActionResponse(conn, serviceActionResponse{Success: true})
}

func writeServiceActionResponse(conn net.Conn, response serviceActionResponse) {
	_ = json.NewEncoder(conn).Encode(response)
}

func runFixedServiceAction(action string) error {
	cfg, err := loadLastGood()
	if err != nil || cfg == nil {
		return fmt.Errorf("trusted last-good configuration unavailable")
	}
	switch action {
	case apply.ServiceActionWANReconnect:
		if !cfg.WAN.Enabled {
			return fmt.Errorf("PPPoE WAN is disabled")
		}
		if err := runFixed("/sbin/rc-service", "pppoe-wan", "restart"); err != nil {
			return fmt.Errorf("restart PPPoE WAN: %w", err)
		}
		if err := runFixed("/sbin/rc-service", "pppoe-wan", "status"); err != nil {
			return fmt.Errorf("PPPoE WAN service unhealthy after restart: %w", err)
		}
		return nil
	case apply.ServiceActionDNSDHCPRestart:
		if err := runFixed("/sbin/rc-service", "dnsmasq", "restart"); err != nil {
			return fmt.Errorf("restart dnsmasq: %w", err)
		}
		if err := runFixed("/sbin/rc-service", "dnsmasq", "status"); err != nil {
			return fmt.Errorf("dnsmasq unhealthy after restart: %w", err)
		}
		return nil
	case apply.ServiceActionWireGuardRestart:
		if err := activateWireGuard(*cfg); err != nil {
			return fmt.Errorf("restart WireGuard server: %w", err)
		}
		if err := activateWireGuardClient(*cfg); err != nil {
			return fmt.Errorf("restart WireGuard client: %w", err)
		}
		if cfg.WireGuard.Enabled {
			if _, err := runFixedOutput("/sbin/ip", "link", "show", "dev", wireGuardInterfaceName(cfg.WireGuard)); err != nil {
				return fmt.Errorf("WireGuard server interface unavailable after restart: %w", err)
			}
		}
		if cfg.WGClient.Enabled {
			if _, err := runFixedOutput("/sbin/ip", "link", "show", "dev", cfg.WGClient.Interface); err != nil {
				return fmt.Errorf("WireGuard client interface unavailable after restart: %w", err)
			}
		}
		return nil
	default:
		return fmt.Errorf("service action is not allowlisted")
	}
}

func validatePauseIP(value string) (string, error) {
	cfg, err := loadLastGood()
	if err != nil || cfg == nil {
		return "", fmt.Errorf("trusted last-good configuration unavailable")
	}
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("device address must be IPv4")
	}
	_, lan, err := net.ParseCIDR(cfg.LAN.CIDR)
	if err != nil || !lan.Contains(ip.To4()) {
		return "", fmt.Errorf("device address is outside the trusted LAN")
	}
	if ip.Equal(net.ParseIP(cfg.LAN.IPAddress)) {
		return "", fmt.Errorf("router LAN address cannot be paused")
	}
	return ip.To4().String(), nil
}

func pauseDevice(value string, seconds int) ([]apply.DevicePause, error) {
	if seconds != 0 && seconds != 900 && seconds != 3600 {
		return nil, fmt.Errorf("unsupported pause duration")
	}
	ip, err := validatePauseIP(value)
	if err != nil {
		return nil, err
	}
	pauses, err := loadActiveDevicePauses()
	if err != nil {
		return nil, err
	}
	until := int64(0)
	if seconds > 0 {
		until = time.Now().UTC().Add(time.Duration(seconds) * time.Second).Unix()
	}
	updated := false
	for index := range pauses {
		if pauses[index].IP == ip {
			pauses[index].UntilUnix = until
			updated = true
			break
		}
	}
	if !updated {
		if len(pauses) >= maxDevicePauses {
			return nil, fmt.Errorf("device pause limit reached")
		}
		pauses = append(pauses, apply.DevicePause{IP: ip, UntilUnix: until})
	}
	if err := persistDevicePauses(pauses); err != nil {
		return nil, err
	}
	if err := applyDevicePauseFirewall(pauses); err != nil {
		return nil, err
	}
	return pauses, nil
}

func resumeDevice(value string) ([]apply.DevicePause, error) {
	ip, err := validatePauseIP(value)
	if err != nil {
		return nil, err
	}
	pauses, err := loadActiveDevicePauses()
	if err != nil {
		return nil, err
	}
	filtered := pauses[:0]
	for _, pause := range pauses {
		if pause.IP != ip {
			filtered = append(filtered, pause)
		}
	}
	pauses = filtered
	if err := persistDevicePauses(pauses); err != nil {
		return nil, err
	}
	if err := applyDevicePauseFirewall(pauses); err != nil {
		return nil, err
	}
	return pauses, nil
}

func loadActiveDevicePauses() ([]apply.DevicePause, error) {
	data, err := os.ReadFile(devicePauseStatePath)
	if os.IsNotExist(err) {
		return []apply.DevicePause{}, nil
	}
	if err != nil {
		return nil, err
	}
	var pauses []apply.DevicePause
	if err := json.Unmarshal(data, &pauses); err != nil {
		return nil, err
	}
	if len(pauses) > maxDevicePauses {
		return nil, fmt.Errorf("device pause state exceeds limit")
	}
	now := time.Now().UTC().Unix()
	active := make([]apply.DevicePause, 0, len(pauses))
	for _, pause := range pauses {
		if _, err := validatePauseIP(pause.IP); err != nil {
			continue
		}
		if pause.UntilUnix != 0 && pause.UntilUnix <= now {
			continue
		}
		active = append(active, pause)
	}
	sort.Slice(active, func(i, j int) bool { return active[i].IP < active[j].IP })
	if len(active) != len(pauses) {
		if err := persistDevicePauses(active); err != nil {
			return nil, err
		}
	}
	return active, nil
}

func persistDevicePauses(pauses []apply.DevicePause) error {
	if len(pauses) > maxDevicePauses {
		return fmt.Errorf("device pause state exceeds limit")
	}
	if err := os.MkdirAll(filepath.Dir(devicePauseStatePath), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(pauses)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(devicePauseStatePath), ".device-pauses-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, devicePauseStatePath)
}

func applyDevicePauseFirewall(pauses []apply.DevicePause) error {
	cfg, err := loadLastGood()
	if err != nil || cfg == nil {
		return fmt.Errorf("trusted last-good configuration unavailable")
	}
	var b strings.Builder
	if err := runFixed("/usr/sbin/nft", "list", "table", "inet", "minimalrouter_pause"); err == nil {
		b.WriteString("delete table inet minimalrouter_pause\n")
	}
	b.WriteString("table inet minimalrouter_pause {\n")
	b.WriteString("  set blocked_ipv4 {\n    type ipv4_addr\n    flags timeout\n")
	if len(pauses) > 0 {
		b.WriteString("    elements = { ")
		now := time.Now().UTC().Unix()
		for i, pause := range pauses {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(pause.IP)
			if pause.UntilUnix > 0 {
				remaining := pause.UntilUnix - now
				if remaining < 1 {
					remaining = 1
				}
				b.WriteString(fmt.Sprintf(" timeout %ds", remaining))
			}
		}
		b.WriteString(" }\n")
	}
	b.WriteString("  }\n")
	b.WriteString("  chain forward {\n")
	b.WriteString("    type filter hook forward priority -10; policy accept;\n")
	// Internet pause is deliberately WAN-only. Local LAN switching is outside
	// this routed hook, and forwarded traffic toward trusted tunnels/segments is
	// not blocked by this feature.
	if cfg.WAN.Interface != "" {
		b.WriteString(fmt.Sprintf("    ip saddr @blocked_ipv4 oifname \"%s\" drop\n", cfg.WAN.Interface))
	}
	b.WriteString("    ip saddr @blocked_ipv4 oifname \"ppp*\" drop\n")
	b.WriteString("  }\n}\n")
	if err := os.WriteFile(devicePauseNftPath, []byte(b.String()), 0600); err != nil {
		return err
	}
	defer os.Remove(devicePauseNftPath)
	if err := runFixed("/usr/sbin/nft", "-c", "-f", devicePauseNftPath); err != nil {
		return fmt.Errorf("validate pause firewall: %w", err)
	}
	if err := runFixed("/usr/sbin/nft", "-f", devicePauseNftPath); err != nil {
		return fmt.Errorf("apply pause firewall: %w", err)
	}
	return nil
}
