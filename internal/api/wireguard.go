package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
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

func ipv4Uint32(ip net.IP) (uint32, bool) {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0, false
	}
	return binary.BigEndian.Uint32(ip4), true
}

func uint32IPv4(value uint32) net.IP {
	ip := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(ip, value)
	return ip
}

// nextFreeWireGuardIP returns the first free usable IPv4 after the server
// address, wrapping once to the start of the subnet when needed. Addresses
// owned by disabled peers remain reserved so re-enabling a peer cannot create a
// duplicate route. Network, broadcast and the server address are never used.
func nextFreeWireGuardIP(serverIP net.IP, subnet string, peers []config.WireGuardPeer) (string, error) {
	used := make(map[uint32]struct{})
	for _, p := range peers {
		for _, allowed := range p.AllowedIPs {
			ipText, network, err := net.ParseCIDR(strings.TrimSpace(allowed))
			if err != nil {
				continue
			}
			ones, bits := network.Mask.Size()
			if bits != 32 || ones != 32 || ipText.To4() == nil {
				continue
			}
			if value, ok := ipv4Uint32(ipText); ok {
				used[value] = struct{}{}
			}
		}
	}

	_, network, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", fmt.Errorf("invalid WireGuard subnet %s", subnet)
	}
	net4 := network.IP.To4()
	server4 := serverIP.To4()
	if net4 == nil || server4 == nil || !network.Contains(server4) {
		return "", fmt.Errorf("WireGuard subnet is not a valid IPv4 network for the server address")
	}
	ones, bits := network.Mask.Size()
	if bits != 32 || ones > 30 {
		return "", fmt.Errorf("no free address available in WireGuard subnet %s", subnet)
	}

	networkValue, _ := ipv4Uint32(net4)
	serverValue, _ := ipv4Uint32(server4)
	hostBits := uint(32 - ones)
	broadcastValue := networkValue | uint32((uint64(1)<<hostBits)-1)
	firstUsable := networkValue + 1
	lastUsable := broadcastValue - 1

	findFree := func(start, end uint32) (string, bool) {
		if start > end {
			return "", false
		}
		for candidate := start; candidate <= end; candidate++ {
			if candidate == serverValue {
				continue
			}
			if _, exists := used[candidate]; exists {
				continue
			}
			return uint32IPv4(candidate).String(), true
		}
		return "", false
	}

	if serverValue < lastUsable {
		if candidate, ok := findFree(serverValue+1, lastUsable); ok {
			return candidate, nil
		}
	}
	if serverValue > firstUsable {
		if candidate, ok := findFree(firstUsable, serverValue-1); ok {
			return candidate, nil
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
	if req.ServerEndpoint == "" {
		http.Error(w, "Configure Dynamic DNS or provide a server_endpoint", http.StatusUnprocessableEntity)
		return
	}
	if err := validateWireGuardEndpoint(req.ServerEndpoint); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	clientIP := net.ParseIP(req.ClientIPAddress)
	if clientIP == nil || clientIP.To4() == nil || !wgNetwork.Contains(clientIP) || clientIP.Equal(serverIP) {
		http.Error(w, "client_ip_address must be a unique IPv4 address inside the WireGuard subnet", http.StatusUnprocessableEntity)
		return
	}
	clientCIDR := clientIP.String() + "/32"
	for _, existing := range candidate.WireGuard.Peers {
		for _, allowed := range existing.AllowedIPs {
			if allowed == clientCIDR {
				http.Error(w, "client_ip_address is already reserved by another WireGuard peer", http.StatusConflict)
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

	// Build all one-time private client material before changing router state.
	// If QR/config generation fails, no unusable peer is left committed.
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
		http.Error(w, "Could not generate WireGuard client configuration", http.StatusInternalServerError)
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

// handleWireGuardProvisioningPreview reports the exact client IP and server
// endpoint the backend will assign to the next WireGuard peer, so the frontend
// never has to duplicate allocation logic (MR-AUD-005).
func (s *Server) handleWireGuardProvisioningPreview(w http.ResponseWriter, r *http.Request) {
	candidate := s.engine.GetCurrentConfig()
	serverIP, _, err := net.ParseCIDR(candidate.WireGuard.Address)
	if err != nil {
		http.Error(w, "WireGuard subnet is invalid", http.StatusUnprocessableEntity)
		return
	}
	clientIP, err := nextFreeWireGuardIP(serverIP, candidate.WireGuard.Address, candidate.WireGuard.Peers)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	endpoint := ""
	if domain := strings.TrimSpace(candidate.Cloudflare.Domain); domain != "" {
		endpoint = net.JoinHostPort(domain, strconv.Itoa(candidate.WireGuard.ListenPort))
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"client_ip":         clientIP,
		"server_endpoint":   endpoint,
		"ddns_configured":   endpoint != "",
		"wireguard_enabled": candidate.WireGuard.Enabled,
	})
}
