package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"k8s.io/apimachinery/pkg/util/wait"
)

var DefaultDownloadBackoff = wait.Backoff{
	Duration: 1 * time.Second,
	Factor:   2.0,
	Steps:    3,
}

const DefaultStallTimeout = 60 * time.Second

var (
	downloadClientOnce sync.Once
	downloadClient     *http.Client
)

func defaultDownloadClient() *http.Client {
	downloadClientOnce.Do(func() { downloadClient = newStallClient(DefaultStallTimeout) })
	return downloadClient
}

// newStallClient builds a download client with a stall timeout but no retry:
// DownloadToFile owns download retries (including truncation).
func newStallClient(timeout time.Duration) *http.Client {
	rt := WithUserAgent(baseRoundTripper(), UserAgent())
	rt = NewStallTransport(rt, timeout)
	return &http.Client{Transport: rt}
}

type downloadConfig struct {
	headers     map[string]string
	backoff     wait.Backoff
	client      *http.Client
	mode        os.FileMode
	wrap        func(r io.Reader, totalSize int64) io.Reader
	skipIfSized bool
}

// DownloadOption customizes DownloadToFile.
type DownloadOption func(*downloadConfig)

// WithHeaders sets request headers sent on every download attempt.
func WithHeaders(headers map[string]string) DownloadOption {
	return func(c *downloadConfig) { c.headers = headers }
}

// WithBackoff overrides the retry backoff policy.
func WithBackoff(backoff wait.Backoff) DownloadOption {
	return func(c *downloadConfig) { c.backoff = backoff }
}

// WithClient overrides the HTTP client used for the download.
func WithClient(client *http.Client) DownloadOption {
	return func(c *downloadConfig) { c.client = client }
}

// WithMode sets the permission bits for the destination file (default 0o600),
// for downloads that must be executable.
func WithMode(mode os.FileMode) DownloadOption {
	return func(c *downloadConfig) { c.mode = mode }
}

// WithProgress wraps the response body on each attempt, e.g. to report download
// progress. wrap receives the body reader and the advertised size (-1 when
// unknown) and returns the reader to copy from.
func WithProgress(wrap func(r io.Reader, totalSize int64) io.Reader) DownloadOption {
	return func(c *downloadConfig) { c.wrap = wrap }
}

// SkipIfSameSize skips the download when destPath already exists and its size
// matches the advertised Content-Length, reusing the cached file.
func SkipIfSameSize() DownloadOption {
	return func(c *downloadConfig) { c.skipIfSized = true }
}

// WithStallTimeout swaps in a client with the given inactivity timeout.
func WithStallTimeout(d time.Duration) DownloadOption {
	return func(c *downloadConfig) { c.client = newStallClient(d) }
}

// permanentError marks a failure that retrying will not resolve.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// DownloadToFile downloads url into destPath, retrying transient failures
// (connection errors, 5xx, 429, truncated transfers) and failing fast on 4xx.
func DownloadToFile(ctx context.Context, url, destPath string, opts ...DownloadOption) error {
	cfg := downloadConfig{
		backoff: DefaultDownloadBackoff,
		client:  defaultDownloadClient(),
		mode:    0o600,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	var lastErr error
	err := wait.ExponentialBackoffWithContext(
		ctx,
		cfg.backoff,
		func(ctx context.Context) (bool, error) {
			lastErr = downloadOnce(ctx, &cfg, url, destPath)
			switch {
			case lastErr == nil:
				return true, nil
			case isPermanent(lastErr):
				return false, lastErr
			default:
				log.Debugf("download attempt failed, retrying: url=%s err=%v", url, lastErr)
				return false, nil
			}
		},
	)
	if wait.Interrupted(err) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return lastErr
	}
	return err
}

func isPermanent(err error) bool {
	var perr *permanentError
	return errors.As(err, &perr)
}

type downloadAttempt struct {
	cfg      *downloadConfig
	url      string
	destPath string
}

func downloadOnce(ctx context.Context, cfg *downloadConfig, url, destPath string) error {
	a := &downloadAttempt{cfg: cfg, url: url, destPath: destPath}
	return a.do(ctx)
}

func (a *downloadAttempt) do(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.url, nil)
	if err != nil {
		return &permanentError{fmt.Errorf("download %s: %w", a.url, err)}
	}
	for key, value := range a.cfg.headers {
		req.Header.Set(key, value)
	}

	resp, err := a.cfg.client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", a.url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		statusErr := fmt.Errorf("download %s: %s", a.url, resp.Status)
		if retryableStatus(resp.StatusCode) {
			return statusErr
		}
		return &permanentError{statusErr}
	}

	return a.copyToFile(resp)
}

func retryableStatus(code int) bool {
	return code >= http.StatusInternalServerError || code == http.StatusTooManyRequests
}

func (a *downloadAttempt) copyToFile(resp *http.Response) error {
	if alreadyDownloaded(a.cfg, resp, a.destPath) {
		return nil
	}

	destPath := filepath.Clean(a.destPath)
	destDir := filepath.Dir(destPath)
	// #nosec G301 -- match existing download destinations; contents are public artifacts.
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return &permanentError{fmt.Errorf("create download folder: %w", err)}
	}

	// Write to a temp file and rename on success so a failed transfer never
	// leaves a partial file at destPath.
	tmpPath, err := a.streamToTempFile(resp, destDir, destPath)
	if err != nil {
		return err
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("download %s: %w", a.url, err)
	}
	return nil
}

func (a *downloadAttempt) streamToTempFile(
	resp *http.Response, destDir, destPath string,
) (string, error) {
	tmp, err := os.CreateTemp(destDir, "."+filepath.Base(destPath)+".*.tmp")
	if err != nil {
		return "", &permanentError{fmt.Errorf("create download file: %w", err)}
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(a.cfg.mode); err != nil {
		return "", &permanentError{fmt.Errorf("set download file mode: %w", err)}
	}

	written, err := io.Copy(tmp, a.source(resp))
	if err != nil {
		return "", fmt.Errorf("download %s: %w", a.url, err)
	}
	if err := verifyContentLength(resp, written, a.url); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("download %s: %w", a.url, err)
	}
	committed = true
	return tmpPath, nil
}

func (a *downloadAttempt) source(resp *http.Response) io.Reader {
	if a.cfg.wrap != nil {
		return a.cfg.wrap(resp.Body, resp.ContentLength)
	}
	return resp.Body
}

func alreadyDownloaded(cfg *downloadConfig, resp *http.Response, destPath string) bool {
	if !cfg.skipIfSized || resp.ContentLength < 0 {
		return false
	}
	info, err := os.Stat(filepath.Clean(destPath))
	return err == nil && info.Size() == resp.ContentLength
}

func verifyContentLength(resp *http.Response, written int64, url string) error {
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		return fmt.Errorf(
			"download %s: incomplete transfer, got %d of %d bytes",
			url, written, resp.ContentLength,
		)
	}
	return nil
}
