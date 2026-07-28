package services

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultUpdateURL     = "https://updates.minimalrouter.io"
	updateManifestFile   = "manifest.json"
	updatePackageFile    = "minimalrouter.apk"
	updateSignatureFile  = "minimalrouter.sig"
	updateStagingDir     = "/var/lib/minimalrouter-update/staging"
	updateInstalledDir   = "/var/lib/minimalrouter-update/installed"
	updateReceiptFile    = "verified.json"
	maxUpdatePackageSize = 256 << 20
)

// UpdateManifest describes an available system update.
type UpdateManifest struct {
	Version      string `json:"version"`
	ReleaseDate  string `json:"release_date"`
	ReleaseNote  string `json:"release_notes"`
	PackageURL   string `json:"package_url"`
	SignatureURL string `json:"signature_url"`
	Checksum     string `json:"sha256"`
	Size         int64  `json:"size"`
	MinVersion   string `json:"min_version"`
}

// UpdateStatus represents the current update state.
type UpdateStatus struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	Downloaded      bool   `json:"downloaded"`
	Verified        bool   `json:"verified"`
	Error           string `json:"error,omitempty"`
}

type verificationReceipt struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// CheckForUpdate queries the update server for available packages.
func CheckForUpdate(currentVersion, updateURL string) (*UpdateManifest, error) {
	if updateURL == "" {
		updateURL = defaultUpdateURL
	}

	manifestURL, err := secureUpdateURL(updateURL, updateManifestFile)
	if err != nil {
		return nil, err
	}
	client := secureUpdateClient(manifestURL)
	resp, err := client.Get(manifestURL.String())
	if err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update server returned HTTP %d", resp.StatusCode)
	}

	var manifest UpdateManifest
	decoder := json.NewDecoder(io.LimitReader(resp.Body, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("parse update manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("update manifest contains trailing data")
	}
	if err := validateUpdateManifest(&manifest, updateURL); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// DownloadAndVerifyUpdate downloads a signed update package and verifies its
// Ed25519 signature against the trusted signing key.
func DownloadAndVerifyUpdate(manifest *UpdateManifest, trustedKey ed25519.PublicKey, updateURL string) error {
	if updateURL == "" {
		updateURL = defaultUpdateURL
	}

	if len(trustedKey) != ed25519.PublicKeySize {
		return fmt.Errorf("trusted Ed25519 update key is required")
	}
	if err := validateUpdateManifest(manifest, updateURL); err != nil {
		return err
	}
	if err := os.MkdirAll(updateStagingDir, 0700); err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}

	// Download package
	pkgPath := filepath.Join(updateStagingDir, updatePackageFile)
	pkgURL, _ := secureUpdateURL(updateURL, manifest.PackageURL)
	if err := downloadFile(pkgURL, pkgPath, manifest.Size); err != nil {
		return fmt.Errorf("download package: %w", err)
	}

	// Verify checksum
	pkgData, err := os.ReadFile(pkgPath)
	if err != nil {
		return fmt.Errorf("read package: %w", err)
	}
	hash := sha256.Sum256(pkgData)
	actualChecksum := hex.EncodeToString(hash[:])
	if actualChecksum != strings.ToLower(manifest.Checksum) {
		os.Remove(pkgPath)
		return fmt.Errorf("checksum mismatch: expected %s, got %s", manifest.Checksum, actualChecksum)
	}

	// Download signature
	sigPath := filepath.Join(updateStagingDir, updateSignatureFile)
	sigURL, _ := secureUpdateURL(updateURL, manifest.SignatureURL)
	if err := downloadFile(sigURL, sigPath, ed25519.SignatureSize); err != nil {
		os.Remove(pkgPath)
		return fmt.Errorf("download signature: %w", err)
	}

	sigData, err := os.ReadFile(sigPath)
	if err != nil {
		os.Remove(pkgPath)
		return fmt.Errorf("read signature: %w", err)
	}

	// Verify Ed25519 signature
	if len(sigData) != ed25519.SignatureSize || !ed25519.Verify(trustedKey, pkgData, sigData) {
		_ = os.Remove(pkgPath)
		_ = os.Remove(sigPath)
		return fmt.Errorf("signature verification failed — package may be tampered")
	}

	// Save manifest for install step
	manifestPath := filepath.Join(updateStagingDir, updateManifestFile)
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(manifestPath, manifestData, 0600); err != nil {
		return fmt.Errorf("save manifest: %w", err)
	}
	receiptData, err := json.Marshal(verificationReceipt{SHA256: actualChecksum, Size: int64(len(pkgData))})
	if err != nil {
		return fmt.Errorf("create verification receipt: %w", err)
	}
	if err := os.WriteFile(filepath.Join(updateStagingDir, updateReceiptFile), receiptData, 0600); err != nil {
		return fmt.Errorf("save verification receipt: %w", err)
	}

	return nil
}

// InstallUpdate applies a previously downloaded and verified update package.
// On Alpine Linux, this uses apk to install the package.
func InstallUpdate() error {
	pkgPath := filepath.Join(updateStagingDir, updatePackageFile)
	pkgData, err := os.ReadFile(pkgPath)
	if err != nil {
		return fmt.Errorf("no update package found in staging")
	}
	var receipt verificationReceipt
	receiptData, err := os.ReadFile(filepath.Join(updateStagingDir, updateReceiptFile))
	if err != nil || json.Unmarshal(receiptData, &receipt) != nil {
		return fmt.Errorf("update package has no valid verification receipt")
	}
	hash := sha256.Sum256(pkgData)
	if len(pkgData) == 0 || int64(len(pkgData)) != receipt.Size ||
		hex.EncodeToString(hash[:]) != receipt.SHA256 {
		return fmt.Errorf("staged update changed after verification")
	}

	// Create installed dir for rollback
	if err := os.MkdirAll(updateInstalledDir, 0700); err != nil {
		return fmt.Errorf("create installed dir: %w", err)
	}

	// Install via apk
	// apk must independently validate the APK's Alpine package signature.
	cmd := exec.Command("/sbin/apk", "add", "--no-interactive", pkgPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install update: %w", err)
	}

	// Clean staging
	os.RemoveAll(updateStagingDir)

	return nil
}

func downloadFile(source *url.URL, dest string, expectedSize int64) error {
	if expectedSize <= 0 || expectedSize > maxUpdatePackageSize {
		return fmt.Errorf("invalid expected download size")
	}
	client := secureUpdateClient(source)
	resp, err := client.Get(source.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	written, err := io.Copy(f, io.LimitReader(resp.Body, expectedSize+1))
	if err != nil {
		return err
	}
	if written != expectedSize {
		return fmt.Errorf("download size mismatch: expected %d, received %d", expectedSize, written)
	}
	return f.Sync()
}

func secureUpdateURL(baseURL, relative string) (*url.URL, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil {
		return nil, fmt.Errorf("update base URL must be absolute HTTPS")
	}
	ref, err := url.Parse(relative)
	if err != nil || ref.IsAbs() || ref.Host != "" || ref.User != nil ||
		ref.Fragment != "" || strings.HasPrefix(ref.Path, "/") {
		return nil, fmt.Errorf("update artifact URL must be a safe relative path")
	}
	resolved := base.ResolveReference(ref)
	if !strings.EqualFold(resolved.Host, base.Host) || resolved.Scheme != "https" {
		return nil, fmt.Errorf("update artifact URL escaped the trusted origin")
	}
	return resolved, nil
}

func secureUpdateClient(origin *url.URL) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || req.URL.Scheme != "https" ||
				!strings.EqualFold(req.URL.Host, origin.Host) {
				return fmt.Errorf("unsafe update redirect rejected")
			}
			return nil
		},
	}
}

func validateUpdateManifest(manifest *UpdateManifest, updateURL string) error {
	if manifest == nil || manifest.Version == "" || len(manifest.Version) > 64 {
		return fmt.Errorf("manifest version is missing or invalid")
	}
	if manifest.Size <= 0 || manifest.Size > maxUpdatePackageSize {
		return fmt.Errorf("manifest package size is invalid")
	}
	checksum, err := hex.DecodeString(manifest.Checksum)
	if err != nil || len(checksum) != sha256.Size {
		return fmt.Errorf("manifest must contain a valid SHA-256 checksum")
	}
	if _, err := secureUpdateURL(updateURL, manifest.PackageURL); err != nil {
		return err
	}
	if _, err := secureUpdateURL(updateURL, manifest.SignatureURL); err != nil {
		return err
	}
	return nil
}
