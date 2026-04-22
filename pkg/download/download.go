package download

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/devsy-org/devsy/pkg/gitcredentials"
	devsyhttp "github.com/devsy-org/devsy/pkg/http"
	"github.com/devsy-org/devsy/pkg/log"
)

// HTTPStatusError wraps HTTP status code errors for better error handling.
type HTTPStatusError struct {
	StatusCode int
	URL        string
	Body       string
}

func (e *HTTPStatusError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf(
			"received status code %d when trying to download %s: %s",
			e.StatusCode,
			e.URL,
			e.Body,
		)
	}
	return fmt.Sprintf(
		"received status code %d when trying to download %s",
		e.StatusCode,
		e.URL,
	)
}

func Head(rawURL string) (int, error) {
	req, err := http.NewRequest(http.MethodHead, rawURL, nil)
	if err != nil {
		return 0, err
	}

	resp, err := devsyhttp.GetHTTPClient().Do(req)
	if err != nil {
		return 0, fmt.Errorf("download file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode, nil
}

func File(rawURL string) (io.ReadCloser, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	if parsed.Host == "github.com" {
		body, err := tryGithubPrivateDownload(parsed)
		if err != nil {
			return nil, err
		}
		if body != nil {
			return body, nil
		}
	}

	resp, err := devsyhttp.GetHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	} else if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, URL: rawURL, Body: string(body)}
	}

	return resp.Body, nil
}

// tryGithubPrivateDownload attempts to download a GitHub release asset using
// git credentials when the URL returns a 404 (indicating a private repo).
// Returns (nil, nil) if the URL is not a private GitHub release or credentials
// are unavailable, allowing the caller to fall through to a normal download.
func tryGithubPrivateDownload(parsed *url.URL) (io.ReadCloser, error) {
	code, err := Head(parsed.String())
	if err != nil {
		return nil, err
	}
	if code != 404 {
		return nil, nil
	}

	org, repo, release, file := parseGithubURL(parsed.Path)
	if org == "" {
		return nil, nil
	}

	log.Debugf("Try to find credentials for github")
	credentials, err := gitcredentials.GetCredentials(&gitcredentials.GitCredentials{
		Protocol: parsed.Scheme,
		Host:     parsed.Host,
		Path:     parsed.Path,
	})
	if err != nil || credentials == nil || credentials.Password == "" {
		return nil, nil
	}

	log.Debugf("Make request with credentials")
	return downloadGithubRelease(org, repo, release, file, credentials.Password)
}

type GithubRelease struct {
	Assets []GithubReleaseAsset `json:"assets,omitempty"`
}

type GithubReleaseAsset struct {
	ID   int    `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

func downloadGithubRelease(org, repo, release, file, token string) (io.ReadCloser, error) {
	assetID, err := fetchGithubReleaseAssetID(org, repo, release, file, token)
	if err != nil {
		return nil, err
	}

	return downloadGithubAsset(org, repo, assetID, token)
}

func fetchGithubReleaseAssetID(org, repo, release, file, token string) (int, error) {
	var releasePath string
	if release == "" {
		releasePath = fmt.Sprintf(
			"/repos/%s/%s/releases/latest",
			url.PathEscape(org),
			url.PathEscape(repo),
		)
	} else {
		releasePath = fmt.Sprintf(
			"/repos/%s/%s/releases/tags/%s",
			url.PathEscape(org),
			url.PathEscape(repo),
			url.PathEscape(release),
		)
	}

	releaseURL := (&url.URL{
		Scheme: "https",
		Host:   "api.github.com",
		Path:   releasePath,
	}).String()

	req, err := http.NewRequest(http.MethodGet, releaseURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := devsyhttp.GetHTTPClient().Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return 0, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			URL:        releaseURL,
			Body:       string(body),
		}
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	releaseObj := &GithubRelease{}
	if err = json.Unmarshal(raw, releaseObj); err != nil {
		return 0, err
	}

	for _, asset := range releaseObj.Assets {
		if asset.Name == file {
			return asset.ID, nil
		}
	}
	return 0, fmt.Errorf("couldn't find asset %s in github release (%s)", file, releaseURL)
}

func downloadGithubAsset(org, repo string, assetID int, token string) (io.ReadCloser, error) {
	assetPath := fmt.Sprintf(
		"/repos/%s/%s/releases/assets/%d",
		url.PathEscape(org),
		url.PathEscape(repo),
		assetID,
	)
	assetURL := (&url.URL{
		Scheme: "https",
		Host:   "api.github.com",
		Path:   assetPath,
	}).String()

	req, err := http.NewRequest(http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := devsyhttp.GetHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		return nil, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			URL:        assetURL,
			Body:       string(body),
		}
	}

	return resp.Body, nil
}

func parseGithubURL(path string) (org, repo, release, file string) {
	splitted := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(splitted) != 6 {
		return "", "", "", ""
	} else if splitted[2] != "releases" {
		return "", "", "", ""
	} else if (splitted[3] != "latest" || splitted[4] != "download") && splitted[3] != "download" {
		return "", "", "", ""
	}

	if splitted[3] == "latest" {
		return splitted[0], splitted[1], "", splitted[5]
	}

	return splitted[0], splitted[1], splitted[4], splitted[5]
}
