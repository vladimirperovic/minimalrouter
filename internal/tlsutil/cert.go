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
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/config"
)

// CertManager handles device certificate generation and loading.
type CertManager struct {
	certPath string
	keyPath  string
}

func NewCertManager(dataDir string) *CertManager {
	return &CertManager{certPath: filepath.Join(dataDir, "server.crt"), keyPath: filepath.Join(dataDir, "server.key")}
}

func (cm *CertManager) EnsureCertificate(cfg *config.SystemConfig) ([]byte, []byte, error) {
	return cm.EnsureCertificateWithAdditionalSANs(cfg, nil, nil)
}

// EnsureCertificateWithAdditionalIPs is kept as a focused convenience wrapper
// for callers that only need temporary address SANs.
func (cm *CertManager) EnsureCertificateWithAdditionalIPs(cfg *config.SystemConfig, additionalIPs []net.IP) ([]byte, []byte, error) {
	return cm.EnsureCertificateWithAdditionalSANs(cfg, additionalIPs, nil)
}

// EnsureCertificateWithAdditionalSANs additionally requires temporary SANs.
// During commit-confirm, routerd uses this for candidate LAN/WireGuard IPs and
// candidate DNS identity while keeping all canonical identities in the same
// certificate. After commit the same certificate remains valid because extra
// SANs are harmless.
func (cm *CertManager) EnsureCertificateWithAdditionalSANs(cfg *config.SystemConfig, additionalIPs []net.IP, additionalDNS []string) ([]byte, []byte, error) {
	additionalIPs = normalizeIPs(additionalIPs)
	additionalDNS = normalizeDNSNames(additionalDNS)
	if certPEM, keyPEM, err := cm.loadExisting(); err == nil {
		if certificateMatchesConfig(certPEM, cfg, additionalIPs, additionalDNS) {
			return certPEM, keyPEM, nil
		}
	}
	return cm.generateSelfSigned(cfg, additionalIPs, additionalDNS)
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

func normalizeDNSNames(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func certificateMatchesConfig(certPEM []byte, cfg *config.SystemConfig, additionalIPs []net.IP, additionalDNS []string) bool {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	now := time.Now()
	// A certificate generated before the clock synchronized may become
	// not-yet-valid after a large wall-clock correction. Conversely, rotate
	// with a month of validity left rather than failing a later handshake.
	if now.Before(cert.NotBefore) || cert.NotAfter.Sub(now) < 30*24*time.Hour {
		return false
	}
	lanIP := net.ParseIP(cfg.LAN.IPAddress)
	if lanIP == nil || cert.VerifyHostname(cfg.LAN.IPAddress) != nil {
		return false
	}
	// WireGuard is optional. Requiring its SAN while the service is disabled
	// caused every TLS handshake to reject and regenerate an otherwise valid
	// certificate when an old/partial configuration carried no WG address.
	if cfg.WireGuard.Enabled {
		wgAddress, _, parseErr := net.ParseCIDR(cfg.WireGuard.Address)
		if parseErr != nil || cert.VerifyHostname(wgAddress.String()) != nil {
			return false
		}
	}
	fqdn := strings.TrimSuffix(strings.TrimSpace(cfg.System.Hostname+"."+cfg.System.Domain), ".")
	if fqdn == "" || cert.VerifyHostname(fqdn) != nil {
		return false
	}
	for _, ip := range normalizeIPs(additionalIPs) {
		if cert.VerifyHostname(ip.String()) != nil {
			return false
		}
	}
	for _, name := range normalizeDNSNames(additionalDNS) {
		if cert.VerifyHostname(name) != nil {
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

func appendUniqueDNS(list []string, name string) []string {
	name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	if name == "" {
		return list
	}
	for _, existing := range list {
		if strings.EqualFold(existing, name) {
			return list
		}
	}
	return append(list, name)
}

func (cm *CertManager) generateSelfSigned(cfg *config.SystemConfig, additionalIPs []net.IP, additionalDNS []string) ([]byte, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	serialNumber, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	ipAddresses := []net.IP{net.IPv4(127, 0, 0, 1)}
	ipAddresses = appendUniqueIP(ipAddresses, net.ParseIP(cfg.LAN.IPAddress))
	if cfg.WireGuard.Enabled {
		if wgAddress, _, parseErr := net.ParseCIDR(cfg.WireGuard.Address); parseErr == nil {
			ipAddresses = appendUniqueIP(ipAddresses, wgAddress)
		}
	}
	for _, ip := range normalizeIPs(additionalIPs) {
		ipAddresses = appendUniqueIP(ipAddresses, ip)
	}
	dnsNames := []string{cfg.System.Hostname + "." + cfg.System.Domain, "localhost"}
	for _, name := range normalizeDNSNames(additionalDNS) {
		dnsNames = appendUniqueDNS(dnsNames, name)
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
		DNSNames:              dnsNames,
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
