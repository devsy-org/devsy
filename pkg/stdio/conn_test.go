package stdio

import (
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

type trackingWriteCloser struct {
	closeCalls atomic.Int32
	closeErr   error
}

func (w *trackingWriteCloser) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *trackingWriteCloser) Close() error {
	w.closeCalls.Add(1)
	return w.closeErr
}

func TestStdioStreamCloseDoesNotExitAndIsIdempotent(t *testing.T) {
	writer := &trackingWriteCloser{}
	var callbackCalls atomic.Int32
	stream := newStdioStream(strings.NewReader(""), writer, func() {
		callbackCalls.Add(1)
	})

	if err := stream.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	if got := writer.closeCalls.Load(); got != 1 {
		t.Fatalf("writer Close calls = %d, want 1", got)
	}
	if got := callbackCalls.Load(); got != 1 {
		t.Fatalf("close callback calls = %d, want 1", got)
	}

	// Reaching this assertion proves Close returned control to the process.
	if _, err := io.ReadAll(stream); err != nil {
		t.Fatalf("read after Close() error = %v", err)
	}
}

func TestStdioStreamCloseReturnsUnderlyingErrorOnce(t *testing.T) {
	wantErr := io.ErrClosedPipe
	writer := &trackingWriteCloser{closeErr: wantErr}
	stream := NewStdioStream(strings.NewReader(""), writer)

	if err := stream.Close(); err != wantErr {
		t.Fatalf("first Close() error = %v, want %v", err, wantErr)
	}
	if err := stream.Close(); err != wantErr {
		t.Fatalf("second Close() error = %v, want %v", err, wantErr)
	}
}
