package tlsutil

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"testing"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

func parseTestCertificate(t *testing.T, certPEM []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("certificate PEM did not decode")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

func TestPendingCertificateCoversCanonicalAndCandidateIdentities(t *testing.T) {
	cfg := config.DefaultConfig()
	mgr := NewCertManager(t.TempDir())
	candidateLAN := net.ParseIP("192.168.1.254")
	candidateWG := net.ParseIP("10.9.0.1")
	candidateDNS := "router-new.home.arpa"

	certPEM, _, err := mgr.EnsureCertificateWithAdditionalSANs(&cfg, []net.IP{candidateLAN, candidateWG}, []string{candidateDNS})
	if err != nil {
		t.Fatalf("ensure pending certificate: %v", err)
	}
	cert := parseTestCertificate(t, certPEM)
	for _, identity := range []string{cfg.LAN.IPAddress, "10.8.0.1", cfg.System.Hostname + "." + cfg.System.Domain, candidateLAN.String(), candidateWG.String(), candidateDNS} {
		if err := cert.VerifyHostname(identity); err != nil {
			t.Fatalf("pending certificate does not cover %s: %v", identity, err)
		}
	}
}

func TestPendingCertificateIsReusedAfterCandidateBecomesCanonical(t *testing.T) {
	cfg := config.DefaultConfig()
	mgr := NewCertManager(t.TempDir())
	candidate := net.ParseIP("192.168.1.254")
	certPEM, _, err := mgr.EnsureCertificateWithAdditionalIPs(&cfg, []net.IP{candidate})
	if err != nil {
		t.Fatal(err)
	}
	cfg.LAN.IPAddress = candidate.String()
	cfg.LAN.CIDR = candidate.String() + "/24"
	canonicalPEM, _, err := mgr.EnsureCertificate(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(certPEM) != string(canonicalPEM) {
		t.Fatal("certificate containing pending candidate IP was needlessly rotated after commit")
	}
}
