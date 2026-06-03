package mcp

import (
	"strings"
	"testing"
)

func TestBoundedBuffer_NoTruncation(t *testing.T) {
	b := NewBoundedBuffer(100)
	_, _ = b.Write([]byte("hello"))
	if got := b.String(); got != "hello" {
		t.Fatalf("got %q", got)
	}
	if b.Truncated() {
		t.Fatal("expected not truncated")
	}
}

func TestBoundedBuffer_TruncatesMiddle(t *testing.T) {
	b := NewBoundedBuffer(20)
	_, _ = b.Write([]byte(strings.Repeat("a", 50)))
	s := b.String()
	if !b.Truncated() {
		t.Fatal("expected truncated")
	}
	if !strings.Contains(s, "bytes truncated") {
		t.Fatalf("missing marker: %q", s)
	}
	if !strings.HasPrefix(s, "aaaa") || !strings.HasSuffix(s, "aaaa") {
		t.Fatalf("head/tail not preserved: %q", s)
	}
}

func TestBoundedBuffer_MultipleWritesAccumulate(t *testing.T) {
	b := NewBoundedBuffer(10)
	for range 5 {
		_, _ = b.Write([]byte("xxxx"))
	}
	if !b.Truncated() {
		t.Fatal("expected truncated after 20 bytes into cap 10")
	}
}

func TestBoundedBuffer_OddCap(t *testing.T) {
	// Odd cap should be rounded up so head+tail covers everything when
	// written exactly cap bytes.
	b2 := NewBoundedBuffer(101) // odd, > floor; rounded up to 102
	for range 101 {
		_, _ = b2.Write([]byte("a"))
	}
	if b2.Truncated() {
		t.Fatal("at exactly 101 bytes into cap 102, should not be truncated")
	}
	if got := b2.String(); len(got) != 101 {
		t.Fatalf("expected 101 bytes, got %d", len(got))
	}
}
