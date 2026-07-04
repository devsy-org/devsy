package download

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	devsyhttp "github.com/devsy-org/devsy/pkg/http"
	"github.com/devsy-org/devsy/pkg/log"
)

// CredentialResolver resolves a username/password (token) for a host, used to
// authenticate downloads of private assets.
type CredentialResolver interface {
	Resolve(ctx context.Context, protocol, host, path string) (username, password string, err error)
}

type options struct {
	resolver CredentialResolver
}

// Option configures a download.
type Option func(*options)

// WithCredentialResolver enables authenticated retries for private assets.
func WithCredentialResolver(resolver CredentialResolver) Option {
	return func(o *options) { o.resolver = resolver }
}

// HTTPStatusError wraps HTTP status code errors for better error handling.
type HTTPStatusError struct {
	StatusCode int
	URL        string
	Body       string
}

func (e *HTTPStatusError) Error() string {
	safeURL := sanitizeURL(e.URL)
	if e.Body != "" {
		return fmt.Sprintf(
			"received status code %d when trying to download %s: %s",
			e.StatusCode,
			safeURL,
			e.Body,
		)
	}
	return fmt.Sprintf(
		"received status code %d when trying to download %s",
		e.StatusCode,
		safeURL,
	)
}

func sanitizeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err == nil {
		if parsed.User != nil {
			parsed.User = nil
			return parsed.String()
		}
		return raw
	}
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return raw
	}
	_, afterAt, found := strings.Cut(rest, "@")
	if !found {
		return raw
	}
	return scheme + "://" + afterAt
}

func Head(ctx context.Context, rawURL string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
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

func File(ctx context.Context, rawURL string, opts ...Option) (io.ReadCloser, error) {
	cfg := &options{}
	for _, opt := range opts {
		opt(cfg)
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}

	if parsed.Host == "github.com" && cfg.resolver != nil {
		body, err := fetchGithubPrivateRelease(ctx, parsed, cfg.resolver)
		if err != nil {
			return nil, err
		}
		if body != nil {
			return body, nil
		}
	}

	return getURL(ctx, rawURL)
}

// getURL performs an anonymous GET and returns the response body, mapping
// non-2xx/3xx responses to an HTTPStatusError.
func getURL(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := devsyhttp.GetHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, URL: rawURL, Body: string(body)}
	}

	return resp.Body, nil
}

// fetchGithubPrivateRelease attempts to download a GitHub release asset using
// credentials from the resolver when the URL returns a 404 (indicating a
// private repo). Returns (nil, nil) if the URL is not a private GitHub release
// or credentials are unavailable, allowing the caller to fall through to a
// normal download.
func fetchGithubPrivateRelease(
	ctx context.Context,
	parsed *url.URL,
	resolver CredentialResolver,
) (io.ReadCloser, error) {
	code, err := Head(ctx, parsed.String())
	if err != nil {
		return nil, err
	}
	if code != 404 {
		return nil, nil
	}

	ref, ok := parseGithubURL(parsed.Path)
	if !ok {
		return nil, nil
	}

	log.Debugf("try to find credentials for github")
	_, password, err := resolver.Resolve(ctx, parsed.Scheme, parsed.Host, parsed.Path)
	if err != nil || password == "" {
		return nil, nil
	}

	log.Debugf("make request with credentials")
	return downloadGithubRelease(ctx, ref, password)
}

type githubReleaseRef struct {
	org, repo, release, file string
}

type GithubRelease struct {
	Assets []GithubReleaseAsset `json:"assets,omitempty"`
}

type GithubReleaseAsset struct {
	ID   int    `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

func downloadGithubRelease(
	ctx context.Context,
	ref githubReleaseRef,
	token string,
) (io.ReadCloser, error) {
	assetID, err := fetchGithubReleaseAssetID(ctx, ref, token)
	if err != nil {
		return nil, err
	}

	return downloadGithubAsset(ctx, ref, assetID, token)
}

func (ref githubReleaseRef) releaseURL() string {
	var path string
	if ref.release == "" {
		path = fmt.Sprintf(
			"/repos/%s/%s/releases/latest",
			url.PathEscape(ref.org),
			url.PathEscape(ref.repo),
		)
	} else {
		path = fmt.Sprintf(
			"/repos/%s/%s/releases/tags/%s",
			url.PathEscape(ref.org),
			url.PathEscape(ref.repo),
			url.PathEscape(ref.release),
		)
	}

	return (&url.URL{
		Scheme: "https",
		Host:   "api.github.com",
		Path:   path,
	}).String()
}

func githubAPIGetJSON(ctx context.Context, apiURL, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := devsyhttp.GetHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			URL:        apiURL,
			Body:       string(body),
		}
	}

	return io.ReadAll(resp.Body)
}

func fetchGithubReleaseAssetID(
	ctx context.Context,
	ref githubReleaseRef,
	token string,
) (int, error) {
	releaseURL := ref.releaseURL()

	raw, err := githubAPIGetJSON(ctx, releaseURL, token)
	if err != nil {
		return 0, err
	}

	releaseObj := &GithubRelease{}
	if err = json.Unmarshal(raw, releaseObj); err != nil {
		return 0, err
	}

	for _, asset := range releaseObj.Assets {
		if asset.Name == ref.file {
			return asset.ID, nil
		}
	}

	return 0, fmt.Errorf("couldn't find asset %s in github release (%s)", ref.file, releaseURL)
}

func downloadGithubAsset(
	ctx context.Context,
	ref githubReleaseRef,
	assetID int,
	token string,
) (io.ReadCloser, error) {
	assetPath := fmt.Sprintf(
		"/repos/%s/%s/releases/assets/%d",
		url.PathEscape(ref.org),
		url.PathEscape(ref.repo),
		assetID,
	)
	assetURL := (&url.URL{
		Scheme: "https",
		Host:   "api.github.com",
		Path:   assetPath,
	}).String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
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

func parseGithubURL(path string) (githubReleaseRef, bool) {
	splitted := strings.Split(strings.TrimPrefix(path, "/"), "/")

	switch {
	case len(splitted) != 6:
		return githubReleaseRef{}, false
	case splitted[2] != "releases":
		return githubReleaseRef{}, false
	case (splitted[3] != "latest" || splitted[4] != "download") && splitted[3] != "download":
		return githubReleaseRef{}, false
	}

	ref := githubReleaseRef{
		org:  splitted[0],
		repo: splitted[1],
		file: splitted[5],
	}

	if splitted[3] != "latest" {
		ref.release = splitted[4]
	}

	return ref, true
}
