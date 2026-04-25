package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	if _, err := pb.StdoutWriter.Write(msg); err != nil {
		t.Fatalf("write to StdoutWriter: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(pb.StdoutReader, buf); err != nil {
		t.Fatalf("read from StdoutReader: %v", err)
	}
	if string(buf) != "hello" {
		t.Errorf("stdout pipe got %q, want %q", string(buf), "hello")
	}

	if _, err := pb.StdinWriter.Write(msg); err != nil {
		t.Fatalf("write to StdinWriter: %v", err)
	}
	if _, err := io.ReadFull(pb.StdinReader, buf); err != nil {
		t.Fatalf("read from StdinReader: %v", err)
	}
	if string(buf) != "hello" {
		t.Errorf("stdin pipe got %q, want %q", string(buf), "hello")
	}
}

func TestRunPairBothSucceed(t *testing.T) {
	t.Parallel()
	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	tunnelFn := func(_ context.Context, stdin *os.File, stdout *os.File) error {
		buf := make([]byte, 4)
		n, err := stdin.Read(buf)
		if err != nil {
			return err
		}
		_, err = stdout.Write(buf[:n])
		return err
	}
	handlerFn := func(_ context.Context, stdout *os.File, stdin *os.File) error {
		if _, err := stdin.Write([]byte("ping")); err != nil {
			return err
		}
		buf := make([]byte, 4)
		n, err := stdout.Read(buf)
		if err != nil {
			return err
		}
		if got := string(buf[:n]); got != "ping" {
			return fmt.Errorf("got %q, want %q", got, "ping")
		}
		return nil
	}

	if err := pb.RunPair(context.Background(), tunnelFn, handlerFn); err != nil {
		t.Errorf("RunPair() error = %v, want nil", err)
	}
}

func TestRunPairTunnelError(t *testing.T) {
	t.Parallel()
	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	tunnelFn := func(_ context.Context, _ *os.File, _ *os.File) error {
		return errors.New("tunnel exploded")
	}
	handlerFn := func(_ context.Context, stdout *os.File, _ *os.File) error {
		buf := make([]byte, 1)
		_, err := stdout.Read(buf)
		return err
	}

	got := pb.RunPair(context.Background(), tunnelFn, handlerFn)
	if got == nil {
		t.Fatal("RunPair() = nil, want error")
	}
	if msg := got.Error(); !stringContains(msg, "connect to server") {
		t.Errorf("RunPair() = %q, want substring %q", msg, "connect to server")
	}
}

func TestRunPairHandlerError(t *testing.T) {
	t.Parallel()
	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	tunnelFn := func(ctx context.Context, _ *os.File, _ *os.File) error {
		<-ctx.Done()
		return nil
	}
	handlerFn := func(_ context.Context, _ *os.File, _ *os.File) error {
		return errors.New("handler broke")
	}

	got := pb.RunPair(context.Background(), tunnelFn, handlerFn)
	if got == nil {
		t.Fatal("RunPair() = nil, want error")
	}
	if msg := got.Error(); !stringContains(msg, "tunnel to container") {
		t.Errorf("RunPair() = %q, want substring %q", msg, "tunnel to container")
	}
}

func TestRunPairContextCancellation(t *testing.T) {
	t.Parallel()
	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	tunnelFn := func(ctx context.Context, _ *os.File, _ *os.File) error {
		<-ctx.Done()
		return nil
	}
	handlerFn := func(ctx context.Context, _ *os.File, _ *os.File) error {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pb.RunPair(ctx, tunnelFn, handlerFn); err != nil {
		t.Errorf("RunPair() error = %v, want nil", err)
	}
}

func TestRunPairPipesClosedAfterReturn(t *testing.T) {
	t.Parallel()
	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}

	tunnelFn := func(_ context.Context, _ *os.File, _ *os.File) error { return nil }
	handlerFn := func(_ context.Context, _ *os.File, _ *os.File) error { return nil }

	_ = pb.RunPair(context.Background(), tunnelFn, handlerFn)
	pb.Close()

	_, err = pb.StdoutWriter.Write([]byte("x"))
	if err == nil {
		t.Error("write to StdoutWriter after Close() should fail")
	}
	_, err = pb.StdinWriter.Write([]byte("x"))
	if err == nil {
		t.Error("write to StdinWriter after Close() should fail")
	}
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
