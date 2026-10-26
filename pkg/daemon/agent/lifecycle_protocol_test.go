package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestOwner(t *testing.T) (*LifecycleOwner, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "lifecycle")
	owner, err := NewLifecycleOwner(dir)
	require.NoError(t, err)
	return owner, dir
}

func connectNow(t *testing.T, dir string) *LifecycleClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := ConnectLifecycle(ctx, dir)
	require.NoError(t, err)
	return client
}

// mustPublish writes a control record without waiting for the owner, for tests
// that drive the owner by hand.
func mustPublish(t *testing.T, c *LifecycleClient, rec ControlRecord) uint64 {
	t.Helper()
	sequence, err := c.publish(rec.State, rec.Timeout, rec.ShutdownAction)
	require.NoError(t, err)
	return sequence
}

func TestSetupControlRecordReachesOwner(t *testing.T) {
	owner, dir := newTestOwner(t)
	client := connectNow(t, dir)

	mustPublish(t, client, control(LifecycleStateSetup, "1m", "stopContainer"))

	rec, changed, err := owner.PollControl()
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, LifecycleStateSetup, rec.State)
	assert.Equal(t, "1m", rec.Timeout)
	assert.Equal(t, owner.Generation(), rec.Generation)

	_, changed, err = owner.PollControl()
	require.NoError(t, err)
	assert.False(t, changed, "an unchanged record is not reapplied")
}

func TestStaleGenerationCannotArmNewOwner(t *testing.T) {
	first, dir := newTestOwner(t)
	oldClient := connectNow(t, dir)
	mustPublish(t, oldClient, control(LifecycleStateReady, "1m", ""))
	require.NotEmpty(t, first.Generation())

	// A restarted daemon on the same directory discards the old record.
	second, err := NewLifecycleOwner(dir)
	require.NoError(t, err)
	require.NotEqual(t, first.Generation(), second.Generation())
	_, changed, err := second.PollControl()
	require.NoError(t, err)
	assert.False(t, changed)

	// Even a stale record that reappears is ignored by generation.
	mustPublish(t, oldClient, control(LifecycleStateReady, "1m", ""))
	_, changed, err = second.PollControl()
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestConnectLifecycleWithoutOwnerFailsBounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := ConnectLifecycle(ctx, filepath.Join(t.TempDir(), "absent"))

	assert.ErrorIs(t, err, ErrLifecycleOwnerMissing)
}

func TestConnectLifecycleRejectsIncompatibleOwner(t *testing.T) {
	_, dir := newTestOwner(t)
	require.NoError(t, writeRecord(dir, ownerFileName, OwnerRecord{
		Version: LifecycleProtocolVersion + 1, Generation: "g", PID: os.Getpid(),
	}, os.Geteuid()))

	_, err := ConnectLifecycle(context.Background(), dir)

	assert.ErrorIs(t, err, ErrLifecycleOwnerIncompatible)
}

func TestConnectLifecycleIgnoresOwnerOfDeadProcess(t *testing.T) {
	_, dir := newTestOwner(t)
	require.NoError(t, writeRecord(dir, ownerFileName, OwnerRecord{
		Version: LifecycleProtocolVersion, Generation: "g", PID: 0x7ffffffe,
	}, os.Geteuid()))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := ConnectLifecycle(ctx, dir)

	assert.ErrorIs(t, err, ErrLifecycleOwnerMissing)
}

func TestOwnerRejectsSymlinkedControlRecord(t *testing.T) {
	owner, dir := newTestOwner(t)
	target := filepath.Join(t.TempDir(), "target.json")
	require.NoError(t, os.WriteFile(target, []byte(`{}`), 0o600))
	require.NoError(t, os.Symlink(target, filepath.Join(dir, controlFileName)))

	_, changed, err := owner.PollControl()

	assert.Error(t, err)
	assert.False(t, changed)
}

func TestNewLifecycleOwnerRejectsSymlinkedDir(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	require.NoError(t, os.Mkdir(target, 0o755)) //nolint:gosec // test dir
	link := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(target, link))

	_, err := NewLifecycleOwner(link)

	assert.Error(t, err)
}

func TestReadLifecycleTimeoutFollowsControlRecord(t *testing.T) {
	_, dir := newTestOwner(t)
	_, ok := ReadLifecycleTimeout(dir)
	assert.False(t, ok, "no control record yet")

	mustPublish(t, connectNow(t, dir), control(LifecycleStateSetup, "45s", ""))

	timeout, ok := ReadLifecycleTimeout(dir)
	assert.True(t, ok)
	assert.Equal(t, 45*time.Second, timeout)
}

func TestSendReturnsOnlyAfterOwnerAppliesTheRecord(t *testing.T) {
	owner, dir := newTestOwner(t)
	client := connectNow(t, dir)
	sent := make(chan error, 1)
	go func() { sent <- client.Send(LifecycleStateSetup, "1m", "") }()

	select {
	case err := <-sent:
		t.Fatalf("Send returned before the owner applied the record: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	rec := pollNow(t, owner)
	assert.Equal(t, LifecycleStateSetup, rec.State)
	require.NoError(t, owner.Acknowledge(rec, nil))

	select {
	case err := <-sent:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Send did not return after the owner acknowledged the record")
	}
}

func pollNow(t *testing.T, owner *LifecycleOwner) ControlRecord {
	t.Helper()
	rec, changed, err := owner.PollControl()
	require.NoError(t, err)
	require.True(t, changed)
	return rec
}

func TestIdenticalRepeatedRecordsAreEachDeliveredToTheOwner(t *testing.T) {
	owner, dir := newTestOwner(t)
	client := connectNow(t, dir)

	mustPublish(t, client, control(LifecycleStateReady, "1m", ""))
	first := pollNow(t, owner)
	mustPublish(t, client, control(LifecycleStateReady, "1m", ""))
	second := pollNow(t, owner)

	assert.Greater(t, second.Sequence, first.Sequence)
}

func TestSecondClientRecordIsNeverTakenForTheFirstClientsRecord(t *testing.T) {
	owner, dir := newTestOwner(t)

	mustPublish(t, connectNow(t, dir), control(LifecycleStateReady, "1m", ""))
	first := pollNow(t, owner)
	mustPublish(t, connectNow(t, dir), control(LifecycleStateReady, "1m", ""))
	second := pollNow(t, owner)

	assert.NotEqual(t, first.ClientID, second.ClientID)
}

func TestSendDoesNotAcceptAnotherClientsAcknowledgement(t *testing.T) {
	owner, dir := newTestOwner(t)
	first := connectNow(t, dir)
	mustPublish(t, first, control(LifecycleStateReady, "1m", ""))
	require.NoError(t, owner.Acknowledge(pollNow(t, owner), nil))

	second := connectNow(t, dir)
	second.ackTimeout = 100 * time.Millisecond

	assert.ErrorIs(t, second.Send(LifecycleStateSetup, "1m", ""),
		ErrLifecycleNotAcknowledged)
}

func TestSendFailsWhenOwnerNeverAcknowledges(t *testing.T) {
	_, dir := newTestOwner(t)
	client := connectNow(t, dir)
	client.ackTimeout = 100 * time.Millisecond

	err := client.Send(LifecycleStateSetup, "1m", "")

	assert.ErrorIs(t, err, ErrLifecycleNotAcknowledged)
}

func TestSendSurfacesOwnerRejection(t *testing.T) {
	owner, dir := newTestOwner(t)
	client := connectNow(t, dir)
	sent := make(chan error, 1)
	go func() { sent <- client.Send(LifecycleStateSetup, "soon", "") }()

	require.Eventually(t, func() bool {
		rec, changed, err := owner.PollControl()
		if err != nil || !changed {
			return false
		}
		require.NoError(t, owner.Acknowledge(rec, errors.New("invalid lifecycle timeout")))
		return true
	}, 5*time.Second, 10*time.Millisecond)

	assert.ErrorContains(t, <-sent, "invalid lifecycle timeout")
}
