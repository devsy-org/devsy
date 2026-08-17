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
		//nolint:gosec
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

func TestRunCmd_InteractiveStdinKeepsSharedProcessGroup(t *testing.T) {
	tmp := t.TempDir()
	pgidFile := filepath.Join(tmp, "pgid")
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
ps -o pgid= -p $$ | tr -d ' ' > `+pgidFile+`
`)
	h := &DockerHelper{DockerCommand: bin}

	require.NoError(t, h.RunWithDir(context.Background(), "", testExecArgs, Streams{
		Stdin:  os.Stdin,
		Stdout: io.Discard,
		Stderr: io.Discard,
	}), "buildCmd must route this real call site (cmd/workspace/exec.go's interactive "+
		"attach) through RunWithDir, not a hand-built exec.Cmd bypassing it")

	//nolint:gosec // path is test-controlled
	got, err := os.ReadFile(pgidFile)
	require.NoError(t, err)
	gotPgid, err := strconv.Atoi(strings.TrimSpace(string(got)))
	require.NoError(t, err)

	ownPgid, err := syscall.Getpgid(os.Getpid())
	require.NoError(t, err)
	require.Equal(t, ownPgid, gotPgid,
		"a command sharing the controlling terminal via os.Stdin must stay in the "+
			"caller's process group, or job control backgrounds it and its first "+
			"keystroke read earns it a SIGTTIN hang")
}

func TestKillCmd_NoGroupFallsBackToSingleProcess(t *testing.T) {
	bin := writeScript(t, t.TempDir(), "docker-fake", "#!/bin/sh\nexec sleep 30\n")
	//nolint:gosec
	cmd := exec.Command(bin)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	require.NoError(t, cmd.Start())
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	require.Nil(t, cmd.SysProcAttr, "precondition: no process group was requested")
	require.NoError(t, killProcessGroup(cmd))

	select {
	case err := <-waitErr:
		require.Error(t, err, "killProcessGroup must still terminate the single process")
	case <-time.After(5 * time.Second):
		t.Fatal("killProcessGroup did not terminate the single process")
	}
}
