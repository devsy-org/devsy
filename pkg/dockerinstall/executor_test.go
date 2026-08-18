package dockerinstall

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestExecutor() *Executor {
	return NewExecutor(&InstallOptions{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}})
}

const dpkgLockCmd = `echo "E: Could not get lock /var/lib/dpkg/lock" >&2; exit 1`

func TestRunWithRetry_ContextCancellationTakesPrecedence(t *testing.T) {
	e := newTestExecutor()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := e.RunWithRetry(ctx, "sh", dpkgLockCmd, 5*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRunWithRetry_DpkgLockTimeoutWrapsLastError(t *testing.T) {
	e := newTestExecutor()

	err := e.RunWithRetry(context.Background(), "sh", dpkgLockCmd, 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timeout waiting for dpkg lock") {
		t.Fatalf("expected dpkg lock timeout error, got %v", err)
	}
	if !strings.Contains(err.Error(), "exit status") {
		t.Fatalf("expected wrapped command error in message, got %v", err)
	}
}

func TestRunWithRetry_NonLockErrorReturnsImmediately(t *testing.T) {
	e := newTestExecutor()

	start := time.Now()
	err := e.RunWithRetry(context.Background(), "sh", "exit 1", 5*time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("expected immediate return without retrying, took %v", elapsed)
	}
}

func TestRunWithRetry_SucceedsOnSuccess(t *testing.T) {
	e := newTestExecutor()

	if err := e.RunWithRetry(context.Background(), "sh", "exit 0", 5*time.Second); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}
