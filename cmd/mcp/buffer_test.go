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
