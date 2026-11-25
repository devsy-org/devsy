package livetee

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"time"
)

const (
	chunkSize      = 8 << 10
	maxQueuedBytes = 1 << 20
	chunkOverhead  = 64
	DrainTimeout   = 100 * time.Millisecond
)

type chunk struct {
	data []byte
	live io.Writer
}

type Tee struct {
	capture  io.Writer
	live     io.Writer
	pending  []chunk
	wake     chan struct{}
	done     chan struct{}
	mu       sync.Mutex
	queued   int64
	used     int64
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
		wake:    make(chan struct{}, 1),
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
		t.signal()
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
		t.used = 0
		t.pending = nil
		t.signal()
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
	for {
		chunk, ok := t.next()
		if !ok {
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
			t.used -= int64(len(chunk.data) + chunkOverhead)
			t.dropped += int64(len(chunk.data) - n)
		}
		t.mu.Unlock()
	}
}

func (t *Tee) next() (chunk, bool) {
	for {
		t.mu.Lock()
		if t.stopped {
			t.mu.Unlock()
			return chunk{}, false
		}
		if len(t.pending) > 0 {
			c := t.pending[0]
			t.pending[0] = chunk{}
			t.pending = t.pending[1:]
			t.mu.Unlock()
			return c, true
		}
		closed := t.closed
		t.mu.Unlock()
		if closed {
			return chunk{}, false
		}
		<-t.wake
	}
}

func (t *Tee) signal() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

func (t *Tee) enqueue(p []byte, live io.Writer) {
	if live == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for len(p) > 0 {
		if t.closed {
			t.dropped += int64(len(p))
			return
		}
		p = p[t.admit(p, live):]
	}
	t.signal()
}

// admit consumes a prefix of p and returns its length. Bytes that do not fit
// the queue budget are counted as dropped, so a slow writer never blocks the
// command. Each chunk record is charged chunkOverhead against the same budget.
func (t *Tee) admit(p []byte, live io.Writer) int {
	n := min(len(p), chunkSize)
	t.dropped += int64(n - t.store(p[:n], live))
	return n
}

// store queues as much of p as the budget allows and returns that length.
func (t *Tee) store(p []byte, live io.Writer) int {
	kept := t.extendTail(p, live)
	p = p[kept:]
	if room := min(int64(len(p)), maxQueuedBytes-t.used-chunkOverhead); room > 0 {
		t.pending = append(t.pending, chunk{data: append([]byte(nil), p[:room]...), live: live})
		t.queued += room
		t.used += room + chunkOverhead
		kept += int(room)
	}
	return kept
}

// extendTail appends to the tail chunk, which the drain has not taken yet, so
// bursts of small writes cost bytes, not queue slots.
func (t *Tee) extendTail(p []byte, live io.Writer) int {
	if len(t.pending) == 0 {
		return 0
	}
	tail := &t.pending[len(t.pending)-1]
	n := min(int64(chunkSize-len(tail.data)), int64(len(p)), maxQueuedBytes-t.used)
	if n <= 0 || !sameWriter(tail.live, live) {
		return 0
	}
	tail.data = append(tail.data, p[:n]...)
	t.queued += n
	t.used += n
	return int(n)
}

func sameWriter(a, b io.Writer) bool {
	return reflect.ValueOf(a).Comparable() && reflect.ValueOf(b).Comparable() && a == b
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
