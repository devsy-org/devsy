package tunnel

import (
	"context"
	"errors"
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/util/goleaktest"
)

func TestMain(m *testing.M) {
	goleaktest.TestMain(m)
}

func TestNewPipeBridge(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	msg := []byte("hello")

	_, err = pb.StdoutWriter.Write(msg)
	if err != nil {
		t.Fatalf("write to stdout pipe: %v", err)
	}

	buf := make([]byte, len(msg))
	_, err = io.ReadFull(pb.StdoutReader, buf)
	if err != nil {
		t.Fatalf("read from stdout pipe: %v", err)
	}
	if string(buf) != "hello" {
		t.Errorf("stdout pipe: got %q, want %q", buf, "hello")
	}

	_, err = pb.StdinWriter.Write(msg)
	if err != nil {
		t.Fatalf("write to stdin pipe: %v", err)
	}

	_, err = io.ReadFull(pb.StdinReader, buf)
	if err != nil {
		t.Fatalf("read from stdin pipe: %v", err)
	}
	if string(buf) != "hello" {
		t.Errorf("stdin pipe: got %q, want %q", buf, "hello")
	}
}

func TestRunPairBothSucceed(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	tunnel := func(_ context.Context, stdin *os.File, stdout *os.File) error {
		buf := make([]byte, 4)
		_, err := io.ReadFull(stdin, buf)
		if err != nil {
			return err
		}
		_, err = stdout.Write(buf)
		return err
	}

	handler := func(_ context.Context, stdout *os.File, stdin *os.File) error {
		_, err := stdin.Write([]byte("ping"))
		if err != nil {
			return err
		}
		buf := make([]byte, 4)
		_, err = io.ReadFull(stdout, buf)
		if err != nil {
			return err
		}
		if string(buf) != "ping" {
			t.Errorf("handler got %q, want %q", buf, "ping")
		}
		return nil
	}

	err = pb.RunPair(context.Background(), tunnel, handler)
	if err != nil {
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

	dialErr := errors.New("dial failed")

	tunnel := func(_ context.Context, _ *os.File, _ *os.File) error {
		return dialErr
	}

	handler := func(_ context.Context, stdout *os.File, _ *os.File) error {
		buf := make([]byte, 1)
		_, err := stdout.Read(buf)
		return err
	}

	err = pb.RunPair(context.Background(), tunnel, handler)
	if err == nil {
		t.Fatal("RunPair() = nil, want error")
	}
	want := "connect to server: dial failed"
	if err.Error() != want {
		t.Errorf("RunPair() error = %q, want %q", err.Error(), want)
	}
}

func TestRunPairHandlerError(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	handlerErr := errors.New("handler broke")

	tunnel := func(ctx context.Context, _ *os.File, _ *os.File) error {
		<-ctx.Done()
		return ctx.Err()
	}

	handler := func(_ context.Context, _ *os.File, _ *os.File) error {
		return handlerErr
	}

	err = pb.RunPair(context.Background(), tunnel, handler)
	if err == nil {
		t.Fatal("RunPair() = nil, want error")
	}
	want := "tunnel to container: handler broke"
	if err.Error() != want {
		t.Errorf("RunPair() error = %q, want %q", err.Error(), want)
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
		return ctx.Err()
	}

	handler := func(_ context.Context, _ *os.File, _ *os.File) error {
		cancel()
		return nil
	}

	err = pb.RunPair(ctx, tunnel, handler)
	if err != nil {
		t.Errorf("RunPair() error = %v, want nil", err)
	}
}

func TestRunPairPipesClosedAfterReturn(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}

	tunnel := func(_ context.Context, _ *os.File, _ *os.File) error {
		return nil
	}
	handler := func(_ context.Context, _ *os.File, _ *os.File) error {
		return nil
	}

	_ = pb.RunPair(context.Background(), tunnel, handler)
	pb.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := pb.StdoutWriter.Write([]byte("x"))
		if err == nil {
			t.Error("write to closed StdoutWriter should fail")
		}
		_, err = pb.StdinWriter.Write([]byte("x"))
		if err == nil {
			t.Error("write to closed StdinWriter should fail")
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for write checks")
	}
}

func TestRunPairJoinsTunnelWhenHandlerFinishesFirst(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	tunnelExited := make(chan struct{})
	tunnel := func(_ context.Context, stdin *os.File, _ *os.File) error {
		buf := make([]byte, 1)
		_, err := stdin.Read(buf) // blocks until stdin write end is closed
		close(tunnelExited)
		return err
	}

	handler := func(_ context.Context, _ *os.File, _ *os.File) error {
		return nil
	}

	if err := pb.RunPair(context.Background(), tunnel, handler); err != nil {
		t.Fatalf("RunPair() error = %v", err)
	}

	select {
	case <-tunnelExited:
	case <-time.After(2 * time.Second):
		t.Fatal("tunnel goroutine was orphaned when handler finished first")
	}
}

func TestRunPairDoesNotHangWhenTunnelIgnoresCancellation(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	release := make(chan struct{})
	tunnelReturned := make(chan struct{})
	tunnel := func(ctx context.Context, _ *os.File, _ *os.File) error { //nolint:unparam
		<-ctx.Done()
		<-release
		close(tunnelReturned)
		return nil
	}

	handler := func(_ context.Context, _ *os.File, _ *os.File) error {
		return nil
	}

	done := make(chan error, 1)
	go func() { done <- pb.RunPair(context.Background(), tunnel, handler) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunPair() error = %v", err)
		}
		select {
		case <-tunnelReturned:
			t.Fatal("RunPair waited for the unresponsive tunnel; expected it to give up")
		default:
		}
	case <-time.After(8 * time.Second):
		t.Fatal("RunPair hung waiting for a tunnel that ignores cancellation")
	}

	// Release the abandoned tunnel goroutine so the test leaks nothing.
	close(release)
	<-tunnelReturned
}

func TestRunPairDoesNotHangWhenHandlerIgnoresCancellation(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	release := make(chan struct{})
	handlerReturned := make(chan struct{})
	tunnel := func(_ context.Context, _ *os.File, _ *os.File) error {
		return nil
	}
	handler := func(ctx context.Context, _ *os.File, _ *os.File) error { //nolint:unparam
		<-ctx.Done()
		<-release
		close(handlerReturned)
		return nil
	}

	done := make(chan error, 1)
	go func() { done <- pb.RunPair(context.Background(), tunnel, handler) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunPair() error = %v", err)
		}
		select {
		case <-handlerReturned:
			t.Fatal("RunPair waited for the unresponsive handler; expected it to give up")
		default:
		}
	case <-time.After(8 * time.Second):
		t.Fatal("RunPair hung waiting for a handler that ignores cancellation")
	}

	close(release)
	<-handlerReturned
}

func TestRunPairBothSidesAlwaysRun(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	var tunnelRan, handlerRan atomic.Bool
	tunnel := func(ctx context.Context, _ *os.File, _ *os.File) error {
		tunnelRan.Store(true)
		<-ctx.Done()
		return nil
	}
	handler := func(_ context.Context, _ *os.File, _ *os.File) error {
		handlerRan.Store(true)
		return nil
	}

	_ = pb.RunPair(context.Background(), tunnel, handler)

	if !tunnelRan.Load() {
		t.Fatal("tunnel side never ran")
	}
	if !handlerRan.Load() {
		t.Fatal("handler side never ran")
	}
}

func TestRunPairStopsBothSidesWhenParentContextCancelledBeforeEitherReturns(t *testing.T) {
	t.Parallel()

	pb, err := NewPipeBridge()
	if err != nil {
		t.Fatalf("NewPipeBridge() error = %v", err)
	}
	defer pb.Close()

	ctx, cancel := context.WithCancel(context.Background())

	tunnel := func(_ context.Context, stdin *os.File, _ *os.File) error {
		buf := make([]byte, 1)
		_, err := stdin.Read(buf)
		return err
	}
	handler := func(_ context.Context, stdout *os.File, _ *os.File) error {
		buf := make([]byte, 1)
		_, err := stdout.Read(buf)
		return err
	}

	done := make(chan error, 1)
	go func() { done <- pb.RunPair(ctx, tunnel, handler) }()

	time.Sleep(50 * time.Millisecond) // let both sides actually enter Read
	cancel()                          // parent ctx ends while both are still blocked

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunPair hung after parent ctx cancellation with both sides blocked in Read")
	}
}
