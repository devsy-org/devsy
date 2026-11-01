//go:build darwin

package command

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

const (
	darwinTestPollTimeout  = 5 * time.Second
	darwinTestPollInterval = 10 * time.Millisecond
)

func startGatedGroupLeader(t *testing.T, script string) (pid int, release func(), arg string) {
	t.Helper()
	arg = filepath.Join(t.TempDir(), "ready")
	// #nosec G204 -- the fixed shell script and all paths are test-controlled.
	cmd := exec.Command("sh", "-c", script, "sh", arg)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	pid = cmd.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	return pid, func() { require.NoError(t, stdin.Close()) }, arg
}

func waitForZombie(t *testing.T, pid int) {
	t.Helper()
	require.Eventually(t, func() bool {
		proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		return err == nil && int(proc.Proc.P_pid) == pid && darwinProcessIsZombie(*proc)
	}, darwinTestPollTimeout, darwinTestPollInterval, "process %d did not become a zombie", pid)
}

func startZombieGroupLeader(t *testing.T) (pid int, identity string) {
	t.Helper()
	pid, release, _ := startGatedGroupLeader(t, `read _`)
	identity = processTreeIdentityForTest(t, pid)
	release()
	waitForZombie(t, pid)
	return pid, identity
}

func TestDarwinZombieLeaderIsNotLiveGroupMember(t *testing.T) {
	pid, identity := startZombieGroupLeader(t)

	live, err := processIsLive(pid)
	require.NoError(t, err)
	assert.False(t, live, "zombie leader reported live")

	living, err := processGroupHasLivingMembers(pid)
	require.NoError(t, err)
	assert.False(t, living, "zombie-only group reported living members")

	matches, err := processGroupMatchesIdentity(pid, identity)
	require.NoError(t, err)
	assert.False(t, matches, "zombie-only group matched saved identity")
}

func TestDarwinKillWorkerTreeOnZombieOnlyGroupSucceeds(t *testing.T) {
	pid, identity := startZombieGroupLeader(t)

	require.NoError(t, killWorkerTreeWithIdentity(pid, "detached-task", identity))
	waitForZombie(t, pid)
}

func TestDarwinZombieLeaderWithLiveChildKeepsGroupMatched(t *testing.T) {
	pid, release, readyFile := startGatedGroupLeader(t, `sleep 30 & echo $! > "$1"; read _`)
	childPID := waitForChildPID(t, readyFile)
	childGroup, err := unix.Getpgid(childPID)
	require.NoError(t, err)
	require.Equal(t, pid, childGroup, "child must share the leader's process group")
	identity := processTreeIdentityForTest(t, pid)

	release()
	waitForZombie(t, pid)
	childLive, err := processIsLive(childPID)
	require.NoError(t, err)
	require.True(t, childLive, "child must outlive the zombie leader")

	matches, err := processGroupMatchesIdentity(pid, identity)
	require.NoError(t, err)
	assert.True(t, matches, "live child must keep the group matched")
	living, err := processGroupHasLivingMembers(pid)
	require.NoError(t, err)
	assert.True(t, living, "live child must count as a living member")

	require.NoError(t, killWorkerTreeWithIdentity(pid, "detached-task", identity))
	require.Eventually(t, func() bool {
		live, err := processIsLive(childPID)
		return err == nil && !live
	}, darwinTestPollTimeout, darwinTestPollInterval, "child %d survived termination", childPID)
}

func TestDarwinZombieLeaderWithLiveChildRejectsStaleIdentity(t *testing.T) {
	pid, release, readyFile := startGatedGroupLeader(t, `sleep 30 & echo $! > "$1"; read _`)
	childPID := waitForChildPID(t, readyFile)
	identity := processTreeIdentityForTest(t, pid)
	stale := identity[:strings.LastIndex(identity, ":")] + ":0"

	release()
	waitForZombie(t, pid)

	matches, err := processGroupMatchesIdentity(pid, stale)
	require.NoError(t, err)
	assert.False(t, matches, "stale identity matched a group")
	require.NoError(t, killWorkerTreeWithIdentity(pid, "detached-task", stale))
	childLive, err := processIsLive(childPID)
	require.NoError(t, err)
	assert.True(t, childLive, "stale identity must not terminate the child")
}

func waitForChildPID(t *testing.T, readyFile string) int {
	t.Helper()
	var childPID int
	require.Eventually(t, func() bool {
		// #nosec G304 -- this test reads only its own temporary readiness file.
		data, err := os.ReadFile(readyFile)
		if err != nil || !strings.HasSuffix(string(data), "\n") {
			return false
		}
		childPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil
	}, darwinTestPollTimeout, darwinTestPollInterval, "child did not report its PID")
	return childPID
}

func TestDarwinProcessGroupHasLivingMembersSeesLiveChild(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	living, err := processGroupHasLivingMembers(cmd.Process.Pid)
	require.NoError(t, err)
	assert.True(t, living)
}
