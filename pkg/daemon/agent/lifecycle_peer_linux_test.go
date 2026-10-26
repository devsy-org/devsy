//go:build linux

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type lifecyclePeerIdentity struct {
	UID    int   `json:"uid"`
	GID    int   `json:"gid"`
	Groups []int `json:"groups"`
	PID    int   `json:"pid"`
}

func TestNonrootLifecyclePeerChild(t *testing.T) {
	dir := os.Getenv("DEVSY_NONROOT_LIFECYCLE_CHILD")
	if dir == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := ConnectLifecycle(ctx, dir)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	require.NoError(t, client.Send(LifecycleStateActivity, "", ""))
	groups, err := os.Getgroups()
	require.NoError(t, err)
	require.NoError(t, json.NewEncoder(os.Stdout).Encode(lifecyclePeerIdentity{
		UID: os.Getuid(), GID: os.Getgid(), Groups: groups, PID: os.Getpid(),
	}))
	_, err = bufio.NewReader(os.Stdin).ReadString('\n')
	require.NoError(t, err)
	require.Error(t, client.Send(LifecycleStateReady, "0", "none"))
	_, err = os.Stdout.WriteString("rejected\n")
	require.NoError(t, err)
}

func readPeerLine(t *testing.T, scanner *bufio.Scanner) string {
	t.Helper()
	line := make(chan string, 1)
	go func() { scanner.Scan(); line <- scanner.Text() }()
	select {
	case got := <-line:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("nonroot peer did not report its state")
		return ""
	}
}

func TestRootOwnerAllowsNonrootActivityButRejectsControl(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires a root owner to exercise the actual cross-UID boundary")
	}
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	monitor.ArmNow(time.Minute, time.Now())
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	binary, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(
		binary,
		"-test.run=^TestNonrootLifecyclePeerChild$",
	) // #nosec G204 -- current test binary
	cmd.Env = append(os.Environ(), "DEVSY_NONROOT_LIFECYCLE_CHILD="+dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: 65534, Gid: 65534, Groups: []uint32{65534}},
		Pdeathsig:  syscall.SIGKILL,
	}
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	scanner := bufio.NewScanner(stdout)
	var identity lifecyclePeerIdentity
	require.NoError(t, json.Unmarshal([]byte(readPeerLine(t, scanner)), &identity))
	assert.Equal(t, 65534, identity.UID)
	assert.Equal(t, 65534, identity.GID)
	assert.Equal(t, []int{65534}, identity.Groups)
	assert.Equal(t, cmd.Process.Pid, identity.PID)
	assert.False(
		t,
		idleForAnHour(monitor).Expired,
		"actual nonroot process holds activity under a root owner",
	)

	assertNonrootControlReleasesCapacity(t, dir, func() {
		_, err = stdin.Write([]byte("control\n"))
		require.NoError(t, err)
		require.Equal(t, "rejected", readPeerLine(t, scanner))
		require.NoError(t, cmd.Wait())
	})

	require.Eventually(
		t,
		func() bool { return idleForAnHour(monitor).Expired },
		time.Second,
		time.Millisecond,
		"nonowner control cannot disable the timer or leave a permanent hold",
	)
}

func assertNonrootControlReleasesCapacity(t *testing.T, dir string, reject func()) {
	t.Helper()
	for range maxLifecycleClients - 1 {
		client := connectNow(t, dir)
		require.NoError(t, client.Send(LifecycleStateActivity, "", ""))
		defer func() { _ = client.Close() }()
	}
	writer := ActivityWriter{LifecycleDir: dir, RequireOwner: true}
	_, _, err := writer.Start(context.Background())
	require.Error(t, err, "a full owner must refuse untracked work")
	reject()
	require.Eventually(t, func() bool {
		_, stop, err := writer.Start(context.Background())
		if err != nil {
			return false
		}
		stop()
		return true
	}, time.Second, time.Millisecond, "nonroot control rejection must return client capacity")
}
