package release

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/vladimirperovic/minimalrouter/internal/firmware"
)

func copyUploadedReleaseFile(source, destination string, maximum int64) error {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("uploaded release file is missing or unsafe")
	}
	if info.Size() < 0 || info.Size() > maximum {
		return errors.New("uploaded release file exceeds size limit")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(output, io.LimitReader(input, maximum+1))
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maximum {
		return errors.New("uploaded release file exceeds size limit")
	}
	return nil
}

// PrepareLocalRelease moves a browser-uploaded signed manifest and release
// archive into the fixed private inbox, then performs the same safe extraction
// used for published releases. Cryptographic verification remains the root
// updater's responsibility and happens before any slot can be staged.
func PrepareLocalRelease(manifestSource, archiveSource, arch, destination string) (string, string, string, error) {
	if arch != "amd64" && arch != "arm64" {
		return "", "", "", fmt.Errorf("unsupported update architecture %q", arch)
	}
	if err := os.RemoveAll(destination); err != nil {
		return "", "", "", err
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return "", "", "", err
	}
	if err := os.Chmod(destination, 0o700); err != nil {
		return "", "", "", err
	}

	manifestPath := filepath.Join(destination, "manifest.json")
	archivePath := filepath.Join(destination, "release.tar.gz")
	if err := copyUploadedReleaseFile(manifestSource, manifestPath, maxReleaseManifest); err != nil {
		return "", "", "", fmt.Errorf("copy signed manifest: %w", err)
	}
	manifest, err := firmware.LoadManifest(manifestPath)
	if err != nil {
		return "", "", "", fmt.Errorf("read signed manifest: %w", err)
	}
	if !firmware.IsReleaseVersion(manifest.Version) {
		return "", "", "", errors.New("uploaded manifest has an invalid release version")
	}
	if err := copyUploadedReleaseFile(archiveSource, archivePath, maxReleaseArchive); err != nil {
		return "", "", "", fmt.Errorf("copy release archive: %w", err)
	}
	extractRoot := filepath.Join(destination, "release")
	if err := os.MkdirAll(extractRoot, 0o700); err != nil {
		return "", "", "", err
	}
	payloadRoot, err := extractReleaseArchive(archivePath, extractRoot, arch)
	if err != nil {
		return "", "", "", err
	}
	if err := os.Remove(archivePath); err != nil {
		return "", "", "", err
	}
	return payloadRoot, manifestPath, manifest.Version, nil
}
