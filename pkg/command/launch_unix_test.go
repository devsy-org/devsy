//go:build linux || darwin || unix

package command

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestLaunchLeaseSurvivesLauncherCloseUntilChildExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lease")
	lease, err := NewLaunchLease(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lease.Close() })
	child := exec.Command(
		os.Args[0],
		"-test.run=^TestLaunchLeaseHelper$",
	) // #nosec G204 G702 -- re-exec this test binary
	child.Env = append(os.Environ(), "DEVSY_TEST_LAUNCH_LEASE=1")
	lease.attach(child)
	input, err := child.StdinPipe()
	require.NoError(t, err)
	output, err := child.StdoutPipe()
	require.NoError(t, err)
	child.Stderr = os.Stderr
	require.NoError(t, child.Start())
	t.Cleanup(func() { _ = input.Close(); _ = child.Process.Kill() })
	ready := make(chan bool, 1)
	go func() { scanner := bufio.NewScanner(output); ready <- scanner.Scan() && scanner.Text() == "ready" }()
	select {
	case ok := <-ready:
		require.True(t, ok)
	case <-time.After(5 * time.Second):
		t.Fatal("child did not retain the launch lease")
	}
	require.NoError(t, lease.Close())
	require.Error(
		t,
		CleanupUnpublishedLaunch(path, "", true),
		"the unidentified child must retain positive lifetime evidence",
	)
	require.NoError(t, input.Close())
	require.NoError(t, child.Wait())
	require.NoError(t, CleanupUnpublishedLaunch(path, "", true))
}

func TestLaunchLeaseHelper(t *testing.T) {
	if os.Getenv("DEVSY_TEST_LAUNCH_LEASE") == "" {
		return
	}
	fd, err := strconv.Atoi(os.Getenv(launchLeaseEnv))
	require.NoError(t, err)
	require.NoError(t, RetainLaunchLease())
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	require.NoError(t, err)
	require.NotZero(t, flags&unix.FD_CLOEXEC)
	require.Empty(t, os.Getenv(launchLeaseEnv))
	_, err = fmt.Fprintln(os.Stdout, "ready")
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, os.Stdin)
	require.NoError(t, err)
}
