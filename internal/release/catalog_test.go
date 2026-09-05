package release

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func releaseJSON(tag string, prerelease bool, assets ...string) string {
	assetJSON := make([]string, 0, len(assets))
	for _, name := range assets {
		assetJSON = append(assetJSON, fmt.Sprintf(`{"name":%q}`, name))
	}
	return fmt.Sprintf(
		`{"tag_name":%q,"draft":false,"prerelease":%t,"published_at":"2026-08-15T12:00:00Z","body":"notes","assets":[%s]}`,
		tag, prerelease, strings.Join(assetJSON, ","))
}

func amd64Assets() []string {
	archive, manifest := payloadAssetNames("amd64")
	return []string{archive, manifest}
}

func TestFetchBuildsCanonicalAssetURLsAndIgnoresRemoteURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"abc"`)
		fmt.Fprintf(w, "[%s]", releaseJSON("v0.1.3", true, amd64Assets()...))
	}))
	defer server.Close()

	result, err := Catalog{APIURL: server.URL}.Fetch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Releases) != 1 {
		t.Fatalf("expected one release, got %d", len(result.Releases))
	}
	published := result.Releases[0]
	if published.Version != "0.1.3" || published.Tag != "v0.1.3" {
		t.Fatalf("unexpected version/tag: %+v", published)
	}
	archive, _ := payloadAssetNames("amd64")
	want := "https://github.com/vladimirperovic/minimalrouter/releases/download/v0.1.3/" + archive
	if got := published.assets[archive]; got != want {
		t.Fatalf("asset URL = %q, want %q", got, want)
	}
	if result.ETag != `"abc"` {
		t.Fatalf("etag = %q, want \"abc\"", result.ETag)
	}
}

func TestFetchHonoursNotModified(t *testing.T) {
	var conditional string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conditional = r.Header.Get("If-None-Match")
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	result, err := Catalog{APIURL: server.URL}.Fetch(context.Background(), `"cached"`)
	if err != nil {
		t.Fatal(err)
	}
	if conditional != `"cached"` {
		t.Fatalf("If-None-Match = %q, want the cached validator", conditional)
	}
	if !result.NotModified || result.ETag != `"cached"` {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestFetchFollowsBoundedPagination(t *testing.T) {
	var pages int
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		// Always advertise another page: the fetch must stop on its own.
		w.Header().Set("Link", fmt.Sprintf(`<%s/next?page=%d>; rel="next"`, server.URL, pages+1))
		if r.URL.Path == "/next" {
			fmt.Fprintf(w, "[%s]", releaseJSON(fmt.Sprintf("v0.1.%d", pages), false, amd64Assets()...))
			return
		}
		fmt.Fprintf(w, "[%s]", releaseJSON("v0.1.0", false, amd64Assets()...))
	}))
	defer server.Close()

	result, err := Catalog{APIURL: server.URL}.Fetch(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if pages != maxCatalogPages {
		t.Fatalf("fetched %d pages, want the bounded %d", pages, maxCatalogPages)
	}
	if len(result.Releases) != maxCatalogPages {
		t.Fatalf("expected one release per page, got %d", len(result.Releases))
	}
}

func TestFetchReportsRateLimitWithRequestedPause(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	result, err := Catalog{APIURL: server.URL}.Fetch(context.Background(), "")
	if err == nil {
		t.Fatal("rate limiting must be reported as an error, not as an empty answer")
	}
	if result.RetryAfter != 2*time.Minute {
		t.Fatalf("retry-after = %s, want 2m", result.RetryAfter)
	}
}

func TestRetryAfterUsesRateLimitResetWhenExhausted(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	header := http.Header{}
	header.Set("X-RateLimit-Remaining", "0")
	header.Set("X-RateLimit-Reset", fmt.Sprint(now.Add(90*time.Second).Unix()))
	if got := retryAfter(header, now); got != 90*time.Second {
		t.Fatalf("wait = %s, want 90s", got)
	}
}

func TestSelectNewestSeparatesNewestFromInstallable(t *testing.T) {
	archive, manifest := payloadAssetNames("amd64")
	releases := []Release{
		{Version: "0.1.6", Tag: "v0.1.6", assets: map[string]string{archive: "u", manifest: "u"}},
		{Version: "0.1.7", Tag: "v0.1.7", assets: map[string]string{archive: "u"}}, // manifest still uploading
		{Version: "0.2.0", Tag: "v0.2.0", Prerelease: true, assets: map[string]string{archive: "u", manifest: "u"}},
	}

	newest, candidate := SelectNewest(releases, ChannelStable, "amd64")
	if newest == nil || newest.Version != "0.1.7" {
		t.Fatalf("newest stable = %+v, want 0.1.7", newest)
	}
	if candidate == nil || candidate.Version != "0.1.6" {
		t.Fatalf("installable stable = %+v, want 0.1.6: a tag without a signed manifest is not a candidate", candidate)
	}

	newest, candidate = SelectNewest(releases, ChannelBeta, "amd64")
	if newest == nil || newest.Version != "0.2.0" || candidate == nil || candidate.Version != "0.2.0" {
		t.Fatalf("beta channel must see the prerelease: newest=%+v candidate=%+v", newest, candidate)
	}
}

func TestReleaseIDChangesWithTheRelease(t *testing.T) {
	first := Release{Tag: "v0.1.7", Version: "0.1.7", PublishedAt: time.Unix(1, 0)}
	same := Release{Tag: "v0.1.7", Version: "0.1.7", PublishedAt: time.Unix(1, 0)}
	other := Release{Tag: "v0.1.8", Version: "0.1.8", PublishedAt: time.Unix(1, 0)}
	if first.ID() != same.ID() {
		t.Fatal("the same release must produce a stable id")
	}
	if first.ID() == other.ID() {
		t.Fatal("a different release must produce a different id, or a confirmation could be replayed onto it")
	}
}

func TestNotesAreBoundedPlainText(t *testing.T) {
	published, ok := convertRelease(githubRelease{
		TagName:     "v0.1.7",
		PublishedAt: "2026-09-05T00:00:00Z",
		Body:        strings.Repeat("a", 5000),
	})
	if !ok {
		t.Fatal("valid release was rejected")
	}
	if len(published.Notes) > 2100 {
		t.Fatalf("notes length = %d, want bounded", len(published.Notes))
	}
}

func TestDraftsAndInvalidTagsAreNotReleases(t *testing.T) {
	if _, ok := convertRelease(githubRelease{TagName: "v0.1.7", Draft: true}); ok {
		t.Fatal("a draft must never be offered")
	}
	if _, ok := convertRelease(githubRelease{TagName: "nightly"}); ok {
		t.Fatal("a non-semver tag must never be offered")
	}
}
