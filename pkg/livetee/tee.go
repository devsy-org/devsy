package livetee

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	chunkSize    = 8 << 10
	queueChunks  = 128
	DrainTimeout = 100 * time.Millisecond
)

type chunk struct {
	data []byte
	live io.Writer
}

type Tee struct {
	capture  io.Writer
	live     io.Writer
	chunks   chan chunk
	done     chan struct{}
	mu       sync.Mutex
	queued   int64
	dropped  int64
	stopped  bool
	closed   bool
	closeErr error
	writeErr error
}

func New(capture, live io.Writer) *Tee {
	return NewWithClose(capture, live, nil)
}

func NewWithClose(capture, live io.Writer, closeLive func() error) *Tee {
	t := &Tee{
		capture: capture,
		live:    live,
		chunks:  make(chan chunk, queueChunks),
		done:    make(chan struct{}),
	}
	go t.run(closeLive)
	return t
}

func (t *Tee) Writer(live io.Writer) io.Writer {
	return streamWriter{tee: t, live: live}
}

type streamWriter struct {
	tee  *Tee
	live io.Writer
}

func (w streamWriter) Write(p []byte) (int, error) {
	w.tee.enqueue(p, w.live)
	return len(p), nil
}

func (t *Tee) Write(p []byte) (int, error) {
	n, err := t.capture.Write(p)
	t.enqueue(p[:n], t.live)
	return n, err
}

func (t *Tee) Close() int64 {
	dropped, _ := t.CloseWithStatus()
	return dropped
}

func (t *Tee) CloseWithStatus() (int64, bool) {
	t.mu.Lock()
	if !t.closed {
		t.closed = true
		close(t.chunks)
	}
	t.mu.Unlock()
	timer := time.NewTimer(DrainTimeout)
	defer timer.Stop()
	abandoned := t.waitForDrain(timer.C)
	if abandoned {
		t.mu.Lock()
		t.stopped = true
		t.dropped += t.queued
		t.queued = 0
		t.mu.Unlock()
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dropped, abandoned
}

func (t *Tee) Finish() error {
	dropped, abandoned := t.CloseWithStatus()
	t.mu.Lock()
	defer t.mu.Unlock()
	var outputErr error
	if dropped != 0 || abandoned {
		outputErr = fmt.Errorf(
			"live output: %d bytes dropped, drain abandoned: %t",
			dropped,
			abandoned,
		)
	}
	return errors.Join(outputErr, t.writeErr, t.closeErr)
}

func (t *Tee) run(closeLive func() error) {
	defer close(t.done)
	if closeLive != nil {
		defer func() {
			err := closeLive()
			t.mu.Lock()
			t.closeErr = err
			t.mu.Unlock()
		}()
	}
	for chunk := range t.chunks {
		t.mu.Lock()
		stopped := t.stopped
		t.mu.Unlock()
		if stopped {
			return
		}
		n, err := chunk.live.Write(chunk.data)
		n = max(0, min(n, len(chunk.data)))
		t.mu.Lock()
		if t.writeErr == nil {
			t.writeErr = err
		}
		if !t.stopped {
			t.queued -= int64(len(chunk.data))
			t.dropped += int64(len(chunk.data) - n)
		}
		t.mu.Unlock()
	}
}

func (t *Tee) enqueue(p []byte, live io.Writer) {
	if live == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for len(p) > 0 {
		size := min(len(p), chunkSize)
		if t.closed {
			t.dropped += int64(len(p))
			return
		}
		if len(t.chunks) == cap(t.chunks) {
			t.dropped += int64(size)
		} else {
			data := append([]byte(nil), p[:size]...)
			t.chunks <- chunk{data: data, live: live}
			t.queued += int64(size)
		}

		p = p[size:]
	}
}

func (t *Tee) waitForDrain(deadline <-chan time.Time) bool {
	select {
	case <-t.done:
		return false
	case <-deadline:
		select {
		case <-t.done:
			return false
		default:
			return true
		}
	}
}
