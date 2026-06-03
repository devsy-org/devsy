package mcp

import "fmt"

// BoundedBuffer is an io.Writer that retains at most Cap bytes by keeping the
// first Cap/2 and last Cap/2 bytes written. String() reports the contents
// joined with a truncation marker when more than Cap bytes were written.
type BoundedBuffer struct {
	cap     int
	head    []byte
	tail    []byte
	written int64
}

func NewBoundedBuffer(cap int) *BoundedBuffer {
	if cap < 8 {
		cap = 8
	}
	return &BoundedBuffer{cap: cap}
}

func (b *BoundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	b.written += int64(n)
	half := b.cap / 2

	if len(b.head) < half {
		take := min(half-len(b.head), n)
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	if len(p) == 0 {
		return n, nil
	}

	b.tail = append(b.tail, p...)
	if len(b.tail) > half {
		b.tail = b.tail[len(b.tail)-half:]
	}
	return n, nil
}

func (b *BoundedBuffer) Truncated() bool {
	return b.written > int64(b.cap)
}

func (b *BoundedBuffer) BytesWritten() int64 { return b.written }

func (b *BoundedBuffer) String() string {
	if !b.Truncated() {
		return string(b.head) + string(b.tail)
	}
	dropped := b.written - int64(len(b.head)) - int64(len(b.tail))
	return fmt.Sprintf("%s\n... [%d bytes truncated] ...\n%s", b.head, dropped, b.tail)
}
