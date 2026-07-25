package firmware

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrInvalidSignature       = errors.New("invalid firmware signature")
	ErrInvalidPublicKey       = errors.New("invalid trusted public key (must be 32 bytes Ed25519)")
	ErrInvalidSignatureFormat = errors.New("invalid signature format (must be 64 bytes Ed25519)")
	ErrUntrustedPublicKey     = errors.New("manifest signer does not match the pinned firmware key")
)

// FirmwareManifest represents the signed firmware manifest.
type FirmwareManifest struct {
	Version   string            `json:"version"`
	BuildDate string            `json:"build_date"`
	GitCommit string            `json:"git_commit"`
	Files     map[string]string `json:"files"`     // path -> sha256
	Signature string            `json:"signature"` // hex-encoded Ed25519 signature
	// PublicKey is informational only. Verification always uses a key pinned
	// in the installed operating system, never this manifest-supplied value.
	PublicKey string `json:"public_key,omitempty"`
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
		if !info.Mode().IsRegular() {
			return fmt.Errorf("firmware contains non-regular file: %s", path)
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

	manifestBytes, err := signedPayload(manifest)
	if err != nil {
		return nil, err
	}

	// Sign
	sig := ed25519.Sign(privKey, manifestBytes)
	manifest.Signature = hex.EncodeToString(sig)
	manifest.PublicKey = hex.EncodeToString(privKey.Public().(ed25519.PublicKey))

	return manifest, nil
}

func signedPayload(manifest *FirmwareManifest) ([]byte, error) {
	return json.Marshal(struct {
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
}

// VerifyManifest verifies signed metadata against an operating-system-pinned
// trust anchor. The public key embedded in an untrusted manifest is never used.
func VerifyManifest(manifest *FirmwareManifest, trustedKey ed25519.PublicKey) error {
	if len(trustedKey) != ed25519.PublicKeySize {
		return ErrInvalidPublicKey
	}
	if manifest == nil || manifest.Version == "" || len(manifest.Files) == 0 {
		return errors.New("incomplete firmware manifest")
	}
	if manifest.PublicKey != "" {
		claimed, err := hex.DecodeString(manifest.PublicKey)
		if err != nil || len(claimed) != ed25519.PublicKeySize ||
			subtle.ConstantTimeCompare(claimed, trustedKey) != 1 {
			return ErrUntrustedPublicKey
		}
	}

	sigBytes, err := hex.DecodeString(manifest.Signature)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return ErrInvalidSignatureFormat
	}

	manifestBytes, err := signedPayload(manifest)
	if err != nil {
		return err
	}
	if !ed25519.Verify(trustedKey, manifestBytes, sigBytes) {
		return ErrInvalidSignature
	}
	return nil
}

// VerifyFirmware verifies signed metadata and every regular file in a fixed,
// already-extracted staging directory.
func VerifyFirmware(firmwareDir string, manifest *FirmwareManifest, trustedKey ed25519.PublicKey) error {
	if err := VerifyManifest(manifest, trustedKey); err != nil {
		return err
	}

	for relPath, expectedHash := range manifest.Files {
		clean := filepath.Clean(relPath)
		if clean == "." || filepath.IsAbs(clean) || clean != relPath ||
			strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe firmware path: %q", relPath)
		}
		expected, err := hex.DecodeString(expectedHash)
		if err != nil || len(expected) != sha256.Size {
			return fmt.Errorf("invalid hash for %s", relPath)
		}

		fullPath := filepath.Join(firmwareDir, clean)
		info, err := os.Lstat(fullPath)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("file missing or unsafe: %s", relPath)
		}
		f, err := os.Open(fullPath)
		if err != nil {
			return fmt.Errorf("file missing: %s", relPath)
		}

		hash := sha256.New()
		if _, err := io.Copy(hash, f); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}

		actual := hash.Sum(nil)
		if subtle.ConstantTimeCompare(actual, expected) != 1 {
			return fmt.Errorf("hash mismatch for %s", relPath)
		}
	}

	return nil
}

// LoadTrustedPublicKey reads a hex-encoded Ed25519 key from a root-controlled
// operating-system file.
func LoadTrustedPublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, ErrInvalidPublicKey
	}
	return ed25519.PublicKey(decoded), nil
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
