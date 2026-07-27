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

func TestSuperviseForward_RestartsUntilCancelled(t *testing.T) {
	oldMin, oldMax := forwardRestartMinBackoff, forwardRestartMaxBackoff
	forwardRestartMinBackoff, forwardRestartMaxBackoff = 10*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { forwardRestartMinBackoff, forwardRestartMaxBackoff = oldMin, oldMax })

	runs := filepath.Join(t.TempDir(), "runs")
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		superviseForward(ctx, "/bin/sh", []string{"-c", "printf x >> " + runs})
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
		superviseForward(ctx, "/bin/sh", []string{"-c", "exit 0"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("superviseForward should return promptly when ctx is already cancelled")
	}
}
