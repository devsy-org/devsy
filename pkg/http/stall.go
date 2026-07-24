package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

var ErrStalled = errors.New("stalled: no progress within timeout")

// StallTransport aborts a request that makes no progress within timeout.
// Unsafe for intentionally-idle responses (long-poll, watch streams).
type StallTransport struct {
	base    http.RoundTripper
	timeout time.Duration
}

func NewStallTransport(base http.RoundTripper, timeout time.Duration) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if timeout <= 0 {
		return base
	}
	return &StallTransport{base: base, timeout: timeout}
}

func (t *StallTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	wd := newStallWatchdog(t.timeout, cancel)

	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		wd.stop()
		cancel()
		if wd.fired() {
			return nil, ErrStalled
		}
		return nil, err
	}

	wd.reset()
	resp.Body = &stallBody{rc: resp.Body, wd: wd, cancel: cancel}
	return resp, nil
}

type stallBody struct {
	rc     io.ReadCloser
	wd     *stallWatchdog
	cancel context.CancelFunc
}

func (b *stallBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.wd.reset()
	}
	if err != nil && !errors.Is(err, io.EOF) && b.wd.fired() {
		return n, ErrStalled
	}
	return n, err
}

func (b *stallBody) Close() error {
	b.wd.stop()
	b.cancel()
	return b.rc.Close()
}

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
