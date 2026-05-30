package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	devsyhttp "github.com/devsy-org/devsy/pkg/http"
)

type githubRelease struct {
	TagName     string    `json:"tag_name"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
}

// listGitHubReleases calls the GitHub Releases API (rooted at baseURL — pass "https://api.github.com" in production)
// and returns versions newest first. Drafts are always filtered out; prereleases only when includePrerelease is true.
func listGitHubReleases(
	baseURL, org, repo string,
	includePrerelease bool,
) ([]ProviderVersion, error) {
	releases, err := fetchGitHubReleases(baseURL, org, repo)
	if err != nil {
		return nil, err
	}
	return filterAndSortReleases(releases, includePrerelease), nil
}

func fetchGitHubReleases(baseURL, org, repo string) ([]githubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=50", baseURL, org, repo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := devsyhttp.GetHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("github releases request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return nil, ErrVersionListRateLimited
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("github releases %s: %d %s", url, resp.StatusCode, string(body))
	}

	var releases []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode github releases: %w", err)
	}
	return releases, nil
}

func filterAndSortReleases(releases []githubRelease, includePrerelease bool) []ProviderVersion {
	out := make([]ProviderVersion, 0, len(releases))
	for _, r := range releases {
		if r.Draft {
			continue
		}
		if r.Prerelease && !includePrerelease {
			continue
		}
		out = append(out, ProviderVersion{
			Tag:         r.TagName,
			PublishedAt: r.PublishedAt,
			Prerelease:  r.Prerelease,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].PublishedAt.After(out[j].PublishedAt)
	})
	return out
}

func parseGitHubSourcePath(canonical string) (org, repo string, ok bool) {
	base, _ := splitSourceAndTag(canonical)
	const prefix = "github.com/"
	if len(base) <= len(prefix) {
		return "", "", false
	}
	rest := base[len(prefix):]
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' {
			return rest[:i], rest[i+1:], true
		}
	}
	return "", "", false
}
