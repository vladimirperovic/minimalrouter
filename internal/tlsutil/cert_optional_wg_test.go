package tlsutil

import (
	"bytes"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestDisabledWireGuardDoesNotRotateCertificate(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WireGuard.Enabled = false
	cfg.WireGuard.Address = ""

	manager := NewCertManager(t.TempDir())
	firstCert, firstKey, err := manager.EnsureCertificate(&cfg)
	if err != nil {
		t.Fatalf("first EnsureCertificate: %v", err)
	}
	secondCert, secondKey, err := manager.EnsureCertificate(&cfg)
	if err != nil {
		t.Fatalf("second EnsureCertificate: %v", err)
	}
	if !bytes.Equal(firstCert, secondCert) || !bytes.Equal(firstKey, secondKey) {
		t.Fatal("valid certificate rotated solely because disabled WireGuard had no address")
	}
}
