package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewStallTransportPassthroughWhenDisabled(t *testing.T) {
	base := http.DefaultTransport
	if got := NewStallTransport(base, 0); got != base {
		t.Fatalf("expected base transport returned unchanged when timeout <= 0")
	}
	if _, ok := NewStallTransport(base, time.Second).(*StallTransport); !ok {
		t.Fatalf("expected a *StallTransport when timeout > 0")
	}
}

func TestStallTransportAbortsIdleBody(t *testing.T) {
	base := stallTestRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       &stallTestBody{ctx: req.Context()},
			Request:    req,
		}, nil
	})
	client := &http.Client{Transport: NewStallTransport(base, 50*time.Millisecond)}
	resp, err := client.Get("http://stall.test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	_, err = io.ReadAll(resp.Body)
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("expected ErrStalled reading a stalled body, got %v", err)
	}
}

type stallTestRoundTripper func(*http.Request) (*http.Response, error)

func (fn stallTestRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type stallTestBody struct {
	ctx  context.Context
	sent bool
}

func (b *stallTestBody) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		return copy(p, "partial"), nil
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *stallTestBody) Close() error { return nil }

func TestStallTransportAllowsProgressingBody(t *testing.T) {
	const body = "steadily-delivered-payload"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	client := &http.Client{Transport: NewStallTransport(http.DefaultTransport, 50*time.Millisecond)}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("expected clean read, got %v", err)
	}
	if string(got) != body {
		t.Fatalf("body mismatch: got %q want %q", got, body)
	}
}

func TestStallTransportFollowsRedirect(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("final-content"))
	}))
	defer origin.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, origin.URL, http.StatusFound)
	}))
	defer redir.Close()

	client := &http.Client{Transport: NewStallTransport(http.DefaultTransport, 5*time.Second)}
	resp, err := client.Get(redir.URL)
	if err != nil {
		t.Fatalf("Get through redirect: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "final-content" {
		t.Fatalf("got %q", body)
	}
}

func TestStallTransportHeaderStall(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	client := &http.Client{Transport: NewStallTransport(http.DefaultTransport, 50*time.Millisecond)}
	_, err := client.Get(srv.URL)
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("expected ErrStalled when headers never arrive, got %v", err)
	}
}
