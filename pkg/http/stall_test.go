package http

import (
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
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	client := &http.Client{Transport: NewStallTransport(http.DefaultTransport, 50*time.Millisecond)}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	_, err = io.ReadAll(resp.Body)
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("expected ErrStalled reading a stalled body, got %v", err)
	}
}

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
