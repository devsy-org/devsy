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

// RetryConfig configures RetryTransport.
type RetryConfig struct {
	MaxAttempts int           // total attempts including the first (values <= 1 disable retry)
	BaseDelay   time.Duration // first backoff delay; doubles each attempt
	MaxDelay    time.Duration // cap for both backoff and honored Retry-After
}

// DefaultRetry is the retry policy applied to the shared general-purpose client.
var DefaultRetry = RetryConfig{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: 30 * time.Second}

// maxDrainOnRetry bounds how much of a discarded response body is read to keep
// the connection reusable before the next attempt.
const maxDrainOnRetry = 4 << 10

// RetryTransport retries idempotent requests (GET, HEAD) on connection errors
// and retryable statuses (5xx, 429) with exponential backoff, honoring the
// server's Retry-After hint. Non-idempotent methods and caller-cancelled
// requests are never retried, so it is safe on the shared client.
type RetryTransport struct {
	base http.RoundTripper
	cfg  RetryConfig
}

// NewRetryTransport wraps base with cfg. A policy of at most one attempt returns
// base unchanged.
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
	if !idempotentMethod(req.Method) {
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

// retryable reports whether a failed attempt should be retried. Caller
// cancellation and deadline expiry are never retried.
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

func idempotentMethod(method string) bool {
	return method == "" || method == http.MethodGet || method == http.MethodHead
}

// ParseRetryAfter returns the delay requested by a Retry-After header, in either
// delta-seconds or HTTP-date form. Negative or malformed values report false.
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
