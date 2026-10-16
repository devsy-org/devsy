//go:build windows

package docker

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

var procIsProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

func processInJob(t *testing.T, pid int, job windows.Handle) bool {
	t.Helper()
	h, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(pid), //nolint:gosec // pid fits uint32
	)
	require.NoError(t, err, "open pid %d", pid)
	defer func() { _ = windows.CloseHandle(h) }()

	var result int32
	ret, _, callErr := procIsProcessInJob.Call(
		uintptr(h),
		uintptr(job),
		uintptr(unsafe.Pointer(&result)), //nolint:gosec // Win32 out parameter
	)
	require.NotZero(t, ret, "IsProcessInJob: %v", callErr)
	return result != 0
}

// readDescendantPID reads the pid line printed by the "spawn-child" helper.
// Anonymous pipes have no read deadline, so the read is bounded by a goroutine.
func readDescendantPID(t *testing.T, r io.Reader) int {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	read := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		read <- result{line, err}
	}()
	select {
	case res := <-read:
		require.NoError(t, res.err)
		pid, err := strconv.Atoi(strings.TrimSpace(res.line))
		require.NoError(t, err)
		return pid
	case <-time.After(30 * time.Second):
		require.FailNow(t, "descendant pid was not reported")
		return 0
	}
}

// TestLaunchPutsChildAndImmediateDescendantInJob exercises the pre-assignment
// race window natively: the child starts its own child before doing anything
// else, so only creation-time job membership can own the descendant.
func TestLaunchPutsChildAndImmediateDescendantInJob(t *testing.T) {
	launched, err := launchDialStdio(helperInvocation(t, "spawn-child").launchSpec())
	require.NoError(t, err)
	proc, ok := launched.proc.(*jobProcess)
	require.True(t, ok)
	released := false
	t.Cleanup(func() {
		if !released {
			_ = proc.KillTree()
			_ = proc.Wait()
			_ = proc.Release()
		}
		closeAll(launched.stdin, launched.stdout, launched.stderr)
	})

	descendant := readDescendantPID(t, launched.stdout)
	require.True(t, processRunning(proc.PID()))
	require.True(t, processRunning(descendant))
	require.True(t, processInJob(t, proc.PID(), proc.job), "child must be in its job")
	require.True(t, processInJob(t, descendant, proc.job), "descendant must inherit the job")

	require.NoError(t, proc.KillTree())
	require.NoError(t, proc.KillTree(), "KillTree is idempotent")
	require.Error(t, proc.Wait(), "terminated child reports a non-zero exit")
	require.NoError(t, proc.Release())
	released = true
	require.NoError(t, proc.Release(), "Release is idempotent")
	require.NoError(t, proc.KillTree(), "KillTree after Release is a no-op")

	require.Eventually(t, func() bool { return !processRunning(descendant) },
		5*time.Second, 10*time.Millisecond, "descendant survived job termination")
}

func TestLaunchRejectsBatchScript(t *testing.T) {
	script := filepath.Join(t.TempDir(), "docker.cmd")
	require.NoError(t, os.WriteFile(script, []byte("@echo off\r\n"), 0o600))

	_, err := launchDialStdio(launchSpec{path: script, args: []string{"system", "dial-stdio"}})
	require.ErrorContains(t, err, "batch script")
}

func TestLaunchExitCode259DoesNotMeanRunning(t *testing.T) {
	launched, err := launchDialStdio(helperInvocation(t, helperHangMode).launchSpec())
	require.NoError(t, err)
	proc, ok := launched.proc.(*jobProcess)
	require.True(t, ok)
	t.Cleanup(func() {
		_ = proc.KillTree()
		_ = proc.Release()
		closeAll(launched.stdin, launched.stdout, launched.stderr)
	})

	require.True(t, processRunning(proc.PID()))
	require.NoError(t, windows.TerminateJobObject(proc.job, 259))
	event, err := windows.WaitForSingleObject(proc.proc, 5000)
	require.NoError(t, err)
	require.Equal(t, uint32(windows.WAIT_OBJECT_0), event)
	require.ErrorIs(t, proc.Wait(), exitStatusError(259))
	// The launcher retains its handles, keeping the exited PID available.
	require.False(t, processRunning(proc.PID()))
}

func TestLaunchMissingExecutableFailsWithoutProcess(t *testing.T) {
	_, err := launchDialStdio(launchSpec{path: "devsy-no-such-docker-binary"})
	require.Error(t, err)
}

func TestLaunchRejectsNULInEnvironmentWithoutLeakingIt(t *testing.T) {
	spec := helperInvocation(t, "echo").launchSpec()
	spec.env = append(spec.env, "SECRET_TOKEN=hun\x00ter2")

	_, err := launchDialStdio(spec)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "hun")
}

func TestEnvBlockIsSortedAndDoubleNULTerminated(t *testing.T) {
	block, err := buildEnvBlock([]string{"b=2", "=C:=C:\\x", "A=1"})
	require.NoError(t, err)
	require.Equal(t, "=C:=C:\\x\x00A=1\x00b=2\x00\x00", string(utf16.Decode(block)))
}

func TestEnvBlockEmptyIsDoubleNUL(t *testing.T) {
	block, err := buildEnvBlock(nil)
	require.NoError(t, err)
	require.Equal(t, []uint16{0, 0}, block)
}
