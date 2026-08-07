package gpg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildForwardArgs(t *testing.T) {
	got := buildForwardArgs("root", "test-context", "test-workspace")
	expected := []string{
		"workspace",
		"ssh",
		"--ssh-gpg-forwarding=true",
		"--agent-forwarding=true",
		"--start-services=true",
		"--user", "root",
		"--context", "test-context",
		"test-workspace",
		"--log-output=raw",
		"--command", "sleep infinity",
	}
	assert.Equal(t, expected, got)
}

func TestBuildForwardArgs_NonRootUser(t *testing.T) {
	got := buildForwardArgs("vscode", "test-context", "test-workspace")
	expected := []string{
		"workspace",
		"ssh",
		"--ssh-gpg-forwarding=true",
		"--agent-forwarding=true",
		"--start-services=true",
		"--user", "vscode",
		"--context", "test-context",
		"test-workspace",
		"--log-output=raw",
		"--command", "sleep infinity",
	}
	assert.Equal(t, expected, got)
}

func TestSuperviseForward_RestartsUntilCancelled(t *testing.T) {
	runs := filepath.Join(t.TempDir(), "runs")
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		superviseForward(ctx, forwardSpec{
			execPath: "/bin/sh",
			args:     []string{"-c", "printf x >> " + runs},
			backoff:  backoff{min: 10 * time.Millisecond, max: 20 * time.Millisecond},
		}, make(chan struct{}, 1))
		close(done)
	}()

	require.Eventually(t, func() bool {
		data, _ := os.ReadFile(runs) //nolint:gosec // test path is created by the test
		return strings.Count(string(data), "x") >= 2
	}, 5*time.Second, 10*time.Millisecond, "forward should be restarted after it exits")

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("superviseForward did not stop after ctx cancel")
	}
}

func TestSuperviseForward_StopsImmediatelyIfCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		superviseForward(ctx, forwardSpec{
			execPath: "/bin/sh",
			args:     []string{"-c", "exit 0"},
			backoff:  backoff{min: 10 * time.Millisecond, max: 20 * time.Millisecond},
		}, make(chan struct{}, 1))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("superviseForward should return promptly when ctx is already cancelled")
	}
}

func TestRunForwardOnce_SignalsReadyOnChildWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{}, 1)
	type result struct {
		reported bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		reported, err := runForwardOnce(
			ctx, "/bin/sh", []string{"-c", "printf x >&3; exec 3>&-; exec sleep 5"}, ready,
		)
		done <- result{reported, err}
	}()

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("ready was not signaled after child wrote to its ready fd")
	}

	select {
	case <-done:
		t.Fatal("runForwardOnce returned before cancellation")
	default:
	}

	cancel()

	select {
	case res := <-done:
		assert.True(t, res.reported, "runForwardOnce should report readiness after a real write")
	case <-time.After(2 * time.Second):
		t.Fatal("runForwardOnce did not return after ctx cancel")
	}
}

func TestRunForwardOnce_DoesNotSignalReadyOnChildExitWithoutWriting(t *testing.T) {
	ready := make(chan struct{}, 1)
	reported, err := runForwardOnce(
		context.Background(), "/bin/sh", []string{"-c", "exit 1"}, ready,
	)
	require.Error(t, err)
	assert.False(t, reported, "a child that exits without writing must not be treated as ready")

	select {
	case <-ready:
		t.Fatal("ready must not be signaled when the child never wrote")
	default:
	}
}

func TestSuperviseForward_ReportsReadyOnlyAfterSuccessfulRetry(t *testing.T) {
	runs := filepath.Join(t.TempDir(), "runs")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		superviseForward(ctx, forwardSpec{
			execPath: "/bin/sh",
			args: []string{
				"-c",
				"printf x >> " + runs + "; " +
					"if [ $(wc -c < " + runs + ") -lt 2 ]; then exit 1; fi; " +
					"printf x >&3; exec 3>&-; exec sleep 5",
			},
			backoff: backoff{min: 5 * time.Millisecond, max: 10 * time.Millisecond},
		}, ready)
		close(done)
	}()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("ready was never signaled after a successful retry")
	}

	data, err := os.ReadFile(runs) //nolint:gosec // test path is created by the test
	require.NoError(t, err)
	assert.GreaterOrEqual(t, strings.Count(string(data), "x"), 2,
		"the first (failing) attempt should not have consumed readiness reporting")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("superviseForward did not stop after ctx cancel")
	}
}
