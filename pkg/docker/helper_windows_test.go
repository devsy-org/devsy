//go:build windows

package docker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCmd_WindowsKillsOrphanedGrandchildOnCancel(t *testing.T) {
	tmp := t.TempDir()
	ready := filepath.Join(tmp, "ready")
	childPIDFile := filepath.Join(tmp, "child.pid")
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
sleep 30 &
echo $! > `+childPIDFile+`
touch `+ready+`
wait $!
`)
	h := &DockerHelper{DockerCommand: bin}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(ready); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()

	streams := Streams{Stdout: io.Discard, Stderr: io.Discard}
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx, testExecArgs, streams) }()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal(
			"Run did not return within 5s of cancel; grandchild likely leaked and held the pipe open",
		)
	}

	childPIDRaw, err := os.ReadFile(childPIDFile) //nolint:gosec // G304: test-controlled path
	require.NoError(t, err)
	childPID, err := strconv.Atoi(strings.TrimSpace(string(childPIDRaw)))
	require.NoError(t, err)

	assert.Eventually(t, func() bool {
		proc, err := os.FindProcess(childPID)
		if err != nil {
			return true // Process does not exist
		}
		err = proc.Signal(syscall.Signal(0))
		return err != nil // If error, process is not running
	}, time.Second, 10*time.Millisecond, "grandchild process %d was orphaned instead of killed with the group", childPID)
}
