//go:build !windows

package agentcontainer

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	agentd "github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testRoot = "root"

func managedInfo(timeout string) *provider2.ContainerWorkspaceInfo {
	return &provider2.ContainerWorkspaceInfo{
		ContainerTimeout: timeout,
		LifecycleOwner:   provider2.ContainerLifecycleOwnerManagedDaemon,
	}
}

func publishOwner(t *testing.T) (*agentd.LifecycleOwner, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "lifecycle")
	owner, err := agentd.NewLifecycleOwner(dir)
	require.NoError(t, err)
	return owner, dir
}

func TestBeginLifecycleSuspendsThenReadyArmsSameOwner(t *testing.T) {
	owner, dir := publishOwner(t)

	h, err := beginLifecycle(
		context.Background(), dir, managedInfo("1m"), config.ShutdownActionStopContainer,
	)
	require.NoError(t, err)
	require.NotNil(t, h)

	rec, changed, err := owner.PollControl()
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, agentd.LifecycleStateSetup, rec.State)
	assert.Equal(t, "1m", rec.Timeout)
	assert.Equal(t, config.ShutdownActionStopContainer, rec.ShutdownAction)

	require.NoError(t, h.ready())
	rec, changed, err = owner.PollControl()
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, agentd.LifecycleStateReady, rec.State)
}

func TestLifecycleFailureReleasesSuspension(t *testing.T) {
	owner, dir := publishOwner(t)
	h, err := beginLifecycle(context.Background(), dir, managedInfo("1m"), "")
	require.NoError(t, err)
	_, _, err = owner.PollControl()
	require.NoError(t, err)

	h.fail()

	rec, changed, err := owner.PollControl()
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, agentd.LifecycleStateFailed, rec.State)
}

func TestBeginLifecycleMissingOwnerWithTimeoutIsMigrationError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	h, err := beginLifecycle(ctx, filepath.Join(t.TempDir(), "absent"), managedInfo("1m"), "")

	assert.Nil(t, h)
	require.Error(t, err)
	assert.ErrorIs(t, err, agentd.ErrLifecycleOwnerMissing)
	assert.Contains(t, err.Error(), "stop and start the workspace")
}

func TestBeginLifecycleMissingOwnerWithoutTimeoutIsSkipped(t *testing.T) {
	h, err := beginLifecycle(
		context.Background(), filepath.Join(t.TempDir(), "absent"), managedInfo(""), "",
	)

	assert.NoError(t, err)
	assert.Nil(t, h)
}

func TestBeginLifecycleIgnoresUnmanagedContainers(t *testing.T) {
	h, err := beginLifecycle(
		context.Background(),
		filepath.Join(t.TempDir(), "absent"),
		&provider2.ContainerWorkspaceInfo{ContainerTimeout: "1m"},
		"",
	)

	assert.NoError(t, err)
	assert.Nil(t, h)
}

func TestStartContainerDaemonNeverSpawnsSecondOwnerForManagedContainers(t *testing.T) {
	cmd := &SetupContainerCmd{}

	err := cmd.startContainerDaemon(managedInfo("1m"), config.ShutdownActionStopContainer)

	assert.NoError(t, err)
}

func TestDaemonExitCode(t *testing.T) {
	assert.Equal(t, 0, daemonExitCode(nil))
	assert.Equal(t, 0, daemonExitCode(agentd.ErrInactivityExpired), "expiry is a normal shutdown")
	assert.Equal(t, 0, daemonExitCode(errors.Join(agentd.ErrInactivityExpired)))
	assert.Equal(t, 1, daemonExitCode(errors.New("SSH server exited abnormally")))
}

func TestSshServerArgs(t *testing.T) {
	timeoutOnly := &agentd.DaemonConfig{
		Timeout:   "1m",
		Lifecycle: agentd.LifecycleConfig{Owner: agentd.LifecycleOwnerManagedDaemon},
	}
	platform := &agentd.DaemonConfig{
		Ssh:       agentd.SshConfig{Workdir: "/w", User: testRoot},
		Lifecycle: agentd.LifecycleConfig{Owner: agentd.LifecycleOwnerManagedDaemon},
	}

	cmd := &DaemonCmd{Config: timeoutOnly}
	assert.False(t, cmd.shouldRunSsh(), "a timeout-only config must not start an SSH server")
	assert.False(t, cmd.shouldRunNetworkServer())

	assert.Equal(t,
		[]string{
			"internal", "agent", "container", "ssh-server",
			"--workdir", "/w", "--remote-user", testRoot, "--track-activity",
		},
		sshServerArgs(platform),
		"managed Platform routes record connection activity",
	)
	assert.NotContains(
		t,
		sshServerArgs(&agentd.DaemonConfig{Ssh: platform.Ssh}),
		"--track-activity",
	)
}

func TestLoadConfigCLITimeoutOverridesStoredConfig(t *testing.T) {
	stored, err := agentd.EncodeDaemonConfig(
		&agentd.DaemonConfig{Timeout: "5m", ShutdownAction: "none"},
	)
	require.NoError(t, err)
	t.Setenv("DEVSY_WORKSPACE_DAEMON_CONFIG", stored)
	cmd := &DaemonCmd{Config: &agentd.DaemonConfig{Timeout: "30s"}}

	require.NoError(t, cmd.loadConfig())

	assert.Equal(t, "30s", cmd.Config.Timeout)
	assert.Equal(t, "none", cmd.Config.ShutdownAction)
}

func TestManagedLoopStartsUnarmedUntilSetup(t *testing.T) {
	// The runtime dir is fixed in production, so only the unmanaged branch is
	// observable without root; managed arming is covered by the monitor tests.
	cmd := &DaemonCmd{Config: &agentd.DaemonConfig{}}

	loop, err := cmd.newLifecycleLoop(0)
	assert.NoError(t, err)
	assert.Nil(t, loop, "no timeout and no managed owner means no loop")

	loop, err = cmd.newLifecycleLoop(time.Minute)
	require.NoError(t, err)
	require.NotNil(t, loop)
	assert.Nil(t, loop.Owner)
}

type countingStarter struct{ started, stopped atomic.Int32 }

func (c *countingStarter) start() func() {
	c.started.Add(1)
	return func() { c.stopped.Add(1) }
}

func TestTrackConnectionsCountsConnectionsNotTheListener(t *testing.T) {
	var starter countingStarter
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	l := trackConnections(inner, starter.start)
	defer func() { _ = l.Close() }()

	assert.Zero(t, starter.started.Load(), "an idle listener is not activity")

	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept()
		accepted <- c
	}()
	client, err := net.Dial("tcp", inner.Addr().String())
	require.NoError(t, err)
	server := <-accepted
	require.Eventually(t, func() bool { return starter.started.Load() == 1 },
		time.Second, time.Millisecond)
	assert.Zero(t, starter.stopped.Load(), "activity continues while connected")

	_ = client.Close()
	_, _ = io.Copy(io.Discard, server)
	require.NoError(t, server.Close())
	_ = server.Close()

	assert.Equal(t, int32(1), starter.stopped.Load(), "disconnect ends the activity writer once")
}
