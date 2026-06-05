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

func TestBoundedBuffer_TruncatesHead(t *testing.T) {
	// Write 130 bytes into a cap-64 buffer: only the last 64 bytes (tail) should survive.
	b := NewBoundedBuffer(64)
	_, _ = b.Write([]byte(strings.Repeat("a", 66) + strings.Repeat("b", 64)))
	s := b.String()
	if !b.Truncated() {
		t.Fatal("expected truncated")
	}
	if !strings.Contains(s, "bytes dropped") {
		t.Fatalf("missing drop marker: %q", s)
	}
	// The tail (last 64 bytes, all 'b') should be preserved.
	if !strings.HasSuffix(s, strings.Repeat("b", 64)) {
		t.Fatalf("tail not preserved: %q", s)
	}
	// The head ('a' bytes) should be dropped.
	if strings.Contains(s, "aaaa") {
		t.Fatalf("head should be dropped but was retained: %q", s)
	}
}

func TestBoundedBuffer_MultipleWritesAccumulate(t *testing.T) {
	b := NewBoundedBuffer(64) // min cap is 64
	for range 5 {
		_, _ = b.Write([]byte(strings.Repeat("x", 20)))
	}
	// 100 bytes written into cap 64: should be truncated.
	if !b.Truncated() {
		t.Fatal("expected truncated after 100 bytes into cap 64")
	}
}

func TestBoundedBuffer_TailPreservedAcrossSmallWrites(t *testing.T) {
	// Write many small chunks; the buffer should hold the most recent cap bytes.
	b := NewBoundedBuffer(64)
	for i := range 200 {
		_, _ = b.Write([]byte{byte('a' + (i % 26))})
	}
	if !b.Truncated() {
		t.Fatal("expected truncated")
	}
	s := b.String()
	// The last 64 chars of the 200-char sequence.
	var want strings.Builder
	for i := 136; i < 200; i++ {
		want.WriteString(string([]byte{byte('a' + (i % 26))}))
	}
	if !strings.HasSuffix(s, want.String()) {
		t.Fatalf(
			"tail not preserved: got suffix %q, want %q",
			s[len(s)-len(want.String()):],
			want.String(),
		)
	}
}

func TestBoundedBuffer_LargeChunkOverwrite(t *testing.T) {
	// A single write larger than cap should keep only the last cap bytes.
	b := NewBoundedBuffer(64)
	data := strings.Repeat("a", 30) + strings.Repeat("b", 64)
	_, _ = b.Write([]byte(data))
	s := b.String()
	if !b.Truncated() {
		t.Fatal("expected truncated")
	}
	if !strings.HasSuffix(s, strings.Repeat("b", 64)) {
		t.Fatalf("last cap bytes not preserved: %q", s)
	}
}
