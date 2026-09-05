package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/firmware"
)

const (
	defaultCatalogURL = "https://api.github.com/repos/vladimirperovic/minimalrouter/releases?per_page=30"
	userAgent         = "minimalrouter-update"
	// maxCatalogPages bounds one discovery round. Thirty releases per page is
	// already far more history than a candidate search needs, and following
	// pagination forever would turn a hostile or misconfigured endpoint into an
	// unbounded fetch on an appliance.
	maxCatalogPages = 3
	maxCatalogBody  = 1 << 20
	catalogTimeout  = 20 * time.Second
)

// Channel decides which published releases an appliance is willing to see.
type Channel string

const (
	ChannelStable Channel = "stable"
	ChannelBeta   Channel = "beta"
)

// ParseChannel maps stored or requested text onto a supported channel.
func ParseChannel(value string) (Channel, bool) {
	switch Channel(strings.ToLower(strings.TrimSpace(value))) {
	case ChannelStable:
		return ChannelStable, true
	case ChannelBeta:
		return ChannelBeta, true
	default:
		return "", false
	}
}

// Includes reports whether a release belongs to this channel. Beta is a
// superset: an appliance following pre-releases still takes stable ones.
func (c Channel) Includes(published Release) bool {
	if c == ChannelBeta {
		return true
	}
	return !published.Prerelease
}

// Release is the metadata needed to decide whether a published release is a
// usable candidate, plus the canonical asset URLs this package constructs
// itself. A URL supplied by the remote metadata is never followed.
type Release struct {
	Version     string    `json:"version"`
	Tag         string    `json:"tag"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	URL         string    `json:"url"`
	Notes       string    `json:"notes,omitempty"`

	assets map[string]string
}

func payloadAssetNames(arch string) (archive string, manifest string) {
	return "minimalrouter-linux-" + arch + ".tar.gz", "minimalrouter-linux-" + arch + ".manifest.json"
}

// HasPayload reports whether this release actually carries an installable
// payload for the given architecture. The newest tag is not a candidate if its
// archive or signed manifest is missing: offering it would produce a download
// failure at the moment the operator pressed Update.
func (r Release) HasPayload(arch string) bool {
	archive, manifest := payloadAssetNames(arch)
	return r.assets[archive] != "" && r.assets[manifest] != ""
}

// ID is a stable identifier for one candidate. The dashboard confirms an ID,
// and the server re-derives it before installing, so a release published
// between the two cannot silently take the confirmed one's place.
func (r Release) ID() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		r.Tag, r.Version, r.PublishedAt.UTC().Format(time.RFC3339),
	}, "\x00")))
	return hex.EncodeToString(sum[:])[:32]
}

type githubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
	Assets      []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

// Catalog reads the published release list. Origin and asset paths are fixed;
// only the endpoint is injectable, for tests.
type Catalog struct {
	APIURL string
	Client *http.Client
}

// FetchResult carries one discovery round, including the cache validator and
// any server-requested pause.
type FetchResult struct {
	Releases    []Release
	ETag        string
	NotModified bool
	RetryAfter  time.Duration
}

// ErrRateLimited reports that the release service asked the appliance to stop
// for a while. It is a distinct condition from "no release found": one means
// try later, the other means the check ran and produced an answer.
var ErrRateLimited = errors.New("release service rate limit reached")

func (c Catalog) endpoint() string {
	if strings.TrimSpace(c.APIURL) == "" {
		return defaultCatalogURL
	}
	return c.APIURL
}

func (c Catalog) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: catalogTimeout}
}

// nextPageURL reads RFC 8288 pagination. Only a link the service itself
// returned for rel="next" is followed, and only within maxCatalogPages.
func nextPageURL(linkHeader string) string {
	for _, part := range strings.Split(linkHeader, ",") {
		segments := strings.Split(strings.TrimSpace(part), ";")
		if len(segments) < 2 {
			continue
		}
		target := strings.TrimSpace(segments[0])
		if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
			continue
		}
		for _, attribute := range segments[1:] {
			if strings.EqualFold(strings.TrimSpace(attribute), `rel="next"`) {
				return strings.TrimSuffix(strings.TrimPrefix(target, "<"), ">")
			}
		}
	}
	return ""
}

// retryAfter honours both the Retry-After header and the rate-limit reset the
// REST API documents, so a throttled appliance waits the interval it was told
// to rather than a guess.
func retryAfter(header http.Header, now time.Time) time.Duration {
	if raw := strings.TrimSpace(header.Get("Retry-After")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second
		}
		if when, err := http.ParseTime(raw); err == nil {
			if wait := when.Sub(now); wait > 0 {
				return wait
			}
			return 0
		}
	}
	if strings.TrimSpace(header.Get("X-RateLimit-Remaining")) == "0" {
		if raw := strings.TrimSpace(header.Get("X-RateLimit-Reset")); raw != "" {
			if epoch, err := strconv.ParseInt(raw, 10, 64); err == nil {
				if wait := time.Unix(epoch, 0).Sub(now); wait > 0 {
					return wait
				}
			}
		}
	}
	return 0
}

func (c Catalog) fetchPage(ctx context.Context, endpoint, etag string) ([]githubRelease, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", userAgent)
	if etag != "" {
		// A conditional request lets the service answer 304 without resending
		// the list. It reduces transfer; it is not a guarantee about quota.
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotModified:
		return nil, resp, nil
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return nil, resp, fmt.Errorf("%w (HTTP %d)", ErrRateLimited, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, resp, fmt.Errorf("release service returned HTTP %d", resp.StatusCode)
	}

	var page []githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxCatalogBody)).Decode(&page); err != nil {
		return nil, resp, fmt.Errorf("decode release metadata: %w", err)
	}
	return page, resp, nil
}

// Fetch reads the published releases, following pagination up to a bounded
// number of pages. A matching etag short-circuits the whole round.
func (c Catalog) Fetch(ctx context.Context, etag string) (FetchResult, error) {
	endpoint := c.endpoint()
	result := FetchResult{}
	now := time.Now()

	for page := 0; page < maxCatalogPages && endpoint != ""; page++ {
		conditional := ""
		if page == 0 {
			conditional = etag
		}
		items, resp, err := c.fetchPage(ctx, endpoint, conditional)
		if resp != nil {
			result.RetryAfter = retryAfter(resp.Header, now)
		}
		if err != nil {
			return result, err
		}
		if resp.StatusCode == http.StatusNotModified {
			result.NotModified = true
			result.ETag = etag
			return result, nil
		}
		if page == 0 {
			result.ETag = strings.TrimSpace(resp.Header.Get("ETag"))
		}
		for _, item := range items {
			if converted, ok := convertRelease(item); ok {
				result.Releases = append(result.Releases, converted)
			}
		}
		endpoint = nextPageURL(resp.Header.Get("Link"))
	}
	return result, nil
}

func convertRelease(item githubRelease) (Release, bool) {
	tag := strings.TrimSpace(item.TagName)
	if item.Draft || !firmware.IsReleaseVersion(tag) {
		return Release{}, false
	}
	assets := make(map[string]string, len(item.Assets))
	for _, asset := range item.Assets {
		if asset.Name == "" {
			continue
		}
		// The metadata is used only to learn that an asset exists. The URL is
		// always the canonical one this package builds.
		assets[asset.Name] = canonicalReleaseAssetURL(tag, asset.Name)
	}
	publishedAt, _ := time.Parse(time.RFC3339, item.PublishedAt)
	return Release{
		Version:     strings.TrimPrefix(tag, "v"),
		Tag:         tag,
		Prerelease:  item.Prerelease,
		PublishedAt: publishedAt,
		URL:         canonicalReleaseHTMLURL(tag),
		Notes:       summarizeNotes(item.Body),
		assets:      assets,
	}, true
}

func canonicalReleaseHTMLURL(tag string) string {
	return "https://github.com/vladimirperovic/minimalrouter/releases/tag/" + tag
}

// summarizeNotes keeps release notes to a bounded amount of plain text. The
// dashboard renders it as text: notes are author-controlled content and must
// never reach the browser as markup.
func summarizeNotes(body string) string {
	const maxNotes = 2000
	cleaned := strings.ReplaceAll(strings.TrimSpace(body), "\r\n", "\n")
	if len(cleaned) > maxNotes {
		cleaned = strings.TrimSpace(cleaned[:maxNotes]) + "\n…"
	}
	return cleaned
}

// SelectNewest returns the newest release in the channel, and the newest one
// that is actually installable for this architecture. They differ when the
// newest tag has no payload yet, and the dashboard must be able to say so
// instead of offering an update that cannot be downloaded.
func SelectNewest(releases []Release, channel Channel, arch string) (newest *Release, candidate *Release) {
	for index := range releases {
		item := releases[index]
		if !channel.Includes(item) {
			continue
		}
		if newest == nil || isNewer(item.Version, newest.Version) {
			copied := item
			newest = &copied
		}
		if !item.HasPayload(arch) {
			continue
		}
		if candidate == nil || isNewer(item.Version, candidate.Version) {
			copied := item
			candidate = &copied
		}
	}
	return newest, candidate
}

func isNewer(candidate, current string) bool {
	if !firmware.IsReleaseVersion(candidate) || !firmware.IsReleaseVersion(current) {
		return false
	}
	cmp, err := firmware.CompareReleaseVersions(candidate, current)
	return err == nil && cmp > 0
}

// IsNewerVersion reports whether candidate supersedes current.
func IsNewerVersion(candidate, current string) bool { return isNewer(candidate, current) }
