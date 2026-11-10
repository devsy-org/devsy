package livetee

import (
	"io"
	"sync"
	"time"
)

const (
	chunkSize    = 8 << 10
	queueChunks  = 128
	drainTimeout = 100 * time.Millisecond
)

type Tee struct {
	capture io.Writer
	chunks  chan []byte
	done    chan struct{}
	mu      sync.Mutex
	queued  int64
	dropped int64
	stopped bool
}

func New(capture, live io.Writer) *Tee {
	t := &Tee{capture: capture}
	if live != nil {
		t.chunks = make(chan []byte, queueChunks)
		t.done = make(chan struct{})
		go t.run(live)
	}
	return t
}

func (t *Tee) Write(p []byte) (int, error) {
	n, err := t.capture.Write(p)
	if t.chunks == nil {
		return n, err
	}
	for remaining := p[:n]; len(remaining) > 0; {
		size := min(len(remaining), chunkSize)
		chunk := make([]byte, size)
		copy(chunk, remaining[:size])
		t.mu.Lock()
		select {
		case t.chunks <- chunk:
			t.queued += int64(size)
		default:
			t.dropped += int64(size)
		}
		t.mu.Unlock()
		remaining = remaining[size:]
	}
	return n, err
}

func (t *Tee) Close() int64 {
	if t.chunks != nil {
		close(t.chunks)
		timer := time.NewTimer(drainTimeout)
		defer timer.Stop()
		select {
		case <-t.done:
		case <-timer.C:
			t.mu.Lock()
			t.stopped = true
			t.dropped += t.queued
			t.queued = 0
			t.mu.Unlock()
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dropped
}

func (t *Tee) run(live io.Writer) {
	defer close(t.done)
	for chunk := range t.chunks {
		t.mu.Lock()
		if t.stopped {
			t.mu.Unlock()
			return
		}
		t.queued -= int64(len(chunk))
		t.mu.Unlock()
		n, _ := live.Write(chunk)
		n = max(0, min(n, len(chunk)))
		t.mu.Lock()
		t.dropped += int64(len(chunk) - n)
		t.mu.Unlock()
	}
}
