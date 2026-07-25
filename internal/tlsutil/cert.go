package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// CertManager handles device certificate generation and loading.
type CertManager struct {
	certPath string
	keyPath  string
}

// NewCertManager creates a certificate manager for the given data directory.
func NewCertManager(dataDir string) *CertManager {
	return &CertManager{
		certPath: filepath.Join(dataDir, "server.crt"),
		keyPath:  filepath.Join(dataDir, "server.key"),
	}
}

// EnsureCertificate generates a self-signed certificate if none exists,
// or loads existing certificate/key pair.
func (cm *CertManager) EnsureCertificate(cfg *config.SystemConfig) ([]byte, []byte, error) {
	// Try to load existing certificate
	if certPEM, keyPEM, err := cm.loadExisting(); err == nil {
		return certPEM, keyPEM, nil
	}

	// Generate new self-signed certificate
	return cm.generateSelfSigned(cfg)
}

func (cm *CertManager) loadExisting() ([]byte, []byte, error) {
	certPEM, err := os.ReadFile(cm.certPath)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(cm.keyPath)
	if err != nil {
		return nil, nil, err
	}
	return certPEM, keyPEM, nil
}

func (cm *CertManager) generateSelfSigned(cfg *config.SystemConfig) ([]byte, []byte, error) {
	// Generate ECDSA P-256 private key
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate template
	serialNumber, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Minimal Router OS"},
			CommonName:   cfg.System.Hostname + "." + cfg.System.Domain,
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour), // 1 year
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)}, // Add LAN IP when available
		DNSNames:              []string{cfg.System.Hostname + "." + cfg.System.Domain, "localhost"},
	}

	// Self-sign
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	// Encode to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})

	// Write to disk (restrictive permissions)
	if err := os.WriteFile(cm.certPath, certPEM, 0600); err != nil {
		return nil, nil, fmt.Errorf("failed to write cert: %w", err)
	}
	if err := os.WriteFile(cm.keyPath, keyPEM, 0600); err != nil {
		return nil, nil, fmt.Errorf("failed to write key: %w", err)
	}

	return certPEM, keyPEM, nil
}

// GetCertificateFingerprint returns SHA256 fingerprint for display during setup.
func GetCertificateFingerprint(certPEM []byte) (string, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", fmt.Errorf("failed to decode certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	// Return SHA256 fingerprint
	hash := sha256.Sum256(cert.Raw)
	return fmt.Sprintf("%x", hash[:]), nil
}