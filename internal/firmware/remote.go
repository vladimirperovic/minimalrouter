package firmware

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultReleaseAPIURL = "https://api.github.com/repos/vladimirperovic/minimalrouter/releases?per_page=20"
	maxReleaseManifest   = 1 << 20
	maxReleaseArchive    = 128 << 20
	maxExpandedRelease   = 256 << 20
)

var releaseAPIURL = defaultReleaseAPIURL

// PublishedRelease is the minimal trusted-by-policy metadata needed to select
// and fetch a public Minimal Router release. Cryptographic trust is established
// later by the pinned Ed25519 key, never by GitHub metadata alone.
type PublishedRelease struct {
	Version     string
	Prerelease  bool
	PublishedAt time.Time
	assets      map[string]string
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func releaseHTTPClient() *http.Client {
	redirects := 0
	return &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			redirects++
			if redirects > 5 || len(via) > 5 {
				return errors.New("too many release download redirects")
			}
			if req.URL.Scheme != "https" {
				return errors.New("release download redirected away from HTTPS")
			}
			return nil
		},
	}
}

func getReleaseJSON(ctx context.Context, endpoint string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "minimalrouter-update")
	resp, err := releaseHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("release service returned HTTP %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxReleaseManifest))
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("decode release metadata: %w", err)
	}
	return nil
}

// LatestPublishedRelease selects the highest valid non-draft SemVer release.
// Beta/prerelease builds are deliberately eligible because Minimal Router is
// currently distributed as a signed Beta.
func LatestPublishedRelease(ctx context.Context) (PublishedRelease, error) {
	var releases []githubRelease
	if err := getReleaseJSON(ctx, releaseAPIURL, &releases); err != nil {
		return PublishedRelease{}, err
	}

	var best PublishedRelease
	found := false
	for _, item := range releases {
		version := strings.TrimSpace(item.TagName)
		if item.Draft || !IsReleaseVersion(version) {
			continue
		}
		assets := make(map[string]string, len(item.Assets))
		for _, asset := range item.Assets {
			if asset.Name != "" && strings.HasPrefix(asset.BrowserDownloadURL, "https://") {
				assets[asset.Name] = asset.BrowserDownloadURL
			}
		}
		publishedAt, _ := time.Parse(time.RFC3339, item.PublishedAt)
		candidate := PublishedRelease{Version: version, Prerelease: item.Prerelease, PublishedAt: publishedAt, assets: assets}
		if !found {
			best, found = candidate, true
			continue
		}
		cmp, err := CompareReleaseVersions(candidate.Version, best.Version)
		if err == nil && cmp > 0 {
			best = candidate
		}
	}
	if !found {
		return PublishedRelease{}, errors.New("no published Minimal Router release is available")
	}
	return best, nil
}

func downloadReleaseFile(ctx context.Context, url, destination string, maximum int64) error {
	if !strings.HasPrefix(url, "https://") {
		return errors.New("release asset URL must use HTTPS")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "minimalrouter-update")
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
		relative := strings.TrimPrefix(clean, prefix)
		relative = strings.TrimPrefix(relative, "/")
		target := filepath.Join(destination, prefix, filepath.FromSlash(relative))
		if relative == "" {
			target = filepath.Join(destination, prefix)
			seenRoot = true
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
			if err := os.Chmod(target, os.FileMode(header.Mode)&os.ModePerm); err != nil {
				return "", err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maxReleaseArchive || expanded > maxExpandedRelease-header.Size {
				return "", errors.New("expanded release exceeds size limit")
			}
			expanded += header.Size
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
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
			if err := os.Chmod(target, os.FileMode(header.Mode)&os.ModePerm); err != nil {
				return "", err
			}
			seenRoot = true
		default:
			return "", fmt.Errorf("release archive contains unsupported entry type at %q", header.Name)
		}
	}
	payloadRoot := filepath.Join(destination, prefix)
	if !seenRoot {
		return "", errors.New("release archive is empty")
	}
	info, err := os.Stat(payloadRoot)
	if err != nil || !info.IsDir() {
		return "", errors.New("release archive payload root is missing")
	}
	return payloadRoot, nil
}

// PreparePublishedRelease downloads the architecture-specific signed manifest
// and archive into a private routerd-owned inbox and safely extracts regular
// files only. The root updater performs the authoritative signature, hash,
// mode, architecture, forward-version and A/B-slot checks afterwards.
func PreparePublishedRelease(ctx context.Context, release PublishedRelease, arch, destination string) (string, string, error) {
	if arch != "amd64" && arch != "arm64" {
		return "", "", fmt.Errorf("unsupported update architecture %q", arch)
	}
	if !IsReleaseVersion(release.Version) {
		return "", "", errors.New("invalid published release version")
	}
	archiveName := "minimalrouter-linux-" + arch + ".tar.gz"
	manifestName := "minimalrouter-linux-" + arch + ".manifest.json"
	archiveURL := release.assets[archiveName]
	manifestURL := release.assets[manifestName]
	if archiveURL == "" || manifestURL == "" {
		return "", "", fmt.Errorf("release %s does not contain %s and %s", release.Version, archiveName, manifestName)
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
	archivePath := filepath.Join(destination, "release.tar.gz")
	if err := downloadReleaseFile(ctx, manifestURL, manifestPath, maxReleaseManifest); err != nil {
		return "", "", fmt.Errorf("download signed manifest: %w", err)
	}
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return "", "", fmt.Errorf("read signed manifest: %w", err)
	}
	if manifest.Version != release.Version {
		return "", "", fmt.Errorf("release tag %s does not match manifest version %s", release.Version, manifest.Version)
	}
	if err := downloadReleaseFile(ctx, archiveURL, archivePath, maxReleaseArchive); err != nil {
		return "", "", fmt.Errorf("download release archive: %w", err)
	}
	extractRoot := filepath.Join(destination, "release")
	if err := os.MkdirAll(extractRoot, 0o700); err != nil {
		return "", "", err
	}
	payloadRoot, err := extractReleaseArchive(archivePath, extractRoot, arch)
	if err != nil {
		return "", "", err
	}
	if err := os.Remove(archivePath); err != nil {
		return "", "", err
	}
	return payloadRoot, manifestPath, nil
}
