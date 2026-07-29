package services

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"strings"

	"golang.org/x/crypto/curve25519"
	"rsc.io/qr"
)

// ClientConfigBundle holds client configuration text and QR code payload.
type ClientConfigBundle struct {
	ConfigText string `json:"config_text"`
	QRCodeData string `json:"qr_code_data,omitempty"`
}

// GenerateClientConfig renders a ready-to-import split-tunnel WireGuard client
// configuration. Only the WireGuard subnet and the router's local LAN subnet
// are routed through the tunnel; ordinary client Internet traffic stays on the
// client's current connection.
func GenerateClientConfig(
	clientPrivateKey string,
	clientIP string,
	serverPublicKey string,
	serverEndpoint string,
	presharedKey string,
	dnsServers string,
	lanCIDR ...string,
) (ClientConfigBundle, error) {
	var buf bytes.Buffer

	buf.WriteString("[Interface]\n")
	buf.WriteString(fmt.Sprintf("PrivateKey = %s\n", clientPrivateKey))
	buf.WriteString(fmt.Sprintf("Address = %s\n", clientIP))

	if dnsServers != "" {
		buf.WriteString(fmt.Sprintf("DNS = %s\n", dnsServers))
	}
	buf.WriteString("\n")

	buf.WriteString("[Peer]\n")
	buf.WriteString(fmt.Sprintf("PublicKey = %s\n", serverPublicKey))

	if presharedKey != "" {
		buf.WriteString(fmt.Sprintf("PresharedKey = %s\n", presharedKey))
	}

	if serverEndpoint != "" {
		buf.WriteString(fmt.Sprintf("Endpoint = %s\n", serverEndpoint))
	}

	allowed := make([]string, 0, 2)
	if ip, network, err := net.ParseCIDR(clientIP); err == nil && ip.To4() != nil {
		// The client address identifies the WireGuard subnet. Preserve the subnet
		// prefix while normalizing the network address.
		allowed = append(allowed, network.String())
	}
	if len(lanCIDR) > 0 {
		if ip, network, err := net.ParseCIDR(strings.TrimSpace(lanCIDR[0])); err == nil && ip.To4() != nil {
			lanNetwork := network.String()
			if len(allowed) == 0 || allowed[0] != lanNetwork {
				allowed = append(allowed, lanNetwork)
			}
		}
	}
	if len(allowed) == 0 {
		return ClientConfigBundle{}, fmt.Errorf("client or LAN CIDR is invalid")
	}
	buf.WriteString("AllowedIPs = " + strings.Join(allowed, ", ") + "\n")
	buf.WriteString("PersistentKeepalive = 25\n")

	confText := buf.String()
	code, err := qr.Encode(confText, qr.M)
	if err != nil {
		return ClientConfigBundle{}, fmt.Errorf("encode WireGuard QR: %w", err)
	}

	return ClientConfigBundle{
		ConfigText: confText,
		QRCodeData: "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG()),
	}, nil
}

// GenerateWireGuardKeypair creates an RFC 7748 X25519 keypair in WireGuard's
// standard base64 representation.
func GenerateWireGuardKeypair() (privateKey, publicKey string, err error) {
	private := make([]byte, curve25519.ScalarSize)
	if _, err := rand.Read(private); err != nil {
		return "", "", fmt.Errorf("generate WireGuard private key: %w", err)
	}
	private[0] &= 248
	private[31] &= 127
	private[31] |= 64
	public, err := curve25519.X25519(private, curve25519.Basepoint)
	if err != nil {
		return "", "", fmt.Errorf("derive WireGuard public key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(private), base64.StdEncoding.EncodeToString(public), nil
}

func WireGuardPublicKey(privateKey string) (string, error) {
	private, err := base64.StdEncoding.DecodeString(privateKey)
	if err != nil || len(private) != curve25519.ScalarSize {
		return "", fmt.Errorf("invalid WireGuard private key")
	}
	public, err := curve25519.X25519(private, curve25519.Basepoint)
	if err != nil {
		return "", fmt.Errorf("derive WireGuard public key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(public), nil
}

func GenerateWireGuardPresharedKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("generate WireGuard preshared key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
