package log

import (
	"strings"
	"testing"
	"time"
)

// TestPipeJSONStreamWithFallback_OversizedLineUnblocksWriter verifies that a
// line exceeding the scanner's buffer cap doesn't leave the pipe reader open
// after the goroutine exits: a later Write must fail promptly instead of
// blocking forever with nothing left draining the pipe.
func TestPipeJSONStreamWithFallback_OversizedLineUnblocksWriter(t *testing.T) {
	writer, done := PipeJSONStreamWithFallback(PassthroughWriter())

	oversized := strings.Repeat("a", 2*1024*1024) + "\n"
	firstWriteDone := make(chan struct{})
	go func() {
		defer close(firstWriteDone)
		_, _ = writer.Write([]byte(oversized))
	}()

	select {
	case <-firstWriteDone:
	case <-time.After(2 * time.Second):
		t.Fatal("initial write blocked instead of being drained by the scanner")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not exit after oversized line")
	}

	writeDone := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte("more\n"))
		writeDone <- err
	}()

	select {
	case err := <-writeDone:
		if err == nil {
			t.Fatal("expected write to a closed pipe to fail")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("write blocked: reader was not closed when the scanner stopped early")
	}
}
