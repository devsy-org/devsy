package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// ErrStalled indicates a request was aborted because it made no progress —
// neither response headers nor body bytes — within the configured timeout.
var ErrStalled = errors.New("stalled: no progress within timeout")

// StallTransport wraps a RoundTripper so a request is aborted when it makes no
// progress within timeout: no response headers after the request is sent, or no
// body bytes read while streaming. A steadily-progressing transfer keeps
// rescheduling the deadline, so this bounds unresponsive endpoints without
// penalizing large downloads.
//
// It is deliberately opt-in per client: an inactivity timeout is unsafe for
// intentionally-idle responses such as long-poll or watch streams, so it must
// not be applied to the shared general-purpose client.
type StallTransport struct {
	base    http.RoundTripper
	timeout time.Duration
}

// NewStallTransport returns base wrapped with an inactivity timeout. A
// non-positive timeout returns base unchanged.
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

	// Headers received: give the body transfer a fresh inactivity window and
	// tie the watchdog's lifetime to the response body.
	wd.reset()
	resp.Body = &stallBody{rc: resp.Body, wd: wd, cancel: cancel}
	return resp, nil
}

// stallBody reschedules the watchdog on each read and translates a
// watchdog-triggered cancellation into ErrStalled. Closing it stops the
// watchdog and releases the derived context.
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
	// Once the watchdog has fired the context is cancelled, so any resulting
	// (non-EOF) read error is attributable to the stall.
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

// stallWatchdog cancels a request (via cancel) when no progress is made within
// timeout. Each observed unit of progress reschedules the deadline.
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
