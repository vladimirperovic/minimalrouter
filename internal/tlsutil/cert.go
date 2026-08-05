package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
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
	return &CertManager{certPath: filepath.Join(dataDir, "server.crt"), keyPath: filepath.Join(dataDir, "server.key")}
}

// EnsureCertificate generates a self-signed certificate if none exists, or
// loads an existing certificate/key pair that covers the canonical addresses.
func (cm *CertManager) EnsureCertificate(cfg *config.SystemConfig) ([]byte, []byte, error) {
	return cm.EnsureCertificateWithAdditionalIPs(cfg, nil)
}

// EnsureCertificateWithAdditionalIPs additionally requires temporary SANs.
// During commit-confirm, routerd uses this for the candidate LAN/WireGuard IP
// while keeping the canonical address in the same certificate. This prevents a
// strict TLS client from being forced to confirm a management path whose IP is
// missing from the certificate exactly during the rollback window.
func (cm *CertManager) EnsureCertificateWithAdditionalIPs(cfg *config.SystemConfig, additionalIPs []net.IP) ([]byte, []byte, error) {
	additionalIPs = normalizeIPs(additionalIPs)
	if certPEM, keyPEM, err := cm.loadExisting(); err == nil {
		if certificateMatchesConfig(certPEM, cfg, additionalIPs) {
			return certPEM, keyPEM, nil
		}
	}
	return cm.generateSelfSigned(cfg, additionalIPs)
}

func normalizeIPs(in []net.IP) []net.IP {
	seen := make(map[string]struct{}, len(in))
	out := make([]net.IP, 0, len(in))
	for _, ip := range in {
		if ip == nil {
			continue
		}
		canonical := ip
		if v4 := ip.To4(); v4 != nil {
			canonical = v4
		}
		key := canonical.String()
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, append(net.IP(nil), canonical...))
	}
	return out
}

func certificateMatchesConfig(certPEM []byte, cfg *config.SystemConfig, additionalIPs []net.IP) bool {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	lanIP := net.ParseIP(cfg.LAN.IPAddress)
	if lanIP == nil || time.Until(cert.NotAfter) < 30*24*time.Hour {
		return false
	}
	if err := cert.VerifyHostname(cfg.LAN.IPAddress); err != nil {
		return false
	}
	wgAddress, _, err := net.ParseCIDR(cfg.WireGuard.Address)
	if err != nil || cert.VerifyHostname(wgAddress.String()) != nil {
		return false
	}
	if cert.VerifyHostname(cfg.System.Hostname+"."+cfg.System.Domain) != nil {
		return false
	}
	for _, ip := range normalizeIPs(additionalIPs) {
		if cert.VerifyHostname(ip.String()) != nil {
			return false
		}
	}
	return true
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
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return nil, nil, err
	}
	return certPEM, keyPEM, nil
}

func appendUniqueIP(list []net.IP, ip net.IP) []net.IP {
	if ip == nil {
		return list
	}
	key := ip.String()
	for _, existing := range list {
		if existing.String() == key {
			return list
		}
	}
	return append(list, ip)
}

func (cm *CertManager) generateSelfSigned(cfg *config.SystemConfig, additionalIPs []net.IP) ([]byte, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	serialNumber, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	ipAddresses := []net.IP{net.IPv4(127, 0, 0, 1)}
	ipAddresses = appendUniqueIP(ipAddresses, net.ParseIP(cfg.LAN.IPAddress))
	if wgAddress, _, err := net.ParseCIDR(cfg.WireGuard.Address); err == nil {
		ipAddresses = appendUniqueIP(ipAddresses, wgAddress)
	}
	for _, ip := range normalizeIPs(additionalIPs) {
		ipAddresses = appendUniqueIP(ipAddresses, ip)
	}
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Minimal Router OS"},
			CommonName:   cfg.System.Hostname + "." + cfg.System.Domain,
		},
		NotBefore:             time.Now().Add(-5 * time.Minute),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           ipAddresses,
		DNSNames:              []string{cfg.System.Hostname + "." + cfg.System.Domain, "localhost"},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})

	if err := writeAtomic(cm.keyPath, keyPEM, 0600); err != nil {
		return nil, nil, fmt.Errorf("failed to write key: %w", err)
	}
	if err := writeAtomic(cm.certPath, certPEM, 0600); err != nil {
		return nil, nil, fmt.Errorf("failed to write cert: %w", err)
	}
	return certPEM, keyPEM, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tls-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
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
	hash := sha256.Sum256(cert.Raw)
	return fmt.Sprintf("%x", hash[:]), nil
}
