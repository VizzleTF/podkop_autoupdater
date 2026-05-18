package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

const (
	GithubReleaseURL = "https://api.github.com/repos/itdoginfo/podkop/releases/latest"
	SelfReleaseURL   = "https://api.github.com/repos/VizzleTF/podkop_autoupdater/releases/latest"
)

type githubRelease struct {
	TagName string `json:"tag_name"`
}

// LatestRelease fetches the most recent podkop release and returns its
// normalized X.Y.Z version (without "v" prefix and without packaging suffix).
func LatestRelease(ctx context.Context, hc *http.Client) (string, error) {
	return fetchLatestRelease(ctx, hc, GithubReleaseURL)
}

// LatestSelfRelease fetches the most recent podkop_autoupdater release.
// Returns empty string and nil error if no published releases yet (404).
func LatestSelfRelease(ctx context.Context, hc *http.Client) (string, error) {
	v, err := fetchLatestRelease(ctx, hc, SelfReleaseURL)
	if err != nil && errors.Is(err, errNoReleases) {
		return "", nil
	}
	return v, err
}

var errNoReleases = fmt.Errorf("no releases published")

func fetchLatestRelease(ctx context.Context, hc *http.Client, url string) (string, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", errNoReleases
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: status %d", resp.StatusCode)
	}
	var r githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("github: decode: %w", err)
	}
	v := Normalize(r.TagName)
	if !ValidSemver(v) {
		return "", fmt.Errorf("github: invalid version format %q", r.TagName)
	}
	return v, nil
}
