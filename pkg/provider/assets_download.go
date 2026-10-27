package provider

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/download"
	"github.com/devsy-org/devsy/pkg/gitcredentials"
	devsyhttp "github.com/devsy-org/devsy/pkg/http"
)

const (
	iconDownloadTimeout = 5 * time.Second
	maxIconRedirects    = 5
)

var (
	errTooManyIconRedirects = fmt.Errorf("stopped after %d redirects", maxIconRedirects)
	urlInTextPattern        = regexp.MustCompile(`https?://[^\s"'<>)]+`)
)

// iconVariant is one fetched and validated icon. data is nil when the variant
// is absent or unusable; failure then carries a redacted one-line reason for a
// declared reference.
type iconVariant struct {
	data    []byte
	mime    string
	failure string
}

type iconHTTPResponse struct {
	data        []byte
	contentType string
	status      int
}

// iconFetcher downloads optional icons with bounded time, size and redirects.
// It never retries: a logo must not delay provider setup.
type iconFetcher struct {
	client      *http.Client
	credentials download.CredentialResolver
	timeout     time.Duration
}

func newIconFetcher() *iconFetcher {
	return &iconFetcher{
		client:      newIconHTTPClient(),
		credentials: githubIconCredentials{inner: gitcredentials.Resolver{}},
		timeout:     iconDownloadTimeout,
	}
}

func (f *iconFetcher) close() {
	f.client.CloseIdleConnections()
}

func newIconHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if insecure, _ := strconv.ParseBool(os.Getenv(config.EnvInsecureTLS)); insecure {
		// #nosec G402 -- enabled with DEVSY_INSECURE_TLS env, like the shared client
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	return &http.Client{
		Transport:     devsyhttp.WithUserAgent(transport, devsyhttp.UserAgent()),
		CheckRedirect: checkIconRedirect,
	}
}

// checkIconRedirect caps redirects and keeps Authorization on the origin it was
// resolved for.
func checkIconRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxIconRedirects {
		return errTooManyIconRedirects
	}
	if !sameOrigin(req.URL, via[0].URL) {
		req.Header.Del("Authorization")
	}
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func (f *iconFetcher) fetchVariant(
	ctx context.Context,
	source ProviderSource,
	reference string,
) iconVariant {
	if strings.TrimSpace(reference) == "" {
		return iconVariant{}
	}

	location, err := ResolveProviderIconReference(source, reference)
	if err != nil {
		return failedIcon(err.Error())
	}

	var (
		data        []byte
		contentType string
		origin      string
	)
	if location.File != "" {
		origin = "local file " + filepath.Base(location.File)
		relative, pathErr := localIconRelativePath(reference)
		if pathErr != nil {
			return failedIcon(origin + ": " + pathErr.Error())
		}
		data, err = readLocalIcon(filepath.Dir(source.File), filepath.FromSlash(relative))
	} else {
		origin = redactURL(location.URL)
		data, contentType, err = f.download(ctx, location.URL)
	}
	if err != nil {
		return failedIcon(origin + ": " + err.Error())
	}

	mimeType, err := validateIconImage(data, contentType)
	if err != nil {
		return failedIcon(origin + ": " + err.Error())
	}

	return iconVariant{data: data, mime: mimeType}
}

func failedIcon(reason string) iconVariant {
	return iconVariant{failure: redactURLsInText(reason)}
}

func (f *iconFetcher) download(ctx context.Context, rawURL string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	response, err := f.get(ctx, rawURL, "")
	if err == nil || f.credentials == nil || !isCredentialStatus(response.status) {
		return response.data, response.contentType, err
	}

	token := f.resolveToken(ctx, rawURL)
	if token == "" {
		return nil, "", err
	}

	response, err = f.get(ctx, rawURL, token)
	return response.data, response.contentType, err
}

// resolveToken asks for credentials only for origins that may carry them:
// https, or a loopback host.
func (f *iconFetcher) resolveToken(ctx context.Context, rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || !credentialTransportSafe(parsed) {
		return ""
	}

	_, password, err := f.credentials.Resolve(ctx, parsed.Scheme, parsed.Hostname(), parsed.Path)
	if err != nil {
		return ""
	}
	return password
}

func credentialTransportSafe(target *url.URL) bool {
	if strings.EqualFold(target.Scheme, iconSchemeHTTPS) {
		return true
	}

	host := target.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (f *iconFetcher) get(
	ctx context.Context,
	rawURL, token string,
) (iconHTTPResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return iconHTTPResponse{}, errors.New("invalid icon URL")
	}
	req.Header.Set("Accept", "image/*")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return iconHTTPResponse{}, errors.New(describeRequestError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	return readIconHTTPResponse(resp)
}

func readIconHTTPResponse(resp *http.Response) (iconHTTPResponse, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return iconHTTPResponse{status: resp.StatusCode}, fmt.Errorf(
			"HTTP status %d",
			resp.StatusCode,
		)
	}
	if resp.ContentLength > maxIconBytes {
		return iconHTTPResponse{status: resp.StatusCode}, fmt.Errorf(
			"image exceeds %d bytes",
			maxIconBytes,
		)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxIconBytes+1))
	if err != nil {
		return iconHTTPResponse{status: resp.StatusCode}, errors.New(
			describeRequestError(err),
		)
	}
	if len(data) > maxIconBytes {
		return iconHTTPResponse{status: resp.StatusCode}, fmt.Errorf(
			"image exceeds %d bytes",
			maxIconBytes,
		)
	}

	return iconHTTPResponse{
		data:        data,
		contentType: resp.Header.Get("Content-Type"),
		status:      resp.StatusCode,
	}, nil
}

// describeRequestError reduces a transport error to a reason that cannot echo
// the request URL back.
func describeRequestError(err error) string {
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		err = urlErr.Err
	}

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return err.Error()
	}
}

// readLocalIcon opens the original relative path beneath the logical manifest
// directory, so a symlink swap cannot redirect the read outside that root.
func readLocalIcon(directory, relative string) ([]byte, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, describeFileError(err)
	}
	defer func() { _ = root.Close() }()

	handle, err := openContainedLocalIcon(root, directory, relative)
	if err != nil {
		return nil, describeFileError(err)
	}
	defer func() { _ = handle.Close() }()

	info, err := handle.Stat()
	if err != nil {
		return nil, describeFileError(err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if info.Size() > maxIconBytes {
		return nil, fmt.Errorf("image exceeds %d bytes", maxIconBytes)
	}

	data, err := io.ReadAll(io.LimitReader(handle, maxIconBytes+1))
	if err != nil {
		return nil, describeFileError(err)
	}
	if len(data) > maxIconBytes {
		return nil, fmt.Errorf("image exceeds %d bytes", maxIconBytes)
	}
	return data, nil
}

// openContainedLocalIcon permits contained absolute symlinks but opens only a
// relative target through the already-open root. A later escape swap is rejected.
func openContainedLocalIcon(root *os.Root, directory, relative string) (*os.File, error) {
	canonicalRoot, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	target, err := resolveLocalIconTarget(canonicalRoot, relative)
	if err != nil {
		return nil, err
	}
	return root.Open(target)
}

func describeFileError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return errors.New("file not found")
	}
	return err
}

// redactURL keeps scheme, host and path only: userinfo, query and fragment
// commonly hold secrets or signatures.
func redactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "<invalid url>"
	}

	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}

func redactURLsInText(text string) string {
	return urlInTextPattern.ReplaceAllStringFunc(text, redactURL)
}

// githubIconCredentials limits credential lookups to GitHub hosts, mapping the
// raw-content host to the github.com credential that git helpers store.
type githubIconCredentials struct {
	inner download.CredentialResolver
}

func (r githubIconCredentials) Resolve(
	ctx context.Context,
	_, host, path string,
) (username, password string, err error) {
	switch strings.ToLower(host) {
	case "github.com", "raw.githubusercontent.com":
		repositoryPath := githubRepositoryCredentialPath(path)
		if repositoryPath == "" {
			return "", "", nil
		}
		return r.inner.Resolve(ctx, iconSchemeHTTPS, "github.com", repositoryPath)
	default:
		return "", "", nil
	}
}

func githubRepositoryCredentialPath(path string) string {
	if !strings.HasPrefix(path, "/") {
		return ""
	}
	owner, remaining, found := strings.Cut(path[1:], "/")
	if !found {
		return ""
	}
	repository, _, _ := strings.Cut(remaining, "/")
	if !isGitHubRepositorySegment(owner) || !isGitHubRepositorySegment(repository) {
		return ""
	}
	return "/" + owner + "/" + repository
}

func isGitHubRepositorySegment(segment string) bool {
	if segment == "" || segment == "." || segment == ".." {
		return false
	}
	for i := range segment {
		if !isGitHubRepositoryByte(segment[i]) {
			return false
		}
	}
	return true
}

func isGitHubRepositoryByte(value byte) bool {
	lower := value | 0x20
	return lower >= 'a' && lower <= 'z' ||
		value >= '0' && value <= '9' ||
		value == '.' || value == '_' || value == '-'
}
