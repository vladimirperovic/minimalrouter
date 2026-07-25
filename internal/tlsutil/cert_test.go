package tlsutil

import (
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestCertificateCoversLANAndConfiguredWireGuardAddress(t *testing.T) {
	cfg := config.DefaultConfig()
	manager := NewCertManager(t.TempDir())
	certPEM, _, err := manager.EnsureCertificate(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("certificate PEM missing")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{cfg.LAN.IPAddress, "10.8.0.1"} {
		if err := cert.VerifyHostname(address); err != nil {
			t.Fatalf("certificate does not cover management address %s: %v", address, err)
		}
	}
}
