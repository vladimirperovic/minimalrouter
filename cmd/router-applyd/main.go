package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
)

func main() {
	log.Println("Starting Minimal Router OS router-applyd (privileged execution helper)...")

	socketPath := apply.DefaultSocketPath // "/run/minimalrouter/applyd.sock"

	// Ensure socket directory exists with restrictive permissions (SECURITY.md §10)
	socketDir := "/run/minimalrouter"
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		log.Fatalf("Failed to create socket directory %s: %v", socketDir, err)
	}

	// Remove pre-existing socket file if present
	_ = os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("Failed to bind Unix domain socket at %s: %v", socketPath, err)
	}
	defer listener.Close()

	// Restrict socket file permissions to owner only (SECURITY.md §10)
	if err := os.Chmod(socketPath, 0600); err != nil {
		log.Printf("Warning: could not set socket permissions: %v", err)
	}

	log.Printf("router-applyd listening on unix://%s\n", socketPath)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Error accepting connection: %v", err)
			continue
		}
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	var req apply.ApplyRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		resp := apply.ApplyResponse{
			Success:   false,
			Error:     fmt.Sprintf("Invalid RPC request format: %v", err),
			Timestamp: time.Now().Unix(),
		}
		json.NewEncoder(conn).Encode(resp)
		return
	}

	log.Printf("[router-applyd] Received operation %s (tx: %s, rev: %d)\n", req.Op, req.ID, req.Revision)

	var resp apply.ApplyResponse
	switch req.Op {
	case apply.OpApplyAll, apply.OpLoadNftables, apply.OpReloadService:
		logs := []string{}

		// 1. Write and apply nftables configuration
		if req.Nftables != "" {
			nftPath := "/run/minimalrouter/nftables.nft"
			if err := os.WriteFile(nftPath, []byte(req.Nftables), 0600); err == nil {
				logs = append(logs, fmt.Sprintf("Wrote nftables config to %s", nftPath))
				// Try executing nft -f if binary is available
				if _, err := os.Stat("/sbin/nft"); err == nil {
					cmd := exec.Command("/sbin/nft", "-f", nftPath)
					if out, err := cmd.CombinedOutput(); err != nil {
						resp = apply.ApplyResponse{
							ID:        req.ID,
							Success:   false,
							Error:     fmt.Sprintf("nftables apply failed: %v (%s)", err, string(out)),
							Timestamp: time.Now().Unix(),
						}
						json.NewEncoder(conn).Encode(resp)
						return
					}
					logs = append(logs, "Loaded nftables ruleset via /sbin/nft")
				}
			}
		}

		// 2. Write dnsmasq configuration
		if req.Dnsmasq != "" {
			dnsmasqPath := "/run/minimalrouter/dnsmasq.conf"
			if err := os.WriteFile(dnsmasqPath, []byte(req.Dnsmasq), 0600); err == nil {
				logs = append(logs, fmt.Sprintf("Wrote dnsmasq config to %s", dnsmasqPath))
			}
		}

		// 3. Write hostapd Wi-Fi Access Point configuration
		if req.Hostapd != "" {
			hostapdPath := "/run/minimalrouter/hostapd.conf"
			if err := os.WriteFile(hostapdPath, []byte(req.Hostapd), 0600); err == nil {
				logs = append(logs, fmt.Sprintf("Wrote hostapd config to %s", hostapdPath))
			}
		}

		// 3. Apply QoS CAKE / FQ-CoDel traffic shaping if configured
		if req.Config.QoS.Enabled && req.Config.WAN.Interface != "" {
			if _, err := os.Stat("/sbin/tc"); err == nil {
				wanIf := req.Config.WAN.Interface
				speed := fmt.Sprintf("%dmbit", req.Config.QoS.DownloadLimitMbps)
				cmd := exec.Command("/sbin/tc", "qdisc", "replace", "dev", wanIf, "root", "cake", "bandwidth", speed)
				if out, err := cmd.CombinedOutput(); err == nil {
					logs = append(logs, fmt.Sprintf("Applied CAKE QoS on %s (%s)", wanIf, speed))
				} else {
					logs = append(logs, fmt.Sprintf("tc cake warning: %v (%s)", err, string(out)))
				}
			}
		}

		resp = apply.ApplyResponse{
			ID:        req.ID,
			Success:   true,
			Logs:      strings.Join(logs, "; "),
			Timestamp: time.Now().Unix(),
		}
	default:
		resp = apply.ApplyResponse{
			ID:        req.ID,
			Success:   false,
			Error:     fmt.Sprintf("Unknown or forbidden operation: %s", req.Op),
			Timestamp: time.Now().Unix(),
		}
	}

	json.NewEncoder(conn).Encode(resp)
}
