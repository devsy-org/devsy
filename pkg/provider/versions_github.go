package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/download"
	devsyhttp "github.com/devsy-org/devsy/pkg/http"
)

type githubRelease struct {
	TagName     string    `json:"tag_name"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
}

// ListGitHubReleases calls the GitHub Releases API (rooted at baseURL - pass "https://api.github.com" in production)
// and returns versions newest first. Drafts are always filtered out; prereleases only when includePrerelease is true.
func ListGitHubReleases(
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
		return nil, clierr.ErrRateLimited
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
	base, _ := SplitSourceAndTag(canonical)
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

// githubAPIBaseURL is the GitHub REST API root; tests point it at a local server.
var githubAPIBaseURL = "https://api.github.com"

const latestReleaseTimeout = 10 * time.Second

// FetchLatestGitHubReleaseTag returns the tag GitHub designates as the latest
// stable release of ownerRepo ("owner/repo"). Unlike the newest-published
// ordering of ListGitHubReleases it never selects a prerelease or draft. A
// credential resolver, when given, is used only to retry an anonymous 401/403/404.
func FetchLatestGitHubReleaseTag(
	ctx context.Context,
	ownerRepo string,
	resolver download.CredentialResolver,
) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, latestReleaseTimeout)
	defer cancel()

	apiURL := fmt.Sprintf("%s/repos/%s/releases/latest", githubAPIBaseURL, ownerRepo)
	tag, status, err := requestLatestReleaseTag(ctx, apiURL, "")
	if err == nil || resolver == nil || !isCredentialStatus(status) {
		return tag, err
	}

	_, password, resolveErr := resolver.Resolve(
		ctx,
		"https",
		"github.com",
		"/"+strings.TrimPrefix(ownerRepo, "/"),
	)
	if resolveErr != nil || password == "" {
		return "", err
	}

	tag, _, err = requestLatestReleaseTag(ctx, apiURL, password)
	return tag, err
}

func isCredentialStatus(status int) bool {
	return status == http.StatusUnauthorized ||
		status == http.StatusForbidden ||
		status == http.StatusNotFound
}

func requestLatestReleaseTag(ctx context.Context, apiURL, token string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := devsyhttp.GetHTTPClient().Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("github latest release request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	tag, err := latestReleaseTagFromResponse(resp)
	return tag, resp.StatusCode, err
}

func latestReleaseTagFromResponse(resp *http.Response) (string, error) {
	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return "", clierr.ErrRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github latest release: status %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return "", fmt.Errorf("decode github latest release: %w", err)
	}
	if release.TagName == "" {
		return "", errors.New("github latest release has no tag")
	}
	if release.Draft || release.Prerelease {
		return "", errors.New("github latest release is not a stable published release")
	}

	return release.TagName, nil
}
