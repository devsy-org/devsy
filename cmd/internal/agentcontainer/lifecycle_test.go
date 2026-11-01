//go:build !windows

package agentcontainer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentd "github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	devsyssh "github.com/devsy-org/ssh"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
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
	dir, err := os.MkdirTemp("/tmp", "devsy-lifecycle-cmd-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	owner, err := agentd.NewLifecycleOwner(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = owner.Close() })
	return owner, dir
}

func runLifecycleOwner(
	t *testing.T,
	owner *agentd.LifecycleOwner,
	dir string,
) *agentd.LifecycleMonitor {
	t.Helper()
	monitor := &agentd.LifecycleMonitor{}
	loop := &agentd.LifecycleLoop{
		Owner: owner, Monitor: monitor, ActivityPath: filepath.Join(dir, "activity"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			assert.ErrorIs(t, err, agentd.ErrLifecycleOwnerMissing)
		}
	})
	return monitor
}

func TestBeginLifecycleSuspendsThenReadyArmsSameOwner(t *testing.T) {
	owner, dir := publishOwner(t)
	monitor := runLifecycleOwner(t, owner, dir)
	monitor.ArmNow(time.Minute, time.Now())

	h, err := beginLifecycle(
		context.Background(), dir, managedInfo("1m"), config.ShutdownActionStopContainer,
	)
	require.NoError(t, err)
	require.NotNil(t, h)
	defer h.close()
	assert.False(t, monitor.Evaluate(time.Now().Add(time.Hour), time.Time{}, nil).Expired,
		"setup is suspended before beginLifecycle returns")

	require.NoError(t, h.ready())

	assert.True(t, monitor.Evaluate(time.Now().Add(time.Hour), time.Time{}, nil).Expired,
		"ready has re-armed the timer before returning")
}

func TestLifecycleFailureReleasesSuspension(t *testing.T) {
	owner, dir := publishOwner(t)
	monitor := runLifecycleOwner(t, owner, dir)
	h, err := beginLifecycle(context.Background(), dir, managedInfo("1m"), "")
	require.NoError(t, err)
	defer h.close()
	assert.False(t, monitor.Evaluate(time.Now().Add(time.Hour), time.Time{}, nil).Expired)

	h.fail()

	assert.True(t, monitor.Evaluate(time.Now().Add(time.Hour), time.Time{}, nil).Expired,
		"failed setup releases the suspension")
}

func TestBeginLifecycleMissingOwnerWithTimeoutIsMigrationError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	h, err := beginLifecycle(ctx, filepath.Join(t.TempDir(), "absent"), managedInfo("1m"), "")

	assert.Nil(t, h)
	require.Error(t, err)
	assert.ErrorIs(t, err, agentd.ErrLifecycleOwnerMissing)
}

func TestBeginLifecycleRequiresManagedOwnerEvenWithoutTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	h, err := beginLifecycle(ctx, filepath.Join(t.TempDir(), "absent"), managedInfo(""), "")

	assert.ErrorIs(t, err, agentd.ErrLifecycleOwnerMissing)
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

func (c *countingStarter) start() (context.Context, func(), error) {
	c.started.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	return ctx, func() { cancel(); c.stopped.Add(1) }, nil
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

func TestSetupConnectionLossCancelsProtectedWork(t *testing.T) {
	owner, dir := publishOwner(t)
	monitor := runLifecycleOwner(t, owner, dir)
	h, err := beginLifecycle(context.Background(), dir, managedInfo("1m"), "")
	require.NoError(t, err)
	defer h.close()

	require.NoError(t, owner.Close())

	select {
	case <-h.ctx.Done():
		assert.ErrorIs(t, context.Cause(h.ctx), agentd.ErrLifecycleOwnerMissing)
	case <-time.After(time.Second):
		t.Fatal("setup continued after its protection connection was lost")
	}
	require.Eventually(t, func() bool {
		return monitor.Evaluate(time.Now().Add(time.Hour), time.Time{}, nil).Expired
	}, time.Second, time.Millisecond)
}

func TestBeginLifecycleRequiresAcknowledgementBeforeReturningSetup(t *testing.T) {
	_, dir := publishOwner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	h, err := beginLifecycle(ctx, dir, managedInfo("1m"), "")

	assert.ErrorIs(t, err, agentd.ErrLifecycleOwnerMissing)
	assert.Nil(t, h)
}

func TestTrackingLossClosesTheSSHConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	listener := trackConnections(inner, func() (context.Context, func(), error) {
		return ctx, cancel, nil
	})
	defer func() { _ = listener.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() { conn, _ := listener.Accept(); accepted <- conn }()
	client, err := net.Dial("tcp", inner.Addr().String())
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	server := <-accepted
	require.NotNil(t, server)
	defer func() { _ = server.Close() }()

	cancel()

	require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
	var buf [1]byte
	_, err = client.Read(buf[:])
	assert.ErrorIs(t, err, io.EOF, "SSH must not remain open without lifecycle tracking")
}

func TestSSHServerRejectsOnlyUnregisteredConnectionAndKeepsServing(t *testing.T) {
	var attempts atomic.Int32
	server, listener, done := newTrackedTestSSHServer(t, func() (context.Context, func(), error) {
		if attempts.Add(1) == 2 {
			return nil, nil, errors.New("activity capacity unavailable")
		}
		ctx, cancel := context.WithCancel(context.Background())
		return ctx, cancel, nil
	})
	address := listener.Addr().String()
	key := server.HostSigners[0].PublicKey()
	existing, err := dialTrackedTestSSH(address, key)
	require.NoError(t, err)
	defer func() { _ = existing.Close() }()

	denied, err := dialTrackedTestSSH(address, key)
	if denied != nil {
		_ = denied.Close()
	}
	require.Error(t, err, "a connection without registered activity must not reach SSH")
	assertTrackedSSHCommand(t, existing)
	later, err := dialTrackedTestSSH(address, key)
	require.NoError(t, err, "one rejected admission must not terminate SSH serving")
	defer func() { _ = later.Close() }()
	assertTrackedSSHCommand(t, later)

	require.NoError(t, listener.Close())
	select {
	case err := <-done:
		assert.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(time.Second):
		t.Fatal("SSH serving did not terminate after the underlying listener closed")
	}
}

func newTrackedTestSSHServer(
	t *testing.T,
	start func() (context.Context, func(), error),
) (*devsyssh.Server, net.Listener, <-chan error) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := gossh.NewSignerFromKey(privateKey)
	require.NoError(t, err)
	server := &devsyssh.Server{Handler: func(session devsyssh.Session) {
		_, _ = io.WriteString(session, "registered\n")
	}}
	server.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Close(); _ = listener.Close() })
	done := make(chan error, 1)
	go func() { done <- server.Serve(trackConnections(listener, start)) }()
	return server, listener, done
}

func dialTrackedTestSSH(address string, key gossh.PublicKey) (*gossh.Client, error) {
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	client, channels, requests, err := gossh.NewClientConn(conn, address, &gossh.ClientConfig{
		User: "workspace", HostKeyCallback: gossh.FixedHostKey(key),
	})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return gossh.NewClient(client, channels, requests), nil
}

func assertTrackedSSHCommand(t *testing.T, client *gossh.Client) {
	t.Helper()
	session, err := client.NewSession()
	require.NoError(t, err)
	defer func() { _ = session.Close() }()
	out, err := session.Output("check")
	require.NoError(t, err)
	assert.Equal(t, "registered\n", string(out))
}

func TestEmbeddedSSHActivitySaturationPreservesControlAndExistingSessions(t *testing.T) {
	owner, dir := publishOwner(t)
	runLifecycleOwner(t, owner, dir)
	writer := agentd.ActivityWriter{LifecycleDir: dir, RequireOwner: true}
	admissions := make(chan error, 1)
	server, listener, done := newTrackedTestSSHServer(t, func() (context.Context, func(), error) {
		ctx, stop, err := writer.Start(context.Background())
		admissions <- err
		return ctx, stop, err
	})
	address, key := listener.Addr().String(), server.HostSigners[0].PublicKey()
	existing, err := dialTrackedTestSSH(address, key)
	require.NoError(t, err)
	defer func() { _ = existing.Close() }()
	require.NoError(t, nextSSHAdmission(t, admissions))
	raw := saturateEmbeddedSSHActivity(t, address, admissions)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	control, err := agentd.ConnectLifecycle(ctx, dir)
	require.NoError(
		t,
		err,
		"owner-UID network activity must leave actual control admission available",
	)
	defer func() { _ = control.Close() }()
	require.NoError(t, control.Send(agentd.LifecycleStateSetup, "1m", ""))
	require.NoError(t, control.Send(agentd.LifecycleStateReady, "1m", ""))
	assertTrackedSSHCommand(t, existing)
	require.NoError(t, raw[0].Close())
	var later *gossh.Client
	require.Eventually(t, func() bool {
		client, err := dialTrackedTestSSH(address, key)
		admission := nextSSHAdmission(t, admissions)
		if err != nil {
			return false
		}
		assert.NoError(t, admission)
		later = client
		return true
	}, 3*time.Second, 10*time.Millisecond)
	defer func() { _ = later.Close() }()
	assertTrackedSSHCommand(t, later)
	require.NoError(t, listener.Close())
	select {
	case err := <-done:
		assert.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(time.Second):
		t.Fatal("a real listener close must remain terminal")
	}
}

func nextSSHAdmission(t *testing.T, admissions <-chan error) error {
	t.Helper()
	select {
	case err := <-admissions:
		return err
	case <-time.After(time.Second):
		t.Fatal("SSH stopped accepting instead of reporting activity admission")
		return nil
	}
}

func saturateEmbeddedSSHActivity(t *testing.T, address string, admissions <-chan error) []net.Conn {
	t.Helper()
	var accepted []net.Conn
	t.Cleanup(func() {
		for _, conn := range accepted {
			_ = conn.Close()
		}
	})
	for range 129 {
		conn, err := net.DialTimeout("tcp", address, time.Second)
		require.NoError(t, err)
		if nextSSHAdmission(t, admissions) == nil {
			accepted = append(accepted, conn)
			continue
		}
		defer func() { _ = conn.Close() }()
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
		var buf [1]byte
		_, err = conn.Read(buf[:])
		require.ErrorIs(t, err, io.EOF, "unregistered TCP must not reach the SSH handshake")
		return accepted
	}
	t.Fatal("raw TCP activity exceeded the hard lifecycle resource bound")
	return nil
}

func TestManagedSSHServerCancellationRemainsTerminal(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// #nosec G204 -- re-execute the current test binary with fixed test selection.
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestManagedSSHServerContextChild$")
	cmd.Env = append(os.Environ(), "DEVSY_MANAGED_SSH_CONTEXT_CHILD=1")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "managed listener did not terminate on cancellation: %s", output)
}

func TestManagedSSHServerContextChild(t *testing.T) {
	if os.Getenv("DEVSY_MANAGED_SSH_CONTEXT_CHILD") == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	command := &cobra.Command{}
	command.SetContext(ctx)
	cmd := &SSHServerCmd{Address: "127.0.0.1:0", TrackActivity: true}
	assert.ErrorIs(t, cmd.Run(command, nil), context.Canceled)
}

func readyPath(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()
	for _, kv := range cmd.Env {
		if v, ok := strings.CutPrefix(kv, envWorkerReadyFile+"="); ok {
			return v
		}
	}
	t.Fatal("the worker must be told where to report")
	return ""
}

func TestWorkerReadinessWaitsForSpawnedWorkerRegistration(t *testing.T) {
	workers, err := newWorkerReadiness()
	require.NoError(t, err)
	defer workers.close()
	cmd := &exec.Cmd{}
	require.NoError(t, workers.launch("post-attach", func(prepare func(*exec.Cmd)) error {
		prepare(cmd)
		return nil
	}))
	path := readyPath(t, cmd)

	shortCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	require.ErrorContains(t, workers.wait(shortCtx), "post-attach did not register")

	t.Setenv(envWorkerReadyFile, path)
	signalWorkerReady()

	require.NoError(t, workers.wait(context.Background()))
	assert.Empty(t, os.Getenv(envWorkerReadyFile), "hooks must not inherit the marker path")
}

func TestWorkerReadinessIsNoopForUnmanagedContainers(t *testing.T) {
	var workers *workerReadiness
	cmd := &exec.Cmd{}

	err := workers.launch("deferred-hooks", func(prepare func(*exec.Cmd)) error {
		prepare(cmd)
		return nil
	})

	require.NoError(t, err)
	assert.Empty(t, cmd.Env)
	require.NoError(t, workers.wait(context.Background()))
	workers.close()
}

func TestWorkerReadinessDoesNotAwaitFailedOrSkippedLaunches(t *testing.T) {
	workers, err := newWorkerReadiness()
	require.NoError(t, err)
	defer workers.close()
	launchErr := errors.New("start process")

	err = workers.launch("deferred-hooks", func(prepare func(*exec.Cmd)) error {
		prepare(&exec.Cmd{})
		return launchErr
	})
	require.ErrorIs(t, err, launchErr)
	require.NoError(t, workers.launch("post-attach", func(func(*exec.Cmd)) error {
		return nil
	}), "a worker that was already running registers on its own")

	require.NoError(t, workers.wait(context.Background()),
		"neither a failed nor a skipped launch may be awaited")
}

func TestWorkerReadinessRejectsMarkerOutsideReadinessDir(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "ready")
	t.Setenv(envWorkerReadyFile, outside)

	signalWorkerReady()

	assert.NoFileExists(t, outside)
}
