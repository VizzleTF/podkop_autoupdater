package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const releasesListURL = "https://api.github.com/repos/itdoginfo/podkop/releases?per_page=30"

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type releaseFull struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

// PreviousRelease returns the highest published podkop release strictly below
// `installed` (normalized), with its raw tag — i.e. the natural one-step
// rollback target. Returns ("","",nil) when no older release exists.
func PreviousRelease(ctx context.Context, hc *http.Client, installed string) (version, tag string, err error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesListURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("github releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("github releases: status %d", resp.StatusCode)
	}
	var rels []releaseFull
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return "", "", fmt.Errorf("github releases: decode: %w", err)
	}
	var bestVer, bestTag string
	for _, r := range rels {
		v := Normalize(r.TagName)
		if !ValidSemver(v) {
			continue
		}
		if !IsNewer(v, installed) { // v < installed
			if bestVer == "" || IsNewer(bestVer, v) { // v > bestVer
				bestVer, bestTag = v, r.TagName
			}
		}
	}
	return bestVer, bestTag, nil
}

// ReleaseMap returns a map of normalized version → raw tag for the recent
// published releases, used to check which locally-backed-up versions are still
// downloadable and to resolve a version's download tag.
func ReleaseMap(ctx context.Context, hc *http.Client) (map[string]string, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesListURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases: status %d", resp.StatusCode)
	}
	var rels []releaseFull
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return nil, fmt.Errorf("github releases: decode: %w", err)
	}
	out := make(map[string]string, len(rels))
	for _, r := range rels {
		v := Normalize(r.TagName)
		if ValidSemver(v) {
			out[v] = r.TagName
		}
	}
	return out, nil
}

// AssetURLsForTag fetches the release identified by tag and returns the
// download URLs of its podkop package assets matching the requested format
// (.apk when isAPK, else .ipk). Mirrors podkop's own install.sh asset
// selection.
func AssetURLsForTag(ctx context.Context, hc *http.Client, tag string, isAPK bool) ([]string, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	url := "https://api.github.com/repos/itdoginfo/podkop/releases/tags/" + tag
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github tag %s: %w", tag, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github tag %s: status %d", tag, resp.StatusCode)
	}
	var r releaseFull
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("github tag %s: decode: %w", tag, err)
	}
	suffix := ".ipk"
	if isAPK {
		suffix = ".apk"
	}
	var urls []string
	for _, a := range r.Assets {
		if strings.HasSuffix(a.Name, suffix) && strings.Contains(a.Name, "podkop") {
			urls = append(urls, a.URL)
		}
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("github tag %s: no %s podkop assets", tag, suffix)
	}
	return urls, nil
}
