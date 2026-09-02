package transport

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestCallbackConnEchoAndWait(t *testing.T) {
	conn, err := OpenCallbackConn(context.Background(), func(_ context.Context, stdin io.Reader, stdout io.Writer) error {
		_, err := io.Copy(stdout, stdin)
		return err
	}, CallbackConnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	input := []byte("managed callback")
	readDone := make(chan error, 1)
	go func() {
		_, err := conn.Write(input)
		if err != nil {
			readDone <- err
			return
		}
		output := make([]byte, len(input))
		_, err = io.ReadFull(conn, output)
		if err == nil && string(output) != string(input) {
			err = errors.New("echo mismatch")
		}
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("callback echo timed out")
	}
}

func TestCallbackConnPreservesErrorAndUnblocksOnClose(t *testing.T) {
	wantErr := errors.New("callback failed")
	conn, err := OpenCallbackConn(context.Background(), func(_ context.Context, _ io.Reader, _ io.Writer) error {
		return wantErr
	}, CallbackConnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Wait(); !errors.Is(err, wantErr) {
		t.Fatalf("Wait() = %v, want %v", err, wantErr)
	}
	if err := conn.Wait(); !errors.Is(err, wantErr) {
		t.Fatalf("second Wait() = %v, want %v", err, wantErr)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

func TestProcessConnEchoAndExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command differs on Windows")
	}
	conn, err := StartProcessConn(context.Background(), ProcessSpec{
		Command: []string{"sh", "-c", "cat"},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("managed process")
	if _, err := conn.Write(input); err != nil {
		t.Fatal(err)
	}
	output := make([]byte, len(input))
	if _, err := io.ReadFull(conn, output); err != nil {
		t.Fatal(err)
	}
	if string(output) != string(input) {
		t.Fatalf("output = %q, want %q", output, input)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := conn.Wait(); err == nil {
		t.Fatal("Wait() = nil, want cancellation error")
	}
}

func TestProcessConnReportsExitError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command differs on Windows")
	}
	conn, err := StartProcessConn(context.Background(), ProcessSpec{
		Command: []string{"sh", "-c", "exit 7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = conn.Wait()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Wait() = %v, want exec.ExitError", err)
	}
	if exitErr.ExitCode() != 7 {
		t.Fatalf("exit code = %d, want 7", exitErr.ExitCode())
	}
}
