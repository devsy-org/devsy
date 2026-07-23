package http

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"k8s.io/apimachinery/pkg/util/wait"
)

// DefaultDownloadBackoff retries a download three times with exponential
// backoff, matching the OCI pull retry policy used elsewhere.
var DefaultDownloadBackoff = wait.Backoff{
	Duration: 1 * time.Second,
	Factor:   2.0,
	Steps:    3,
}

// downloadConfig holds the resolved options for a download.
type downloadConfig struct {
	headers map[string]string
	backoff wait.Backoff
	client  *http.Client
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

// permanentError marks a failure that will not be resolved by retrying, such as
// a 4xx response or a malformed request.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// DownloadToFile downloads url into destPath, retrying transient failures
// (connection errors, 5xx responses, and truncated transfers) with backoff and
// failing fast on permanent ones (4xx). Each attempt truncates destPath so a
// partial download never leaks into the next attempt, and the transfer is
// verified against Content-Length when the server advertises it — so an
// incomplete download surfaces as a clear error here rather than as a later
// decode failure in the caller.
func DownloadToFile(ctx context.Context, url, destPath string, opts ...DownloadOption) error {
	cfg := downloadConfig{backoff: DefaultDownloadBackoff, client: GetHTTPClient()}
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
		return lastErr
	}
	return err
}

func isPermanent(err error) bool {
	var perr *permanentError
	return errors.As(err, &perr)
}

func downloadOnce(ctx context.Context, cfg *downloadConfig, url, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return &permanentError{fmt.Errorf("download %s: %w", url, err)}
	}
	for key, value := range cfg.headers {
		req.Header.Set(key, value)
	}

	resp, err := cfg.client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		statusErr := fmt.Errorf("download %s: %s", url, resp.Status)
		if resp.StatusCode < http.StatusInternalServerError {
			return &permanentError{statusErr}
		}
		return statusErr
	}

	return copyToFile(resp, destPath, url)
}

func copyToFile(resp *http.Response, destPath, url string) error {
	// #nosec G301 -- match existing download destinations; contents are public artifacts.
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return &permanentError{fmt.Errorf("create download folder: %w", err)}
	}

	file, err := os.OpenFile(filepath.Clean(destPath), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return &permanentError{fmt.Errorf("create download file: %w", err)}
	}
	defer func() { _ = file.Close() }()

	written, err := io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		return fmt.Errorf(
			"download %s: incomplete transfer, got %d of %d bytes",
			url, written, resp.ContentLength,
		)
	}
	return nil
}
