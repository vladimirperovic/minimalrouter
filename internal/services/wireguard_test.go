package services

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateWireGuard(t *testing.T) {
	wg := &WireGuardConfig{
		Enabled:    true,
		Interface:  "wg0",
		PrivateKey: "sOmEPriVaTeKeY=",
		ListenPort: 51820,
		Address:    "10.0.0.1/24",
		Peers: []WireGuardPeer{
			{
				ID:         "peer1",
				Name:       "Vlad iPhone",
				PublicKey:  "sOmEPuBlIcKeY=",
				AllowedIPs: []string{"10.0.0.2/32"},
				Enabled:    true,
			},
		},
	}

	out, err := GenerateWireGuard(wg)
	if err != nil {
		t.Fatalf("GenerateWireGuard failed: %v", err)
	}

	if !strings.Contains(out, "PrivateKey = sOmEPriVaTeKeY=") {
		t.Errorf("Expected private key in output")
	}
	if !strings.Contains(out, "PublicKey = sOmEPuBlIcKeY=") {
		t.Errorf("Expected peer public key in output")
	}

	runtimeConfig, err := GenerateWireGuardRuntime(wg)
	if err != nil {
		t.Fatalf("GenerateWireGuardRuntime failed: %v", err)
	}
	if strings.Contains(runtimeConfig, "Address =") {
		t.Fatal("wg setconf runtime input must not contain wg-quick-only Address")
	}
	for _, expected := range []string{
		"PrivateKey = sOmEPriVaTeKeY=",
		"ListenPort = 51820",
		"PublicKey = sOmEPuBlIcKeY=",
		"AllowedIPs = 10.0.0.2/32",
	} {
		if !strings.Contains(runtimeConfig, expected) {
			t.Fatalf("runtime config is missing %q", expected)
		}
	}
}

func TestGenerateClientConfig(t *testing.T) {
	bundle, err := GenerateClientConfig(
		"clientPrivateKey123=",
		"10.0.0.2/32",
		"serverPublicKey456=",
		"185.33.42.117:51820",
		"pskKey789=",
		"1.1.1.1",
	)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(bundle.ConfigText, "[Interface]") {
		t.Errorf("Expected [Interface] section in client config")
	}
	if !strings.Contains(bundle.ConfigText, "Endpoint = 185.33.42.117:51820") {
		t.Errorf("Expected endpoint in client config")
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(bundle.QRCodeData, prefix) {
		t.Fatal("expected a base64 PNG QR data URL")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(bundle.QRCodeData, prefix))
	if err != nil || len(decoded) < 8 || string(decoded[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatal("QR data URL is not a valid PNG")
	}
}

func TestGenerateWireGuardKeys(t *testing.T) {
	privateKey, publicKey, err := GenerateWireGuardKeypair()
	if err != nil {
		t.Fatal(err)
	}
	derived, err := WireGuardPublicKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if derived != publicKey {
		t.Fatal("WireGuard public key derivation is not deterministic")
	}
	decoded, err := base64.StdEncoding.DecodeString(privateKey)
	if err != nil || len(decoded) != 32 {
		t.Fatal("WireGuard private key is not a standard 32-byte base64 value")
	}
}
