//go:build linux

package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type inheritedPeerReport struct {
	PID       int `json:"pid"`
	HolderPID int `json:"holderPid"`
}

func TestInheritedSocketCannotRetainActivityAfterOriginalProcessDies(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires an actual unprivileged peer and inherited descriptor")
	}
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	monitor.ArmNow(time.Minute, time.Now())
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	binary, err := os.Executable()
	require.NoError(t, err)
	// #nosec G204 -- re-execute the current test binary with fixed test selection.
	cmd := exec.Command(binary, "-test.run=^TestInheritedSocketParentChild$")
	cmd.Env = append(os.Environ(), "DEVSY_INHERITED_SOCKET_DIR="+dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: 65534, Gid: 65534, Groups: []uint32{65534}},
		Pdeathsig:  syscall.SIGKILL,
	}
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	defer func() { _ = input.Close() }()
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	var report inheritedPeerReport
	require.NoError(t, json.Unmarshal([]byte(readPeerLine(t, bufio.NewScanner(stdout))), &report))
	require.Equal(t, cmd.Process.Pid, report.PID)
	t.Cleanup(func() {
		_ = syscall.Kill(report.HolderPID, syscall.SIGKILL)
		_, _ = syscall.Wait4(report.HolderPID, nil, 0, nil)
	})
	assert.False(t, idleForAnHour(monitor).Expired)
	require.NoError(t, cmd.Process.Kill())
	require.Eventually(
		t,
		func() bool { return isUnreapedZombie(report.PID) },
		time.Second,
		time.Millisecond,
		"the opener must be dead but still owned by this living parent",
	)
	assertZombieReleaseBeforeReap(t, monitor, dir, report)
	require.Error(t, cmd.Wait())
}

func assertZombieReleaseBeforeReap(
	t *testing.T,
	monitor *LifecycleMonitor,
	dir string,
	report inheritedPeerReport,
) {
	t.Helper()
	require.True(
		t,
		processExists(report.HolderPID),
		"the inherited socket holder must remain alive",
	)
	require.Eventually(
		t,
		func() bool { return idleForAnHour(monitor).Expired },
		2*time.Second,
		time.Millisecond,
	)
	require.True(
		t,
		processExists(report.HolderPID),
		"EOF from holder exit must not explain release",
	)
	assertReclaimedUIDBudget(t, dir)
	require.True(
		t,
		isUnreapedZombie(report.PID),
		"release and capacity recovery must precede reaping",
	)
}

func assertReclaimedUIDBudget(t *testing.T, dir string) {
	t.Helper()
	first := startCapacityPeer(t, dir, 65534, "activity")
	second := startCapacityPeer(t, dir, 65534, "activity")
	third := startCapacityPeer(t, dir, 65534, "activity")
	require.Equal(t, maxLifecyclePIDClients, first.report.Live)
	require.Equal(t, maxLifecyclePIDClients, second.report.Live)
	require.Zero(t, third.report.Live, "prune followed by EOF must not decrement UID usage twice")
	first.close(t)
	second.close(t)
	third.close(t)
}

func TestInheritedSocketParentChild(t *testing.T) {
	dir := os.Getenv("DEVSY_INHERITED_SOCKET_DIR")
	if dir == "" {
		return
	}
	client, err := ConnectLifecycle(context.Background(), dir)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	require.NoError(t, client.Send(LifecycleStateActivity, "", ""))
	file, err := client.conn.(*net.UnixConn).File()
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	binary, err := os.Executable()
	require.NoError(t, err)
	// #nosec G204 -- re-execute the current test binary with fixed test selection.
	holder := exec.Command(binary, "-test.run=^TestInheritedSocketHolderChild$")
	holder.Env = append(os.Environ(), "DEVSY_INHERITED_SOCKET_HOLDER=1")
	holder.ExtraFiles = []*os.File{file}
	stdout, err := holder.StdoutPipe()
	require.NoError(t, err)
	holder.Stderr = os.Stderr
	require.NoError(t, holder.Start())
	defer func() { _ = holder.Process.Kill(); _ = holder.Wait() }()
	require.Equal(t, "holding", readPeerLine(t, bufio.NewScanner(stdout)))
	require.NoError(
		t,
		json.NewEncoder(os.Stdout).
			Encode(inheritedPeerReport{PID: os.Getpid(), HolderPID: holder.Process.Pid}),
	)
	_, err = bufio.NewReader(os.Stdin).ReadString('\n')
	require.NoError(t, err)
}

func TestInheritedSocketHolderChild(t *testing.T) {
	if os.Getenv("DEVSY_INHERITED_SOCKET_HOLDER") == "" {
		return
	}
	file := os.NewFile(3, "inherited-lifecycle-socket")
	require.NotNil(t, file)
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	require.NoError(t, err)
	require.Equal(t, os.ModeSocket, info.Mode().Type())
	_, err = os.Stdout.WriteString("holding\n")
	require.NoError(t, err)
	time.Sleep(time.Minute)
}

func TestProcessStartIdentityRejectsReusedPID(t *testing.T) {
	ticks, err := processStartIdentity(os.Getpid())
	require.NoError(t, err)
	require.NotEmpty(t, ticks)
	assert.True(t, sameProcess(os.Getpid(), ticks))
	assert.False(
		t,
		sameProcess(os.Getpid(), ""),
		"a missing start identity must not reduce to PID liveness",
	)
	assert.False(
		t,
		sameProcess(os.Getpid(), ticks+"0"),
		"a live PID does not validate another process generation",
	)
}

func TestLinuxLifecycleFailsClosedWithoutReadableProcIdentity(t *testing.T) {
	if os.Getenv("DEVSY_MASKED_PROC_TEST") == "" {
		t.Skip("run in the isolated container with /proc masked")
	}
	require.Empty(t, procStartTicks(os.Getpid()))
	assert.False(t, sameProcess(os.Getpid(), "previous-process-generation"))
	dir, err := os.MkdirTemp("/tmp", "ds-proc-")
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(dir) }()
	owner, err := NewLifecycleOwner(dir)
	if owner != nil {
		_ = owner.Close()
	}
	assert.ErrorIs(
		t,
		err,
		ErrLifecycleOwnerMissing,
		"unverifiable process identity must not start an owner",
	)
}

func isUnreapedZombie(pid int) bool {
	raw, err := os.ReadFile(
		"/proc/" + strconv.Itoa(pid) + "/status",
	) // #nosec G304 -- actual test child PID
	return err == nil && bytes.Contains(raw, []byte("\nState:\tZ"))
}
