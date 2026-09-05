// Package release fetches, selects and prepares published Minimal Router
// releases for the privileged updater.
//
// It is deliberately separate from internal/firmware. firmware is linked into
// router-update, a bootstrap binary that lives outside the A/B slot and must
// stay byte-identical between releases for an A/B activation to be allowed
// (see docs/WEB-UPDATE.md). Release discovery changes often; keeping it out of
// that package means it can evolve without making every release require the
// full installer.
//
// Nothing here establishes trust. GitHub metadata only proves that an asset
// exists; the pinned Ed25519 key and the root updater decide what may run.
package release

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/firmware"
)

const (
	releaseDownloadBaseURL = "https://github.com/vladimirperovic/minimalrouter/releases/download/"
	maxReleaseManifest     = 1 << 20
	maxReleaseArchive      = 128 << 20
	maxExpandedRelease     = 256 << 20
)

func canonicalReleaseAssetURL(tag, name string) string {
	return releaseDownloadBaseURL + url.PathEscape(tag) + "/" + url.PathEscape(name)
}

func releaseHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("too many release download redirects")
			}
			if req.URL.Scheme != "https" {
				return errors.New("release download redirected away from HTTPS")
			}
			return nil
		},
	}
}

func downloadReleaseFile(ctx context.Context, assetURL, destination string, maximum int64) error {
	parsed, err := url.Parse(assetURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" ||
		!strings.HasPrefix(parsed.EscapedPath(), "/vladimirperovic/minimalrouter/releases/download/") {
		return errors.New("release asset URL is outside the canonical Minimal Router GitHub release path")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := releaseHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("release asset returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maximum {
		return errors.New("release asset exceeds size limit")
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, maximum+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written > maximum {
		return errors.New("release asset exceeds size limit")
	}
	return nil
}

func safeArchivePath(name, prefix string) (string, error) {
	clean := path.Clean(strings.TrimSpace(name))
	if clean == "." || clean == "" || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("unsafe release archive path %q", name)
	}
	if clean != prefix && !strings.HasPrefix(clean, prefix+"/") {
		return "", fmt.Errorf("release archive contains unexpected top-level path %q", name)
	}
	return clean, nil
}

func extractReleaseArchive(archivePath, destination, arch string) (string, error) {
	if arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("unsupported update architecture %q", arch)
	}
	prefix := "minimalrouter-linux-" + arch
	archive, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer archive.Close()
	gz, err := gzip.NewReader(archive)
	if err != nil {
		return "", fmt.Errorf("open release archive: %w", err)
	}
	defer gz.Close()

	if err := os.MkdirAll(destination, 0o700); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return "", fmt.Errorf("open release extraction root: %w", err)
	}
	defer root.Close()

	reader := tar.NewReader(gz)
	var expanded int64
	seenRoot := false

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read release archive: %w", err)
		}
		clean, err := safeArchivePath(header.Name, prefix)
		if err != nil {
			return "", err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(clean, 0o755); err != nil {
				return "", err
			}
			if err := root.Chmod(clean, os.FileMode(header.Mode)&os.ModePerm); err != nil {
				return "", err
			}
			if clean == prefix {
				seenRoot = true
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maxReleaseArchive || expanded > maxExpandedRelease-header.Size {
				return "", errors.New("expanded release exceeds size limit")
			}
			expanded += header.Size
			parent := path.Dir(clean)
			if parent != "." {
				if err := root.MkdirAll(parent, 0o755); err != nil {
					return "", err
				}
			}
			file, err := root.OpenFile(clean, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return "", err
			}
			written, copyErr := io.CopyN(file, reader, header.Size)
			closeErr := file.Close()
			if copyErr != nil || written != header.Size {
				if copyErr != nil {
					return "", copyErr
				}
				return "", errors.New("short release archive entry")
			}
			if closeErr != nil {
				return "", closeErr
			}
			if err := root.Chmod(clean, os.FileMode(header.Mode)&os.ModePerm); err != nil {
				return "", err
			}
			seenRoot = true
		default:
			return "", fmt.Errorf("release archive contains unsupported entry type at %q", header.Name)
		}
	}
	if !seenRoot {
		return "", errors.New("release archive is empty")
	}
	info, err := root.Lstat(prefix)
	if err != nil || !info.IsDir() {
		return "", errors.New("release archive payload root is missing")
	}
	return filepath.Join(destination, prefix), nil
}

// PreparePublished downloads the architecture-specific signed manifest and
// archive into a private routerd-owned inbox and safely extracts regular files
// only. The root updater performs the authoritative signature, hash, mode,
// architecture, forward-version and A/B-slot checks afterwards.
func PreparePublished(ctx context.Context, published Release, arch, destination string) (string, string, error) {
	if arch != "amd64" && arch != "arm64" {
		return "", "", fmt.Errorf("unsupported update architecture %q", arch)
	}
	if !firmware.IsReleaseVersion(published.Version) || !firmware.IsReleaseVersion(published.Tag) {
		return "", "", errors.New("invalid published release version")
	}
	archiveName, manifestName := payloadAssetNames(arch)
	archiveURL := published.assets[archiveName]
	manifestURL := published.assets[manifestName]
	if archiveURL == "" || manifestURL == "" {
		return "", "", fmt.Errorf("release %s does not contain %s and %s", published.Tag, archiveName, manifestName)
	}

	if err := os.RemoveAll(destination); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return "", "", err
	}
	if err := os.Chmod(destination, 0o700); err != nil {
		return "", "", err
	}
	manifestPath := filepath.Join(destination, "manifest.json")
	archiveFilePath := filepath.Join(destination, "release.tar.gz")
	if err := downloadReleaseFile(ctx, manifestURL, manifestPath, maxReleaseManifest); err != nil {
		return "", "", fmt.Errorf("download signed manifest: %w", err)
	}
	manifest, err := firmware.LoadManifest(manifestPath)
	if err != nil {
		return "", "", fmt.Errorf("read signed manifest: %w", err)
	}
	if manifest.Version != published.Version {
		return "", "", fmt.Errorf("release tag %s does not match manifest version %s", published.Tag, manifest.Version)
	}
	if err := downloadReleaseFile(ctx, archiveURL, archiveFilePath, maxReleaseArchive); err != nil {
		return "", "", fmt.Errorf("download release archive: %w", err)
	}
	extractRoot := filepath.Join(destination, "release")
	if err := os.MkdirAll(extractRoot, 0o700); err != nil {
		return "", "", err
	}
	payloadRoot, err := extractReleaseArchive(archiveFilePath, extractRoot, arch)
	if err != nil {
		return "", "", err
	}
	if err := os.Remove(archiveFilePath); err != nil {
		return "", "", err
	}
	return payloadRoot, manifestPath, nil
}
