//go:build !windows

package docker

import (
	"context"
	"io"
	"os"
	"os/exec"
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

// TestRunCmd_InteractiveStdinKeepsSharedProcessGroup guards the other half of
// the tradeoff runCmd makes: cmd.Stdin == os.Stdin means an interactive
// session (e.g. `devsy exec`) that relies on sharing devsy's own process
// group so the terminal's Ctrl+C reaches it directly. Isolating it into its
// own group (as TestRunCmd_CancelKillsProcessGroup validates for every other
// case) would silently break that.
func TestRunCmd_InteractiveStdinKeepsSharedProcessGroup(t *testing.T) {
	bin := writeScript(t, t.TempDir(), "docker-fake", "#!/bin/sh\nexit 0\n")
	//nolint:gosec // test script path we just wrote to a temp directory we control
	cmd := exec.CommandContext(context.Background(), bin)
	cmd.Stdin = os.Stdin
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	require.NoError(t, runCmd(context.Background(), cmd))

	require.False(t, cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid,
		"an interactive command sharing os.Stdin must not be moved into its own process group")
}

// TestKillCmd_NoGroupFallsBackToSingleProcess guards the killCmd side of the
// same tradeoff: when a command was never given its own process group (the
// interactive case), killCmd must never attempt a process-group signal,
// because that group is still devsy's own and killing it would kill devsy.
func TestKillCmd_NoGroupFallsBackToSingleProcess(t *testing.T) {
	bin := writeScript(t, t.TempDir(), "docker-fake", "#!/bin/sh\nexec sleep 30\n")
	//nolint:gosec // test script path we just wrote to a temp directory we control
	cmd := exec.Command(bin)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	require.NoError(t, cmd.Start())
	go func() { _ = cmd.Wait() }() // reap so the liveness check below isn't fooled by a zombie

	require.Nil(t, cmd.SysProcAttr, "precondition: no process group was requested")
	require.NoError(t, killCmd(cmd))

	require.Eventually(t, func() bool {
		return syscall.Kill(cmd.Process.Pid, syscall.Signal(0)) != nil
	}, 5*time.Second, 10*time.Millisecond, "killCmd must still terminate the single process")
}
