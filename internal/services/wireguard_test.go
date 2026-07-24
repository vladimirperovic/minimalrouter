package services

import (
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
				ID:        "peer1",
				Name:      "Vlad iPhone",
				PublicKey: "sOmEPuBlIcKeY=",
				AllowedIPs: []string{"10.0.0.2/32"},
				Enabled:   true,
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
}

func TestGenerateClientConfig(t *testing.T) {
	bundle := GenerateClientConfig(
		"clientPrivateKey123=",
		"10.0.0.2/32",
		"serverPublicKey456=",
		"185.33.42.117:51820",
		"pskKey789=",
		"1.1.1.1",
	)

	if !strings.Contains(bundle.ConfigText, "[Interface]") {
		t.Errorf("Expected [Interface] section in client config")
	}
	if !strings.Contains(bundle.ConfigText, "Endpoint = 185.33.42.117:51820") {
		t.Errorf("Expected endpoint in client config")
	}
}
