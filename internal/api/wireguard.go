package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/services"
)

var wireGuardEndpointHostPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

type wireGuardProvisionRequest struct {
	Name            string `json:"name"`
	ClientIPAddress string `json:"client_ip_address"`
	ServerEndpoint  string `json:"server_endpoint"`
}

func validateWireGuardEndpoint(value string) error {
	if len(value) == 0 || len(value) > 255 || strings.IndexAny(value, "\r\n\t\x00") >= 0 {
		return fmt.Errorf("server_endpoint must be a host and UDP port")
	}
	host, portText, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return fmt.Errorf("server_endpoint must use host:port format")
	}
	if parsed := net.ParseIP(host); parsed != nil {
		if parsed.To4() == nil {
			return fmt.Errorf("IPv6 endpoints are unavailable while IPv6 is disabled")
		}
	} else if !wireGuardEndpointHostPattern.MatchString(host) || strings.Contains(host, "..") {
		return fmt.Errorf("server_endpoint contains an invalid hostname")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1024 || port > 65535 {
		return fmt.Errorf("server_endpoint port must be between 1024 and 65535")
	}
	return nil
}

// nextFreeWireGuardIP returns the first IPv4 in the server's subnet after the
// server address that no enabled peer has claimed. Reserved network and
// broadcast addresses and the server address are skipped. Returns an error when
// the subnet is exhausted.
func nextFreeWireGuardIP(serverIP net.IP, subnet string, peers []config.WireGuardPeer) (string, error) {
	used := make(map[string]struct{})
	for _, p := range peers {
		if !p.Enabled {
			continue
		}
		for _, ip := range p.AllowedIPs {
			if got := net.ParseIP(strings.TrimSuffix(ip, "/32")); got != nil {
				used[got.String()] = struct{}{}
			}
		}
	}

	if _, network, err := net.ParseCIDR(subnet); err != nil {
		return "", fmt.Errorf("invalid WireGuard subnet %s", subnet)
	} else {
		net4 := network.IP.To4()
		server4 := serverIP.To4()
		if net4 == nil || server4 == nil {
			return "", fmt.Errorf("WireGuard subnet is not IPv4")
		}
		ones, bits := network.Mask.Size()
		hosts := 1 << uint(bits-ones)
		lastHost := int(net4[3]) + hosts - 1
		serverHost := int(server4[3])
		for i := 1; i < hosts-1; i++ {
			octet := serverHost + i
			if octet >= lastHost {
				break
			}
			candidate := net.IPv4(net4[0], net4[1], net4[2], byte(octet))
			if _, skip := used[candidate.String()]; skip {
				continue
			}
			return candidate.String(), nil
		}
	}
	return "", fmt.Errorf("no free address available in WireGuard subnet %s", subnet)
}

// handleProvisionWireGuardPeer generates the client key locally, persists only
// its public key plus a preshared key, applies the peer, and returns the private
// client configuration exactly once in a no-store response.
func (s *Server) handleProvisionWireGuardPeer(w http.ResponseWriter, r *http.Request) {
	var req wireGuardProvisionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.ClientIPAddress = strings.TrimSpace(strings.TrimSuffix(req.ClientIPAddress, "/32"))
	req.ServerEndpoint = strings.TrimSpace(req.ServerEndpoint)
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusUnprocessableEntity)
		return
	}

	candidate := s.engine.GetCurrentConfig()
	if !candidate.WAN.Enabled {
		http.Error(w, "Configure and verify the WAN connection before enabling WireGuard", http.StatusConflict)
		return
	}

	serverIP, wgNetwork, err := net.ParseCIDR(candidate.WireGuard.Address)
	if err != nil {
		http.Error(w, "WireGuard subnet is invalid", http.StatusUnprocessableEntity)
		return
	}

	// Auto-assign the next free client IP when the caller leaves it blank.
	if req.ClientIPAddress == "" {
		req.ClientIPAddress, err = nextFreeWireGuardIP(serverIP, candidate.WireGuard.Address, candidate.WireGuard.Peers)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
	}

	// Auto-fill the server endpoint from the DDNS domain when left blank.
	if req.ServerEndpoint == "" {
		if domain := strings.TrimSpace(candidate.Cloudflare.Domain); domain != "" {
			req.ServerEndpoint = net.JoinHostPort(domain, strconv.Itoa(candidate.WireGuard.ListenPort))
		}
	}
	if err := validateWireGuardEndpoint(req.ServerEndpoint); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if req.ServerEndpoint == "" {
		http.Error(w, "Configure Dynamic DNS or provide a server_endpoint", http.StatusUnprocessableEntity)
		return
	}

	clientIP := net.ParseIP(req.ClientIPAddress)
	if clientIP == nil || clientIP.To4() == nil || !wgNetwork.Contains(clientIP) || clientIP.Equal(serverIP) {
		http.Error(w, "client_ip_address must be a unique IPv4 address inside the WireGuard subnet", http.StatusUnprocessableEntity)
		return
	}
	clientCIDR := clientIP.String() + "/32"
	for _, existing := range candidate.WireGuard.Peers {
		if !existing.Enabled {
			continue
		}
		for _, allowed := range existing.AllowedIPs {
			if allowed == clientCIDR {
				http.Error(w, "client_ip_address is already assigned to another WireGuard peer", http.StatusConflict)
				return
			}
		}
	}

	clientPrivate, clientPublic, err := services.GenerateWireGuardKeypair()
	if err != nil {
		http.Error(w, "Could not generate WireGuard client key", http.StatusInternalServerError)
		return
	}
	presharedKey, err := services.GenerateWireGuardPresharedKey()
	if err != nil {
		http.Error(w, "Could not generate WireGuard preshared key", http.StatusInternalServerError)
		return
	}
	if candidate.WireGuard.PrivateKey == "" {
		serverPrivate, _, keyErr := services.GenerateWireGuardKeypair()
		if keyErr != nil {
			http.Error(w, "Could not generate WireGuard server key", http.StatusInternalServerError)
			return
		}
		candidate.WireGuard.PrivateKey = serverPrivate
	}
	serverPublic, err := services.WireGuardPublicKey(candidate.WireGuard.PrivateKey)
	if err != nil {
		http.Error(w, "Stored WireGuard server key is invalid", http.StatusInternalServerError)
		return
	}

	idBytes := make([]byte, 18)
	if _, err := rand.Read(idBytes); err != nil {
		http.Error(w, "Could not generate peer identifier", http.StatusInternalServerError)
		return
	}
	peerID := "wg-" + base64.RawURLEncoding.EncodeToString(idBytes)
	candidate.WireGuard.Enabled = true
	candidate.WireGuard.Peers = append(candidate.WireGuard.Peers, config.WireGuardPeer{
		ID:           peerID,
		Name:         req.Name,
		PublicKey:    clientPublic,
		PresharedKey: presharedKey,
		AllowedIPs:   []string{clientCIDR},
		Enabled:      true,
	})
	if err := candidate.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	tx, err := s.engine.ProcessTransaction(fmt.Sprintf("wireguard-peer-%d", time.Now().UnixNano()), candidate)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": err.Error(),
			"tx":    redactTransaction(tx),
		})
		return
	}
	clientBundle, err := services.GenerateClientConfig(
		clientPrivate,
		clientCIDR,
		serverPublic,
		req.ServerEndpoint,
		presharedKey,
		strings.Join(candidate.DHCP.DNSServers, ", "),
		candidate.WireGuard.Address,
		candidate.LAN.CIDR,
	)
	if err != nil {
		// The peer is already active. Do not pretend provisioning completed
		// without its one-time private material; tell the admin to delete it.
		http.Error(w, "Peer applied but client QR generation failed; delete the peer and retry", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"peer": map[string]any{
			"id":         peerID,
			"name":       req.Name,
			"client_ip":  clientCIDR,
			"public_key": clientPublic,
		},
		"client_config": clientBundle.ConfigText,
		"qr_code_data":  clientBundle.QRCodeData,
		"tx":            redactTransaction(tx),
	})
}
