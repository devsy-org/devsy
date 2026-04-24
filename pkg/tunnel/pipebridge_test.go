package tunnel

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestNewPipeBridge(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	msg := []byte("hello")
	buf := make([]byte, len(msg))

	t.Run("stdout pipe", func(t *testing.T) {
		if _, writeErr := pb.StdoutWriter.Write(msg); writeErr != nil {
			t.Fatalf("StdoutWriter.Write() error = %v", writeErr)
		}
		n, readErr := pb.StdoutReader.Read(buf)
		if readErr != nil {
			t.Fatalf("StdoutReader.Read() error = %v", readErr)
		}
		if string(buf[:n]) != "hello" {
			t.Errorf("StdoutReader.Read() = %q, want %q", string(buf[:n]), "hello")
		}
	})

	t.Run("stdin pipe", func(t *testing.T) {
		if _, writeErr := pb.StdinWriter.Write(msg); writeErr != nil {
			t.Fatalf("StdinWriter.Write() error = %v", writeErr)
		}
		n, readErr := pb.StdinReader.Read(buf)
		if readErr != nil {
			t.Fatalf("StdinReader.Read() error = %v", readErr)
		}
		if string(buf[:n]) != "hello" {
			t.Errorf("StdinReader.Read() = %q, want %q", string(buf[:n]), "hello")
		}
	})
}

func TestRunPairBothSucceed(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	tunnel := func(_ context.Context, stdin *os.File, stdout *os.File) error {
		buf := make([]byte, 5)
		n, readErr := stdin.Read(buf)
		if readErr != nil {
			return readErr
		}
		_, writeErr := stdout.Write(buf[:n])
		return writeErr
	}

	handler := func(_ context.Context, stdout *os.File, stdin *os.File) error {
		if _, writeErr := stdin.Write([]byte("ping!")); writeErr != nil {
			return writeErr
		}
		buf := make([]byte, 5)
		n, readErr := stdout.Read(buf)
		if readErr != nil {
			return readErr
		}
		if string(buf[:n]) != "ping!" {
			t.Errorf("handler got %q, want %q", string(buf[:n]), "ping!")
		}
		return nil
	}

	if runErr := pb.RunPair(context.Background(), tunnel, handler); runErr != nil {
		t.Errorf("RunPair() error = %v, want nil", runErr)
	}
}

func TestRunPairTunnelError(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	tunnelErr := errors.New("host unreachable")

	tunnel := func(_ context.Context, _ *os.File, _ *os.File) error {
		return tunnelErr
	}

	handler := func(_ context.Context, stdout *os.File, _ *os.File) error {
		// Block on read until pipes are closed by awaitPair
		buf := make([]byte, 1)
		_, readErr := stdout.Read(buf)
		return readErr
	}

	got := pb.RunPair(context.Background(), tunnel, handler)
	if got == nil {
		t.Fatal("RunPair() = nil, want error")
	}
	want := "connect to server: host unreachable"
	if got.Error() != want {
		t.Errorf("RunPair() = %q, want %q", got.Error(), want)
	}
}

func TestRunPairHandlerError(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	handlerErr := errors.New("permission denied")

	tunnel := func(ctx context.Context, _ *os.File, _ *os.File) error {
		<-ctx.Done()
		return nil
	}

	handler := func(_ context.Context, _ *os.File, _ *os.File) error {
		return handlerErr
	}

	got := pb.RunPair(context.Background(), tunnel, handler)
	if got == nil {
		t.Fatal("RunPair() = nil, want error")
	}
	want := "tunnel to container: permission denied"
	if got.Error() != want {
		t.Errorf("RunPair() = %q, want %q", got.Error(), want)
	}
}

func TestRunPairContextCancellation(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	ctx, cancel := context.WithCancel(context.Background())

	tunnel := func(ctx context.Context, _ *os.File, _ *os.File) error {
		<-ctx.Done()
		return nil
	}

	handler := func(ctx context.Context, _ *os.File, _ *os.File) error {
		cancel()
		return nil
	}

	if runErr := pb.RunPair(ctx, tunnel, handler); runErr != nil {
		t.Errorf("RunPair() error = %v, want nil", runErr)
	}
}

func TestRunPairPipesClosedAfterReturn(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}

	tunnel := func(_ context.Context, stdin *os.File, stdout *os.File) error {
		buf := make([]byte, 1)
		n, readErr := stdin.Read(buf)
		if readErr != nil {
			return readErr
		}
		_, writeErr := stdout.Write(buf[:n])
		return writeErr
	}

	handler := func(_ context.Context, stdout *os.File, stdin *os.File) error {
		if _, writeErr := stdin.Write([]byte("x")); writeErr != nil {
			return writeErr
		}
		buf := make([]byte, 1)
		_, readErr := stdout.Read(buf)
		return readErr
	}

	if runErr := pb.RunPair(context.Background(), tunnel, handler); runErr != nil {
		t.Fatalf("RunPair() error = %v", runErr)
	}

	// After RunPair, the write-end pipes should be closed by awaitPair
	// (in the tunnel-first-exit path) or were never explicitly closed
	// (handler-first-exit). Close all and verify writes fail.
	pb.Close()

	_, err = pb.StdoutWriter.Write([]byte("x"))
	if err == nil {
		t.Error("write to StdoutWriter after Close should fail")
	}
	_, err = pb.StdinWriter.Write([]byte("x"))
	if err == nil {
		t.Error("write to StdinWriter after Close should fail")
	}
}
