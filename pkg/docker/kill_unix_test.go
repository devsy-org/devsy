//go:build !windows

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

	"github.com/stretchr/testify/require"
)

// TestRunCmd_CancelKillsProcessGroup guards against a real regression: a
// privilege-elevation helper (sudo, pkexec, doas) forwards signals it can
// catch to the command it launches, but cannot forward SIGKILL because it
// cannot catch it. Killing only the helper's PID would leave its child
// (e.g. the actual podman process) running and orphaned, holding runCmd's
// pipes open forever. This test spawns a background grandchild (standing in
// for that child process) and asserts cancellation reaps it too.
func TestRunCmd_CancelKillsProcessGroup(t *testing.T) {
	tmp := t.TempDir()
	ready := filepath.Join(tmp, "ready")
	pidFile := filepath.Join(tmp, "child.pid")
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
sleep 30 &
echo $! > `+pidFile+`
touch `+ready+`
wait
`)
	h := &DockerHelper{DockerCommand: bin}

	ctx, cancel := context.WithCancel(context.Background())
	var childPID int
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(ready); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		//nolint:gosec // test reads from a temp directory we control
		pidBytes, err := os.ReadFile(pidFile)
		if err == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(pidBytes)))
		}
		cancel()
	}()

	streams := Streams{Stdout: io.Discard, Stderr: io.Discard}
	start := time.Now()
	_ = h.Run(ctx, testExecArgs, streams)
	elapsed := time.Since(start)

	require.NotZero(t, childPID, "grandchild PID must have been captured before cancellation")
	require.Less(t, elapsed, 5*time.Second,
		"Run must return once its process group is killed, not block on an orphaned "+
			"grandchild still holding the output pipes open")
	require.Eventually(t, func() bool {
		return syscall.Kill(childPID, syscall.Signal(0)) != nil
	}, 5*time.Second, 10*time.Millisecond,
		"grandchild process %d must be killed along with its process group", childPID)
}
