package firmware

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var (
	ErrInvalidSignature = errors.New("invalid firmware signature")
	ErrInvalidPublicKey = errors.New("invalid public key (must be 32 bytes Ed25519)")
	ErrInvalidSignatureFormat = errors.New("invalid signature format (must be 64 bytes Ed25519)")
)

// FirmwareManifest represents the signed firmware manifest.
type FirmwareManifest struct {
	Version     string            `json:"version"`
	BuildDate   string            `json:"build_date"`
	GitCommit   string            `json:"git_commit"`
	Files       map[string]string `json:"files"`       // path -> sha256
	Signature   string            `json:"signature"`   // hex-encoded Ed25519 signature
	PublicKey   string            `json:"public_key"`  // hex-encoded Ed25519 public key
}

// GenerateKeyPair generates a new Ed25519 key pair for firmware signing.
func GenerateKeyPair() (pubKey, privKey []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, nil, err
	}
	return pub, priv, nil
}

// SignFirmware signs a firmware directory with the private key.
// Returns the manifest with signature and public key.
func SignFirmware(firmwareDir string, privKey ed25519.PrivateKey) (*FirmwareManifest, error) {
	files := make(map[string]string)

	err := filepath.Walk(firmwareDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Compute SHA256 of file
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		hash := sha256.New()
		if _, err := io.Copy(hash, f); err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(firmwareDir, path)
		if err != nil {
			return err
		}

		files[relPath] = hex.EncodeToString(hash.Sum(nil))
		return nil
	})
	if err != nil {
		return nil, err
	}

	manifest := &FirmwareManifest{
		Version:   "1.0.0",
		BuildDate: "2024-01-01T00:00:00Z",
		GitCommit: "unknown",
		Files:     files,
	}

	// Serialize manifest for signing (without signature)
	manifestBytes, err := json.Marshal(struct {
		Version   string            `json:"version"`
		BuildDate string            `json:"build_date"`
		GitCommit string            `json:"git_commit"`
		Files     map[string]string `json:"files"`
	}{
		Version:   manifest.Version,
		BuildDate: manifest.BuildDate,
		GitCommit: manifest.GitCommit,
		Files:     manifest.Files,
	})
	if err != nil {
		return nil, err
	}

	// Sign
	sig := ed25519.Sign(privKey, manifestBytes)
	manifest.Signature = hex.EncodeToString(sig)
	manifest.PublicKey = hex.EncodeToString(privKey.Public().(ed25519.PublicKey))

	return manifest, nil
}

// VerifyFirmware verifies a firmware directory against a manifest.
func VerifyFirmware(firmwareDir string, manifest *FirmwareManifest) error {
	// Decode public key
	pubKeyBytes, err := hex.DecodeString(manifest.PublicKey)
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		return ErrInvalidPublicKey
	}
	pubKey := ed25519.PublicKey(pubKeyBytes)

	// Decode signature
	sigBytes, err := hex.DecodeString(manifest.Signature)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return ErrInvalidSignatureFormat
	}

	// Recompute manifest bytes (without signature)
	manifestBytes, err := json.Marshal(struct {
		Version   string            `json:"version"`
		BuildDate string            `json:"build_date"`
		GitCommit string            `json:"git_commit"`
		Files     map[string]string `json:"files"`
	}{
		Version:   manifest.Version,
		BuildDate: manifest.BuildDate,
		GitCommit: manifest.GitCommit,
		Files:     manifest.Files,
	})
	if err != nil {
		return err
	}

	// Verify signature
	if !ed25519.Verify(pubKey, manifestBytes, sigBytes) {
		return ErrInvalidSignature
	}

	// Verify each file hash matches
	for relPath, expectedHash := range manifest.Files {
		fullPath := filepath.Join(firmwareDir, relPath)
		f, err := os.Open(fullPath)
		if err != nil {
			return fmt.Errorf("file missing: %s", relPath)
		}
		defer f.Close()

		hash := sha256.New()
		if _, err := io.Copy(hash, f); err != nil {
			return err
		}

		actualHash := hex.EncodeToString(hash.Sum(nil))
		if actualHash != expectedHash {
			return fmt.Errorf("hash mismatch for %s: expected %s, got %s", relPath, expectedHash, actualHash)
		}
	}

	return nil
}

// LoadManifest loads a firmware manifest from JSON file.
func LoadManifest(path string) (*FirmwareManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var manifest FirmwareManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// SaveManifest saves a firmware manifest to JSON file.
func SaveManifest(manifest *FirmwareManifest, path string) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}