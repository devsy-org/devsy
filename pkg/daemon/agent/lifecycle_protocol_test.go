package agent

import (
	"context"
	"encoding/json"
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

func TestSetupControlRecordReachesOwner(t *testing.T) {
	owner, dir := newTestOwner(t)
	client := connectNow(t, dir)

	require.NoError(t, client.Send(LifecycleStateSetup, "1m", "stopContainer"))

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

func TestControlRecordCarriesNoExtraFields(t *testing.T) {
	_, dir := newTestOwner(t)
	require.NoError(t, connectNow(t, dir).Send(LifecycleStateReady, "1m", "none"))

	raw, err := os.ReadFile(filepath.Join(dir, controlFileName)) //nolint:gosec // test path
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(raw, &fields))

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	assert.ElementsMatch(
		t,
		[]string{"version", "generation", "timeout", "shutdownAction", "state"},
		keys,
	)
}

func TestStaleGenerationCannotArmNewOwner(t *testing.T) {
	first, dir := newTestOwner(t)
	oldClient := connectNow(t, dir)
	require.NoError(t, oldClient.Send(LifecycleStateReady, "1m", ""))
	require.NotEmpty(t, first.Generation())

	// A restarted daemon on the same directory discards the old record.
	second, err := NewLifecycleOwner(dir)
	require.NoError(t, err)
	require.NotEqual(t, first.Generation(), second.Generation())
	_, changed, err := second.PollControl()
	require.NoError(t, err)
	assert.False(t, changed)

	// Even a stale record that reappears is ignored by generation.
	require.NoError(t, oldClient.Send(LifecycleStateReady, "1m", ""))
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

	require.NoError(t, connectNow(t, dir).Send(LifecycleStateSetup, "45s", ""))

	timeout, ok := ReadLifecycleTimeout(dir)
	assert.True(t, ok)
	assert.Equal(t, 45*time.Second, timeout)
}
