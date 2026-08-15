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

// GenerateKeyPair generates a new Ed25519 key pair.
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

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		hash := sha256.New()
		if _, err := io.Copy(hash, f); err != nil {
			return err
		}

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

// ValidateAppliancePayload rejects a correctly signed but incomplete update.
// A/B slots may replace the application binaries and web bundle, but a release
// is only a valid Minimal Router appliance when it also carries the exact
// reviewed system integration files used by the full installer. This prevents
// an accidentally partial CI artifact from becoming the active router slot.
func ValidateAppliancePayload(manifest *FirmwareManifest) error {
	if manifest == nil {
		return errors.New("missing appliance manifest")
	}
	required := []string{
		"web/dist/index.html",
		"slot-exec",
		"compatibility.json",
		"install.sh",
		"init.d/routerd",
		"init.d/router-applyd",
		"init.d/pppoe-wan",
		"sysctl/99-minimalrouter.conf",
		"modules/minimalrouter.conf",
		"logrotate/minimalrouter",
		"ip-up.d-minimalrouter-qos",
	}
	for _, path := range required {
		if _, ok := manifest.Files[path]; !ok {
			return fmt.Errorf("incomplete appliance payload: missing %s", path)
		}
	}

	archSets := [][]string{
		{"bin/routerd-amd64", "bin/router-applyd-amd64", "bin/router-recovery-amd64", "bin/router-update-amd64"},
		{"bin/routerd-arm64", "bin/router-applyd-arm64", "bin/router-recovery-arm64", "bin/router-update-arm64"},
	}
	completeArchitectures := 0
	for _, set := range archSets {
		complete := true
		present := false
		for _, path := range set {
			if _, ok := manifest.Files[path]; ok {
				present = true
			} else {
				complete = false
			}
		}
		if present && !complete {
			return errors.New("incomplete appliance payload: architecture binary set is partial")
		}
		if complete {
			completeArchitectures++
		}
	}
	if completeArchitectures != 1 {
		return errors.New("incomplete appliance payload: exactly one supported architecture binary set is required")
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
