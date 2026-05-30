package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
)

const (
	GithubReleaseURL = "https://api.github.com/repos/itdoginfo/podkop/releases/latest"
	SelfReleaseURL   = "https://api.github.com/repos/VizzleTF/podkop_autoupdater/releases/latest"
)

type githubRelease struct {
	TagName string `json:"tag_name"`
}

// etagEntry caches the previous successful response for a URL so we can send a
// conditional request and skip re-decoding when GitHub answers 304. This keeps
// us under the 60 req/h unauthenticated rate limit when checks are frequent.
type etagEntry struct {
	etag    string
	version string // normalized X.Y.Z
	tag     string // raw upstream tag (e.g. "v0.7.0")
}

var (
	etagMu    sync.Mutex
	etagCache = map[string]etagEntry{}
)

// LatestRelease fetches the most recent podkop release and returns its
// normalized X.Y.Z version (without "v" prefix and without packaging suffix).
func LatestRelease(ctx context.Context, hc *http.Client) (string, error) {
	v, _, err := fetchLatestRelease(ctx, hc, GithubReleaseURL)
	return v, err
}

// LatestReleaseFull is like LatestRelease but also returns the raw upstream
// tag, used to pin the install.sh download to a specific release instead of
// the moving HEAD of the default branch.
func LatestReleaseFull(ctx context.Context, hc *http.Client) (version, tag string, err error) {
	return fetchLatestRelease(ctx, hc, GithubReleaseURL)
}

// LatestSelfRelease fetches the most recent podkop_autoupdater release.
// Returns empty string and nil error if no published releases yet (404).
func LatestSelfRelease(ctx context.Context, hc *http.Client) (string, error) {
	v, _, err := fetchLatestRelease(ctx, hc, SelfReleaseURL)
	if err != nil && errors.Is(err, errNoReleases) {
		return "", nil
	}
	return v, err
}

var errNoReleases = fmt.Errorf("no releases published")

func fetchLatestRelease(ctx context.Context, hc *http.Client, url string) (version, tag string, err error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	etagMu.Lock()
	cached, hasCache := etagCache[url]
	etagMu.Unlock()
	if hasCache && cached.etag != "" {
		req.Header.Set("If-None-Match", cached.etag)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("github: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		if hasCache {
			return cached.version, cached.tag, nil
		}
		// 304 without a cached body should not happen; fall through to error.
		return "", "", fmt.Errorf("github: 304 with no cached entry")
	case http.StatusNotFound:
		return "", "", errNoReleases
	case http.StatusOK:
		// handled below
	default:
		return "", "", fmt.Errorf("github: status %d", resp.StatusCode)
	}

	var r githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", "", fmt.Errorf("github: decode: %w", err)
	}
	v := Normalize(r.TagName)
	if !ValidSemver(v) {
		return "", "", fmt.Errorf("github: invalid version format %q", r.TagName)
	}
	if et := resp.Header.Get("ETag"); et != "" {
		etagMu.Lock()
		etagCache[url] = etagEntry{etag: et, version: v, tag: r.TagName}
		etagMu.Unlock()
	}
	return v, r.TagName, nil
}
