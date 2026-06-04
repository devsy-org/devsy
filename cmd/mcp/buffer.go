package mcp

import "fmt"

// BoundedBuffer is an io.Writer that keeps only the last cap bytes written.
// Tail retention beats mid-truncation for command output because endings carry
// exit status, errors, and final state.
type BoundedBuffer struct {
	cap     int
	buf     []byte
	written int64
}

// NewBoundedBuffer returns a BoundedBuffer with the given capacity (minimum 64).
func NewBoundedBuffer(cap int) *BoundedBuffer {
	if cap < 64 {
		cap = 64
	}
	return &BoundedBuffer{cap: cap, buf: make([]byte, 0, cap)}
}

func (b *BoundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.written += int64(n)
	if len(p) >= b.cap {
		// Incoming chunk fills or exceeds cap — keep only the last cap bytes.
		b.buf = append(b.buf[:0], p[len(p)-b.cap:]...)
		return n, nil
	}
	if len(b.buf)+len(p) > b.cap {
		drop := len(b.buf) + len(p) - b.cap
		b.buf = b.buf[drop:]
	}
	b.buf = append(b.buf, p...)
	return n, nil
}

// Truncated reports whether more bytes were written than the buffer can hold.
func (b *BoundedBuffer) Truncated() bool { return b.written > int64(b.cap) }

// BytesWritten returns the total number of bytes written, including dropped ones.
func (b *BoundedBuffer) BytesWritten() int64 { return b.written }

// String returns the buffered content. When truncated, a marker showing how
// many bytes were dropped is prepended so callers know output is incomplete.
func (b *BoundedBuffer) String() string {
	if !b.Truncated() {
		return string(b.buf)
	}
	return fmt.Sprintf("... [%d bytes dropped] ...\n%s", b.written-int64(len(b.buf)), b.buf)
}
