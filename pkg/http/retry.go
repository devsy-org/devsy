package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
)

type RetryConfig struct {
	MaxAttempts int // <=1 disables retry
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

var DefaultRetry = RetryConfig{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: 30 * time.Second}

const maxDrainOnRetry = 4 << 10

// RetryTransport retries idempotent requests on connection errors, 5xx and 429,
// honoring Retry-After. Non-idempotent and cancelled requests pass through.
type RetryTransport struct {
	base http.RoundTripper
	cfg  RetryConfig
}

func NewRetryTransport(base http.RoundTripper, cfg RetryConfig) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if cfg.MaxAttempts <= 1 {
		return base
	}
	return &RetryTransport{base: base, cfg: cfg}
}

func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !retriableRequest(req) {
		return t.base.RoundTrip(req)
	}

	for attempt := 1; ; attempt++ {
		resp, err := t.base.RoundTrip(req)
		if attempt >= t.cfg.MaxAttempts || !t.retryable(resp, err) {
			return resp, err
		}

		delay := t.backoff(attempt)
		if resp != nil {
			if after, ok := ParseRetryAfter(resp); ok {
				delay = minDuration(after, t.cfg.MaxDelay)
			}
			drainAndClose(resp)
		}
		if sleepErr := sleepContext(req.Context(), delay); sleepErr != nil {
			return nil, sleepErr
		}
		log.Debugf("retrying request: method=%s url=%s attempt=%d", req.Method, req.URL, attempt+1)
	}
}

func (t *RetryTransport) retryable(resp *http.Response, err error) bool {
	if err != nil {
		return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
	}
	return resp.StatusCode >= http.StatusInternalServerError ||
		resp.StatusCode == http.StatusTooManyRequests
}

func (t *RetryTransport) backoff(attempt int) time.Duration {
	delay := t.cfg.BaseDelay << (attempt - 1)
	return minDuration(delay, t.cfg.MaxDelay)
}

// retriableRequest allows retry only for idempotent, bodyless requests; a
// consumed request body cannot be safely replayed.
func retriableRequest(req *http.Request) bool {
	if req.Body != nil && req.Body != http.NoBody {
		return false
	}
	return req.Method == "" || req.Method == http.MethodGet || req.Method == http.MethodHead
}

// ParseRetryAfter reads a Retry-After header in delta-seconds or HTTP-date form.
func ParseRetryAfter(resp *http.Response) (time.Duration, bool) {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := time.Until(at); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

func drainAndClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainOnRetry))
	_ = resp.Body.Close()
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
