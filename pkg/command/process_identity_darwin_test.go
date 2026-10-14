//go:build darwin

package command

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func startZombieGroupLeader(t *testing.T) (pid int, identity string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", "read _")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	pid = cmd.Process.Pid
	identity = processTreeIdentityForTest(t, pid)

	require.NoError(t, stdin.Close())
	require.Eventually(t, func() bool {
		proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		return err == nil && int(proc.Proc.P_pid) == pid && darwinProcessIsZombie(*proc)
	}, 5*time.Second, 10*time.Millisecond, "leader did not become a zombie")
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

	require.ErrorIs(t, syscall.Kill(-pid, syscall.SIGTERM), syscall.EPERM)
	require.NoError(t, killWorkerTreeWithIdentity(pid, "detached-task", identity))
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
