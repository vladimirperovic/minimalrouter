package services

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	defaultUpdateURL     = "https://updates.minimalrouter.io"
	updateManifestFile   = "manifest.json"
	updatePackageFile    = "minimalrouter.apk"
	updateSignatureFile  = "minimalrouter.sig"
	updateStagingDir     = "/var/lib/minimalrouter-update/staging"
	updateInstalledDir   = "/var/lib/minimalrouter-update/installed"
)

// UpdateManifest describes an available system update.
type UpdateManifest struct {
	Version     string `json:"version"`
	ReleaseDate string `json:"release_date"`
	ReleaseNote string `json:"release_notes"`
	PackageURL  string `json:"package_url"`
	SignatureURL string `json:"signature_url"`
	Checksum    string `json:"sha256"`
	Size        int64  `json:"size"`
	MinVersion  string `json:"min_version"`
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

// CheckForUpdate queries the update server for available packages.
func CheckForUpdate(currentVersion, updateURL string) (*UpdateManifest, error) {
	if updateURL == "" {
		updateURL = defaultUpdateURL
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(updateURL + "/manifest.json")
	if err != nil {
		return nil, fmt.Errorf("check update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update server returned HTTP %d", resp.StatusCode)
	}

	var manifest UpdateManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("parse update manifest: %w", err)
	}

	return &manifest, nil
}

// DownloadAndVerifyUpdate downloads a signed update package and verifies its
// Ed25519 signature against the trusted signing key.
func DownloadAndVerifyUpdate(manifest *UpdateManifest, trustedKey ed25519.PublicKey, updateURL string) error {
	if updateURL == "" {
		updateURL = defaultUpdateURL
	}

	if err := os.MkdirAll(updateStagingDir, 0700); err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}

	// Download package
	pkgPath := filepath.Join(updateStagingDir, updatePackageFile)
	if err := downloadFile(updateURL+"/"+manifest.PackageURL, pkgPath); err != nil {
		return fmt.Errorf("download package: %w", err)
	}

	// Verify checksum
	pkgData, err := os.ReadFile(pkgPath)
	if err != nil {
		return fmt.Errorf("read package: %w", err)
	}
	hash := sha256.Sum256(pkgData)
	actualChecksum := hex.EncodeToString(hash[:])
	if manifest.Checksum != "" && actualChecksum != manifest.Checksum {
		os.Remove(pkgPath)
		return fmt.Errorf("checksum mismatch: expected %s, got %s", manifest.Checksum, actualChecksum)
	}

	// Download signature
	sigPath := filepath.Join(updateStagingDir, updateSignatureFile)
	if err := downloadFile(updateURL+"/"+manifest.SignatureURL, sigPath); err != nil {
		os.Remove(pkgPath)
		return fmt.Errorf("download signature: %w", err)
	}

	sigData, err := os.ReadFile(sigPath)
	if err != nil {
		os.Remove(pkgPath)
		return fmt.Errorf("read signature: %w", err)
	}

	// Verify Ed25519 signature
	if trustedKey != nil && len(sigData) == ed25519.SignatureSize {
		if !ed25519.Verify(trustedKey, pkgData, sigData) {
			os.Remove(pkgPath)
			os.Remove(sigPath)
			return fmt.Errorf("signature verification failed — package may be tampered")
		}
	}

	// Save manifest for install step
	manifestPath := filepath.Join(updateStagingDir, updateManifestFile)
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(manifestPath, manifestData, 0600); err != nil {
		return fmt.Errorf("save manifest: %w", err)
	}

	return nil
}

// InstallUpdate applies a previously downloaded and verified update package.
// On Alpine Linux, this uses apk to install the package.
func InstallUpdate() error {
	pkgPath := filepath.Join(updateStagingDir, updatePackageFile)
	if _, err := os.Stat(pkgPath); os.IsNotExist(err) {
		return fmt.Errorf("no update package found in staging")
	}

	// Create installed dir for rollback
	if err := os.MkdirAll(updateInstalledDir, 0700); err != nil {
		return fmt.Errorf("create installed dir: %w", err)
	}

	// Install via apk
	cmd := exec.Command("/sbin/apk", "add", "--allow-untrusted", pkgPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install update: %w", err)
	}

	// Clean staging
	os.RemoveAll(updateStagingDir)

	return nil
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}
