package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestOwner(t *testing.T) (*LifecycleOwner, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("managed Kubernetes lifecycle uses Unix peer identity")
	}
	// Darwin's Unix socket path limit is shorter than testing.TempDir's names.
	dir, err := os.MkdirTemp("/tmp", "devsy-lifecycle-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	owner, err := NewLifecycleOwner(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = owner.Close() })
	return owner, dir
}

func connectNow(t *testing.T, dir string) *LifecycleClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := ConnectLifecycle(ctx, dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func runLoop(t *testing.T, loop *LifecycleLoop) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- loop.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			assert.ErrorIs(t, err, ErrLifecycleOwnerMissing)
		}
	})
}

func idleForAnHour(m *LifecycleMonitor) Decision {
	return m.Evaluate(time.Now().Add(time.Hour), time.Time{}, nil)
}

func TestSetupIsProtectedBeforeSendReturnsAndReadyResetsIt(t *testing.T) {
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	monitor.ArmNow(time.Minute, time.Now())
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	client := connectNow(t, dir)
	for range 2 {
		require.NoError(t, client.Send(LifecycleStateSetup, "1m", ""))
		assert.False(t, idleForAnHour(monitor).Expired)
		require.NoError(t, client.Send(LifecycleStateReady, "1m", ""))
		assert.True(t, idleForAnHour(monitor).Expired)
	}
}

func TestAcknowledgementFailureRetriesWithoutReapplyingReady(t *testing.T) {
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	firstApplied := make(chan time.Time, 1)
	var writes atomic.Int32
	owner.writeAck = func(conn net.Conn, ack AckRecord) error {
		if writes.Add(1) == 1 {
			monitor.mu.Lock()
			firstApplied <- monitor.armedAt
			monitor.mu.Unlock()
			return os.ErrPermission
		}
		return writeSocketAck(conn, ack)
	}
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	client := connectNow(t, dir)

	require.NoError(t, client.Send(LifecycleStateReady, "1m", ""))

	appliedAt := <-firstApplied
	assert.True(t, monitor.Evaluate(appliedAt.Add(time.Minute), time.Time{}, nil).Expired,
		"retrying the ACK must not shift the original idle deadline")
}

func TestUnacknowledgedSetupCanBeClosedWithoutOrphaningProtection(t *testing.T) {
	owner, dir := newTestOwner(t)
	owner.writeAck = func(net.Conn, AckRecord) error { return os.ErrPermission }
	monitor := &LifecycleMonitor{}
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	client := connectNow(t, dir)
	client.ackTimeout = 250 * time.Millisecond

	require.ErrorIs(t, client.Send(LifecycleStateSetup, "1m", ""), ErrLifecycleNotAcknowledged)
	require.NoError(t, client.Close())

	require.Eventually(t, func() bool { return idleForAnHour(monitor).Expired },
		time.Second, time.Millisecond, "failed handshake cleanup must release setup")
}

func TestLiveSlowSetupHasNoLeaseThatExpiresIt(t *testing.T) {
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var loopErr error
	go func() { defer close(done); loopErr = (&LifecycleLoop{Owner: owner, Monitor: monitor}).Run(ctx) }()
	defer func() { cancel(); <-done }()
	client := connectNow(t, dir)
	require.NoError(t, client.Send(LifecycleStateSetup, "150ms", ""))

	assert.Never(t, func() bool { return monitor.Evaluate(time.Now(), time.Time{}, nil).Expired },
		500*time.Millisecond, 10*time.Millisecond)
	require.NoError(t, client.Close())
	select {
	case <-done:
		assert.ErrorIs(t, loopErr, ErrInactivityExpired)
	case <-time.After(5 * time.Second):
		t.Fatal("disconnected setup remained suspended")
	}
}

func TestOneSetupCannotReleaseAnotherSetupsProtection(t *testing.T) {
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	first, second := connectNow(t, dir), connectNow(t, dir)
	require.NoError(t, first.Send(LifecycleStateSetup, "1m", ""))
	require.NoError(t, second.Send(LifecycleStateSetup, "1m", ""))
	require.NoError(t, first.Send(LifecycleStateReady, "1m", ""))
	assert.False(t, idleForAnHour(monitor).Expired)
	require.NoError(t, second.Send(LifecycleStateFailed, "1m", ""))
	assert.True(t, idleForAnHour(monitor).Expired)
}

func TestStorageFailureCannotHideARegisteredLiveWriter(t *testing.T) {
	owner, dir := newTestOwner(t)
	if os.Geteuid() == 0 {
		t.Skip("read-only directory permissions require an unprivileged writer")
	}
	// Stat remains readable with an old timestamp, but touching the malformed
	// activity file and creating ANY health/publication inode both fail.
	activity := filepath.Join(dir, "activity")
	require.NoError(t, os.Mkdir(activity, 0o700))
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(activity, old, old))
	// #nosec G302 -- readable but unwritable storage is the regression scenario.
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() {
		// #nosec G302 -- restore owner-write access so temporary state can be removed.
		_ = os.Chmod(dir, 0o755)
	})
	require.Error(t, TouchActivity(activity, time.Now()))
	monitor := &LifecycleMonitor{}
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor, ActivityPath: activity})
	client := connectNow(t, dir)
	require.NoError(t, client.Send(LifecycleStateReady, "1m", ""))
	require.Error(
		t,
		writeRecord(
			dir,
			"unwritable-health.json",
			control(LifecycleStateReady, "1m", ""),
			os.Geteuid(),
		),
	)

	workCtx, stop, err := (ActivityWriter{Path: activity, LifecycleDir: dir, Interval: 10 * time.Millisecond}).Start(
		context.Background(),
	)
	require.NoError(t, err)
	defer stop()
	assert.NoError(t, workCtx.Err())
	assert.False(t, monitor.Evaluate(time.Now().Add(time.Hour), old, nil).Expired,
		"neither failed file write can remove in-memory live registration")

	stop()
	require.Eventually(
		t,
		func() bool { return idleForAnHour(monitor).Expired },
		time.Second,
		time.Millisecond,
	)
}

func TestReleaseAcknowledgementRecoveryDoesNotResetIdleTwice(t *testing.T) {
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	var dropped atomic.Bool
	firstRelease := make(chan time.Time, 1)
	owner.writeAck = func(conn net.Conn, ack AckRecord) error {
		if ack.Sequence == 2 && dropped.CompareAndSwap(false, true) {
			monitor.mu.Lock()
			firstRelease <- monitor.armedAt
			monitor.mu.Unlock()
			return os.ErrPermission
		}
		return writeSocketAck(conn, ack)
	}
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	require.NoError(t, connectNow(t, dir).Send(LifecycleStateReady, "1m", ""))
	_, stop, err := (ActivityWriter{Path: filepath.Join(dir, "activity"), LifecycleDir: dir}).Start(
		context.Background(),
	)
	require.NoError(t, err)
	defer stop()

	stop()

	appliedAt := <-firstRelease
	assert.True(t, monitor.Evaluate(appliedAt.Add(time.Minute), time.Time{}, nil).Expired,
		"release must retry after publication failure without another idle reset")
}

func TestOwnerLossCancelsProtectedWriterAndRejectsFurtherOperations(t *testing.T) {
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	workCtx, stop, err := (ActivityWriter{Path: filepath.Join(dir, "activity"), LifecycleDir: dir}).Start(
		context.Background(),
	)
	require.NoError(t, err)
	defer stop()

	require.NoError(t, owner.Close())

	select {
	case <-workCtx.Done():
		assert.ErrorIs(t, context.Cause(workCtx), ErrLifecycleOwnerMissing)
	case <-time.After(time.Second):
		t.Fatal("work continued after losing its lifecycle owner")
	}
	_, _, err = (ActivityWriter{Path: filepath.Join(dir, "activity"), LifecycleDir: dir}).Start(
		context.Background(),
	)
	assert.ErrorIs(t, err, ErrLifecycleOwnerMissing)
}

func TestLifecycleChildProcess(t *testing.T) {
	dir := os.Getenv("DEVSY_LIFECYCLE_TEST_CHILD")
	if dir == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := ConnectLifecycle(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if err := client.Send(LifecycleStateSetup, "1m", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stdout.WriteString("registered\n"); err != nil {
		t.Fatal(err)
	}
	<-client.Done()
}

func TestKilledSetupProcessReleasesProtectionAndCanReconnect(t *testing.T) {
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	binary, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(
		binary,
		"-test.run=^TestLifecycleChildProcess$",
	) // #nosec G204 -- current test binary
	cmd.Env = append(os.Environ(), "DEVSY_LIFECYCLE_TEST_CHILD="+dir)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	registered := make(chan string, 1)
	go func() { scanner := bufio.NewScanner(stdout); scanner.Scan(); registered <- scanner.Text() }()
	select {
	case line := <-registered:
		require.Equal(t, "registered", line)
	case <-time.After(5 * time.Second):
		t.Fatal("child did not establish acknowledged setup")
	}
	assert.False(t, idleForAnHour(monitor).Expired)

	require.NoError(t, cmd.Process.Kill())
	require.Error(t, cmd.Wait())

	require.Eventually(
		t,
		func() bool { return idleForAnHour(monitor).Expired },
		time.Second,
		time.Millisecond,
	)
	reconnected := connectNow(t, dir)
	require.NoError(t, reconnected.Send(LifecycleStateSetup, "1m", ""))
	assert.False(t, idleForAnHour(monitor).Expired)
	require.NoError(t, reconnected.Send(LifecycleStateReady, "1m", ""))
	assert.True(t, idleForAnHour(monitor).Expired)
}

func TestOwnerRestartInvalidatesExistingConnectionAndNewOwnerStartsUnarmed(t *testing.T) {
	first, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	runLoop(t, &LifecycleLoop{Owner: first, Monitor: monitor})
	old := connectNow(t, dir)
	require.NoError(t, old.Send(LifecycleStateSetup, "1m", ""))
	require.NoError(t, first.Close())
	second, err := NewLifecycleOwner(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })
	fresh := &LifecycleMonitor{}
	runLoop(t, &LifecycleLoop{Owner: second, Monitor: fresh})

	assert.ErrorIs(t, old.Send(LifecycleStateReady, "1m", ""), ErrLifecycleOwnerMissing)
	assert.False(t, idleForAnHour(fresh).Expired)
	require.NoError(t, connectNow(t, dir).Send(LifecycleStateReady, "1m", ""))
	assert.True(t, idleForAnHour(fresh).Expired)
}

func TestConnectLifecycleRejectsIncompatibleOwner(t *testing.T) {
	_, dir := newTestOwner(t)
	require.NoError(t, writeRecord(dir, ownerFileName, OwnerRecord{
		Version: LifecycleProtocolVersion - 1, Generation: "old", PID: os.Getpid(),
	}, os.Geteuid()))
	_, err := ConnectLifecycle(context.Background(), dir)
	assert.ErrorIs(t, err, ErrLifecycleOwnerIncompatible)
}

func TestConnectLifecycleMissingOwnerIsBounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := ConnectLifecycle(ctx, filepath.Join(t.TempDir(), "absent"))
	assert.ErrorIs(t, err, ErrLifecycleOwnerMissing)
}

func TestClientSeesRejectedPolicyWithoutArming(t *testing.T) {
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	err := connectNow(t, dir).Send(LifecycleStateSetup, "soon", "")
	assert.ErrorContains(t, err, "invalid lifecycle timeout")
	assert.False(t, idleForAnHour(monitor).Expired)
}

func TestMissingManagedDiscoveryNeverFallsBackToUntrackedWork(t *testing.T) {
	owner, dir := newTestOwner(t)
	require.NoError(t, owner.Close())
	require.NoError(t, os.Remove(filepath.Join(dir, ownerFileName)))

	ctx, stop, err := (ActivityWriter{
		Path: filepath.Join(dir, "activity"), LifecycleDir: dir,
	}).Start(context.Background())
	if stop != nil {
		defer stop()
	}

	assert.ErrorIs(t, err, ErrLifecycleOwnerMissing)
	assert.Nil(t, ctx)
}

func TestDeadOwnerNeverFallsBackToLegacyActivity(t *testing.T) {
	_, dir := newTestOwner(t)
	require.NoError(t, writeRecord(dir, ownerFileName, OwnerRecord{
		Version: LifecycleProtocolVersion, Generation: "dead", PID: 0x7ffffffe,
	}, os.Geteuid()))

	_, stop, err := (ActivityWriter{
		Path: filepath.Join(dir, "activity"), LifecycleDir: dir,
	}).Start(context.Background())
	if stop != nil {
		defer stop()
	}

	assert.ErrorIs(t, err, ErrLifecycleOwnerMissing)
}

func TestNewOwnerRejectsSymlinkWithoutChangingTargetPermissions(t *testing.T) {
	_, dir := newTestOwner(t)
	target := filepath.Join(dir, "private")
	require.NoError(t, os.Mkdir(target, 0o700))
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(target, link))

	_, err := NewLifecycleOwner(link)

	require.Error(t, err)
	info, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestConnectRejectsSymlinkedDiscovery(t *testing.T) {
	_, dir := newTestOwner(t)
	target := filepath.Join(dir, "untrusted")
	require.NoError(t, os.WriteFile(target, []byte(`{}`), 0o600))
	require.NoError(t, os.Remove(filepath.Join(dir, ownerFileName)))
	require.NoError(t, os.Symlink(target, filepath.Join(dir, ownerFileName)))

	_, err := ConnectLifecycle(context.Background(), dir)

	assert.ErrorIs(t, err, ErrLifecycleOwnerMissing)
}

func TestManagedWorkCannotFallBackDuringInitialOwnerStartup(t *testing.T) {
	_, dir := newTestOwner(t)
	activity := filepath.Join(dir, "activity")
	_, stop, err := (ActivityWriter{
		Path: activity, LifecycleDir: filepath.Join(dir, "not-started"), RequireOwner: true,
	}).Start(context.Background())
	if stop != nil {
		defer stop()
	}

	assert.ErrorIs(t, err, ErrLifecycleOwnerMissing)
	_, err = os.Stat(activity)
	assert.ErrorIs(t, err, os.ErrNotExist, "no untracked activity loop should be started")
}

func rawLifecycleConnection(t *testing.T, dir string) net.Conn {
	t.Helper()
	conn, err := net.Dial("unix", filepath.Join(dir, socketFileName))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	return conn
}

func requireRejectedConnection(t *testing.T, conn net.Conn) {
	t.Helper()
	var buf [1]byte
	n, err := conn.Read(buf[:])
	require.Error(t, err)
	assert.Zero(t, n)
	if networkErr, ok := err.(net.Error); ok {
		assert.False(
			t,
			networkErr.Timeout(),
			"the owner must reject, not leave an unbounded pending request",
		)
	}
}

func TestSocketRejectsForgedProcessIdentityWithoutDisarming(t *testing.T) {
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	monitor.ArmNow(time.Minute, time.Now())
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	conn := rawLifecycleConnection(t, dir)
	require.NoError(t, json.NewEncoder(conn).Encode(ControlRecord{
		Version: LifecycleProtocolVersion, Generation: owner.Generation(),
		ClientID: "forged", Sequence: 1, PID: os.Getpid() + 1,
		State: LifecycleStateReady, Timeout: "0",
	}))

	requireRejectedConnection(t, conn)
	assert.True(t, idleForAnHour(monitor).Expired, "a forged peer cannot disable the current timer")
	require.NoError(t, connectNow(t, dir).Send(LifecycleStateSetup, "1m", ""))
	assert.False(t, idleForAnHour(monitor).Expired, "the authenticated owner must remain usable")
}

func TestSocketMessageBoundRejectsUnterminatedOversizedFrame(t *testing.T) {
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	monitor.ArmNow(time.Minute, time.Now())
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	conn := rawLifecycleConnection(t, dir)
	_, err := conn.Write(bytes.Repeat([]byte{'x'}, maxRecordBytes+1))
	require.NoError(t, err)

	requireRejectedConnection(t, conn)
	assert.True(t, idleForAnHour(monitor).Expired)
	require.NoError(t, connectNow(t, dir).Send(LifecycleStateSetup, "1m", ""))
	assert.False(t, idleForAnHour(monitor).Expired)
}

func TestLiveClientBoundRefusesUnprotectedWorkAndRecoversCapacity(t *testing.T) {
	owner, dir := newTestOwner(t)
	monitor := &LifecycleMonitor{}
	monitor.ArmNow(time.Minute, time.Now())
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: monitor})
	var first *LifecycleClient
	for range maxLifecycleClients {
		client := connectNow(t, dir)
		if first == nil {
			first = client
		}
		require.NoError(t, client.Send(LifecycleStateActivity, "", ""))
	}
	writer := ActivityWriter{LifecycleDir: dir, RequireOwner: true}
	ctx, stop, err := writer.Start(context.Background())
	if stop != nil {
		defer stop()
	}
	assert.Error(t, err, "capacity exhaustion must not silently fall back to legacy tracking")
	assert.Nil(t, ctx)

	require.NoError(t, first.Close())

	var regainedStop func()
	require.Eventually(t, func() bool {
		_, release, err := writer.Start(context.Background())
		if err != nil {
			return false
		}
		regainedStop = release
		return true
	}, 3*time.Second, 10*time.Millisecond)
	defer regainedStop()
	assert.False(t, idleForAnHour(monitor).Expired)
}

func TestClientVerifiesSocketPeerAgainstDiscoveryIdentity(t *testing.T) {
	owner, dir := newTestOwner(t)
	if os.Getppid() <= 0 {
		t.Skip("a distinct live parent is needed for the forged discovery identity")
	}
	runLoop(t, &LifecycleLoop{Owner: owner, Monitor: &LifecycleMonitor{}})
	require.NoError(t, writeRecord(dir, ownerFileName, OwnerRecord{
		Version: LifecycleProtocolVersion, Generation: owner.Generation(), PID: os.Getppid(),
	}, os.Geteuid()))

	_, err := ConnectLifecycle(context.Background(), dir)

	assert.ErrorIs(t, err, ErrLifecycleOwnerIncompatible)
}
