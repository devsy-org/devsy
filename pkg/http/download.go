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

// DefaultStallTimeout bounds how long a download attempt may make no progress
// (no response headers or no bytes read) before it is aborted and retried. It
// guards against unresponsive endpoints without penalizing large downloads
// that are still transferring data, since the shared HTTP client has no
// overall Client.Timeout.
const DefaultStallTimeout = 60 * time.Second

type downloadConfig struct {
	headers      map[string]string
	backoff      wait.Backoff
	client       *http.Client
	mode         os.FileMode
	wrap         func(r io.Reader, totalSize int64) io.Reader
	skipIfSized  bool
	stallTimeout time.Duration
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

// WithStallTimeout overrides how long an attempt may make no progress before it
// is aborted. A non-positive value disables the stall guard entirely.
func WithStallTimeout(d time.Duration) DownloadOption {
	return func(c *downloadConfig) { c.stallTimeout = d }
}

// permanentError marks a failure that will not be resolved by retrying, such as
// a 4xx response or a malformed request.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// DownloadToFile downloads url into destPath, retrying transient failures
// (connection errors, 5xx responses, and truncated transfers) with backoff and
// failing fast on permanent ones (4xx).
func DownloadToFile(ctx context.Context, url, destPath string, opts ...DownloadOption) error {
	cfg := downloadConfig{
		backoff:      DefaultDownloadBackoff,
		client:       GetHTTPClient(),
		mode:         0o600,
		stallTimeout: DefaultStallTimeout,
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
		return lastErr
	}
	return err
}

func isPermanent(err error) bool {
	var perr *permanentError
	return errors.As(err, &perr)
}

func downloadOnce(ctx context.Context, cfg *downloadConfig, url, destPath string) error {
	var wd *stallWatchdog
	if cfg.stallTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		wd = newStallWatchdog(cfg.stallTimeout, cancel)
		defer wd.stop()
	}

	err := doDownload(ctx, cfg, url, destPath, wd)
	// A stall aborts via context cancellation; surface it as a clear, retryable
	// error instead of a bare "context canceled".
	if wd != nil && wd.fired() && errors.Is(err, context.Canceled) {
		return fmt.Errorf("download %s: stalled for %s", url, cfg.stallTimeout)
	}
	return err
}

func doDownload(
	ctx context.Context, cfg *downloadConfig, url, destPath string, wd *stallWatchdog,
) error {
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

	// Headers received: give the body transfer a fresh inactivity window.
	if wd != nil {
		wd.reset()
	}

	if resp.StatusCode >= http.StatusBadRequest {
		statusErr := fmt.Errorf("download %s: %s", url, resp.Status)
		if resp.StatusCode < http.StatusInternalServerError {
			return &permanentError{statusErr}
		}
		return statusErr
	}

	return copyToFile(resp, cfg, destPath, url, wd)
}

func copyToFile(
	resp *http.Response, cfg *downloadConfig, destPath, url string, wd *stallWatchdog,
) error {
	if alreadyDownloaded(cfg, resp, destPath) {
		return nil
	}

	destPath = filepath.Clean(destPath)
	destDir := filepath.Dir(destPath)
	// #nosec G301 -- match existing download destinations; contents are public artifacts.
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return &permanentError{fmt.Errorf("create download folder: %w", err)}
	}

	// Download to a temporary file in the destination directory and rename it
	// into place only on success, so a failed transfer never leaves a partial
	// file at destPath (which callers may mistake for a complete download).
	tmp, err := os.CreateTemp(destDir, "."+filepath.Base(destPath)+".*.tmp")
	if err != nil {
		return &permanentError{fmt.Errorf("create download file: %w", err)}
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(cfg.mode); err != nil {
		return &permanentError{fmt.Errorf("set download file mode: %w", err)}
	}

	src := io.Reader(resp.Body)
	if cfg.wrap != nil {
		src = cfg.wrap(resp.Body, resp.ContentLength)
	}
	if wd != nil {
		src = &stallResetReader{r: src, wd: wd}
	}
	written, err := io.Copy(tmp, src)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if err := verifyContentLength(resp, written, url); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	committed = true
	return nil
}

// alreadyDownloaded reports whether destPath already holds the full download,
// based on a size match against the advertised Content-Length.
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

// stallWatchdog aborts a download attempt (by cancelling its context) when no
// progress is made within timeout. Each observed unit of progress reschedules
// the deadline.
type stallWatchdog struct {
	timeout   time.Duration
	timer     *time.Timer
	mu        sync.Mutex
	triggered bool
}

func newStallWatchdog(timeout time.Duration, cancel context.CancelFunc) *stallWatchdog {
	w := &stallWatchdog{timeout: timeout}
	w.timer = time.AfterFunc(timeout, func() {
		w.mu.Lock()
		w.triggered = true
		w.mu.Unlock()
		cancel()
	})
	return w
}

func (w *stallWatchdog) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.triggered {
		return
	}
	w.timer.Reset(w.timeout)
}

func (w *stallWatchdog) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.timer.Stop()
}

func (w *stallWatchdog) fired() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.triggered
}

// stallResetReader reschedules the watchdog deadline on each successful read so
// a steadily-progressing transfer is never aborted.
type stallResetReader struct {
	r  io.Reader
	wd *stallWatchdog
}

func (s *stallResetReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.wd.reset()
	}
	return n, err
}
