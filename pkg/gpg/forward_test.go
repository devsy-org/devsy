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
	done := make(chan error, 1)
	go func() {
		done <- runForwardOnce(ctx, "/bin/sh", []string{"-c", "printf x >&3; exec 3>&-; exec sleep 5"}, ready)
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
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runForwardOnce did not return after ctx cancel")
	}
}

func TestRunForwardOnce_SignalsReadyOnChildExitWithoutWriting(t *testing.T) {
	ready := make(chan struct{}, 1)
	err := runForwardOnce(context.Background(), "/bin/sh", []string{"-c", "exit 1"}, ready)
	require.Error(t, err)

	require.Eventually(t, func() bool {
		select {
		case <-ready:
			return true
		default:
			return false
		}
	}, time.Second, 5*time.Millisecond, "ready should be signaled even though the child never wrote")
}
