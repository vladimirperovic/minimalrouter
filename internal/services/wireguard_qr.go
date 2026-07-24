package services

import (
	"bytes"
	"fmt"
	"strings"
)

// ClientConfigBundle holds client configuration text and QR code payload.
type ClientConfigBundle struct {
	ConfigText string `json:"config_text"`
	QRCodeData string `json:"qr_code_data,omitempty"`
}

// GenerateClientConfig renders a ready-to-import WireGuard client config for mobile/desktop.
func GenerateClientConfig(
	clientPrivateKey string,
	clientIP string,
	serverPublicKey string,
	serverEndpoint string,
	presharedKey string,
	dnsServers string,
) ClientConfigBundle {
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

	buf.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	buf.WriteString("PersistentKeepalive = 25\n")

	confText := buf.String()

	return ClientConfigBundle{
		ConfigText: confText,
		QRCodeData: fmt.Sprintf("data:text/plain;base64,%s", strings.TrimSpace(confText)),
	}
}
