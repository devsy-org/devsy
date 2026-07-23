package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type stubRoundTripper struct {
	calls int
	fn    func(attempt int) (*http.Response, error)
}

func (s *stubRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	s.calls++
	return s.fn(s.calls)
}

func stubResp(code int, hdr http.Header) *http.Response {
	if hdr == nil {
		hdr = http.Header{}
	}
	return &http.Response{
		StatusCode: code,
		Header:     hdr,
		Body:       io.NopCloser(strings.NewReader("body")),
	}
}

func fastRetry() RetryConfig {
	return RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}
}

func mustGet(t *testing.T, method string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, "http://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestRetryTransportRetriesServerErrorThenSucceeds(t *testing.T) {
	stub := &stubRoundTripper{fn: func(attempt int) (*http.Response, error) {
		if attempt < 3 {
			return stubResp(http.StatusServiceUnavailable, nil), nil
		}
		return stubResp(http.StatusOK, nil), nil
	}}
	rt := NewRetryTransport(stub, fastRetry())

	resp, err := rt.RoundTrip(mustGet(t, http.MethodGet))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if stub.calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", stub.calls)
	}
}

func TestRetryTransportGivesUpAfterMaxAttempts(t *testing.T) {
	stub := &stubRoundTripper{fn: func(int) (*http.Response, error) {
		return stubResp(http.StatusBadGateway, nil), nil
	}}
	rt := NewRetryTransport(stub, fastRetry())

	resp, err := rt.RoundTrip(mustGet(t, http.MethodGet))
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected last 502 returned, got %d", resp.StatusCode)
	}
	if stub.calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", stub.calls)
	}
}

func TestRetryTransportDoesNotRetryNonIdempotent(t *testing.T) {
	stub := &stubRoundTripper{fn: func(int) (*http.Response, error) {
		return stubResp(http.StatusServiceUnavailable, nil), nil
	}}
	rt := NewRetryTransport(stub, fastRetry())

	if _, err := rt.RoundTrip(mustGet(t, http.MethodPost)); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("expected POST not retried, got %d attempts", stub.calls)
	}
}

func TestRetryTransportDoesNotRetryRequestWithBody(t *testing.T) {
	stub := &stubRoundTripper{fn: func(int) (*http.Response, error) {
		return stubResp(http.StatusServiceUnavailable, nil), nil
	}}
	rt := NewRetryTransport(stub, fastRetry())

	req, err := http.NewRequest(http.MethodGet, "http://example.test", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	req.GetBody = nil
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("expected no retry for a request with a body, got %d attempts", stub.calls)
	}
}

func TestRetryTransportDoesNotRetryCancelledContext(t *testing.T) {
	stub := &stubRoundTripper{fn: func(int) (*http.Response, error) {
		return nil, context.Canceled
	}}
	rt := NewRetryTransport(stub, fastRetry())

	_, err := rt.RoundTrip(mustGet(t, http.MethodGet))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("expected no retry on cancellation, got %d attempts", stub.calls)
	}
}

func TestRetryTransportHonorsRetryAfterAndContext(t *testing.T) {
	stub := &stubRoundTripper{fn: func(int) (*http.Response, error) {
		return stubResp(http.StatusTooManyRequests, http.Header{"Retry-After": {"3600"}}), nil
	}}
	// Large MaxDelay so the honored Retry-After (1h) is the delay; a short
	// context deadline proves the sleep is context-aware.
	rt := NewRetryTransport(
		stub,
		RetryConfig{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: time.Hour},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := rt.RoundTrip(mustGet(t, http.MethodGet).WithContext(ctx))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded while honoring Retry-After, got %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("expected to block on Retry-After, got %d attempts", stub.calls)
	}
}

func TestNewRetryTransportDisabledReturnsBase(t *testing.T) {
	base := http.DefaultTransport
	if got := NewRetryTransport(base, RetryConfig{MaxAttempts: 1}); got != base {
		t.Fatalf("expected base returned when retry disabled")
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		wantOK bool
		want   time.Duration
	}{
		{"seconds", "5", true, 5 * time.Second},
		{"zero", "0", true, 0},
		{"negative", "-1", false, 0},
		{"garbage", "soon", false, 0},
		{"missing", "", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hdr := http.Header{}
			if tt.value != "" {
				hdr.Set("Retry-After", tt.value)
			}
			got, ok := ParseRetryAfter(&http.Response{Header: hdr})
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Fatalf("duration = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	future := time.Now().Add(2 * time.Hour).UTC().Format(http.TimeFormat)
	hdr := http.Header{}
	hdr.Set("Retry-After", future)
	got, ok := ParseRetryAfter(&http.Response{Header: hdr})
	if !ok || got <= 0 {
		t.Fatalf("expected a positive delay for a future date, got %v ok=%v", got, ok)
	}
}
