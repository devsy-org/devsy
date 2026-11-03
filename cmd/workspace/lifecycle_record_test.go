//go:build !windows

package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fakeDockerScript = `#!/bin/sh
echo "fake-out"
echo "fake-err" >&2
[ -n "$FAKE_SLEEP" ] && sleep "$FAKE_SLEEP"
exit "${FAKE_EXIT:-0}"
`

func fakeDocker(t *testing.T) *docker.DockerHelper {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docker")
	err := os.WriteFile(path, []byte(fakeDockerScript), 0o700) //nolint:gosec // test script
	require.NoError(t, err)

	return &docker.DockerHelper{DockerCommand: path}
}

func redirectStdio(t *testing.T) (stdout, stderr string) {
	t.Helper()
	dir := t.TempDir()
	outPath, errPath := filepath.Join(dir, "out"), filepath.Join(dir, "err")
	outFile, err := os.Create(outPath) //nolint:gosec // test file
	require.NoError(t, err)
	errFile, err := os.Create(errPath) //nolint:gosec // test file
	require.NoError(t, err)
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outFile, errFile
	t.Cleanup(func() { os.Stdout, os.Stderr = origOut, origErr })

	return outPath, errPath
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test file
	require.NoError(t, err)

	return string(data)
}

type recordedLifecycle struct {
	store *workspacelogs.Store
	runID string
	rec   *workspacelogs.Recorder
}

func newRecordedLifecycle(t *testing.T) *recordedLifecycle {
	t.Helper()
	store, err := workspacelogs.Open(workspacelogs.Options{Root: t.TempDir()})
	require.NoError(t, err)
	m, err := store.CreateRun(
		workspacelogs.RunSpec{Context: "default", Command: "run-user-commands"},
	)
	require.NoError(t, err)
	rec, err := workspacelogs.NewRecorder(workspacelogs.RecorderOptions{
		Backend: store, RunID: m.RunID, Location: workspacelogs.LocationContainer,
	})
	require.NoError(t, err)

	return &recordedLifecycle{store: store, runID: m.RunID, rec: rec}
}

func (r *recordedLifecycle) records(t *testing.T) []workspacelogs.Record {
	t.Helper()
	r.rec.Close(workspacelogs.ProducerTerminal, true)
	resp, err := r.store.Read(workspacelogs.ReadRequest{RunID: r.runID})
	require.NoError(t, err)

	return resp.Records
}

func outputOf(recs []workspacelogs.Record, stream workspacelogs.Stream) string {
	var out string
	for i := range recs {
		if recs[i].Kind == workspacelogs.KindOutput && recs[i].Stream == stream {
			out += recs[i].Text
		}
	}

	return out
}

func lastInvocation(recs []workspacelogs.Record) *workspacelogs.Record {
	var last *workspacelogs.Record
	for i := range recs {
		if recs[i].Kind == workspacelogs.KindInvocation {
			last = &recs[i]
		}
	}

	return last
}

func execParams(t *testing.T, rec *workspacelogs.Recorder) *LifecycleExecParams {
	t.Helper()

	return &LifecycleExecParams{
		Ctx: context.Background(), Helper: fakeDocker(t), ContainerID: "c1",
		Recorder: rec, HookIndex: 2,
	}
}

func TestExecLifecycleHookRecordsOutputAndKeepsPassthrough(t *testing.T) {
	stdoutPath, stderrPath := redirectStdio(t)
	run := newRecordedLifecycle(t)

	err := ExecLifecycleHook(execParams(t, run.rec), "postCreateCommand",
		types.LifecycleHook{"setup": {"echo hi"}})
	require.NoError(t, err)

	assert.Equal(t, "fake-out\n", readFile(t, stdoutPath))
	assert.Equal(t, "fake-err\n", readFile(t, stderrPath))
	recs := run.records(t)
	assert.Equal(t, "fake-out\n", outputOf(recs, workspacelogs.StreamStdout))
	assert.Equal(t, "fake-err\n", outputOf(recs, workspacelogs.StreamStderr))
	inv := lastInvocation(recs)
	require.NotNil(t, inv)
	assert.Equal(t, workspacelogs.InvocationSucceeded, inv.State)
	assert.Equal(t, "postCreateCommand", inv.Hook)
	assert.Equal(t, "setup", inv.HookKey)
	require.NotNil(t, inv.HookIndex)
	assert.Equal(t, 2, *inv.HookIndex)
	require.NotNil(t, inv.ExitCode)
	assert.Zero(t, *inv.ExitCode)
}

func TestExecLifecycleHookRecordsFailureWithHint(t *testing.T) {
	redirectStdio(t)
	t.Setenv("FAKE_EXIT", "3")
	run := newRecordedLifecycle(t)

	err := ExecLifecycleHook(execParams(t, run.rec), "postStartCommand",
		types.LifecycleHook{"": {"false"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--source setup --run "+run.runID+" --hook postStartCommand")

	inv := lastInvocation(run.records(t))
	require.NotNil(t, inv)
	assert.Equal(t, workspacelogs.InvocationFailed, inv.State)
	require.NotNil(t, inv.ExitCode)
	assert.Equal(t, 3, *inv.ExitCode)
}

func TestExecLifecycleHookRecordsStartFailure(t *testing.T) {
	redirectStdio(t)
	run := newRecordedLifecycle(t)
	params := execParams(t, run.rec)
	params.Helper = &docker.DockerHelper{DockerCommand: filepath.Join(t.TempDir(), "missing")}

	require.Error(t, ExecLifecycleHook(params, "postCreateCommand",
		types.LifecycleHook{"": {"true"}}))

	inv := lastInvocation(run.records(t))
	require.NotNil(t, inv)
	assert.Equal(t, workspacelogs.InvocationFailed, inv.State)
	assert.Equal(t, "start_failed", inv.Reason)
	assert.Nil(t, inv.ExitCode)
}

func TestExecLifecycleHookRecordsCancellation(t *testing.T) {
	redirectStdio(t)
	t.Setenv("FAKE_SLEEP", "30")
	run := newRecordedLifecycle(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)
	params := execParams(t, run.rec)
	params.Ctx = ctx

	require.Error(t, ExecLifecycleHook(params, "postCreateCommand",
		types.LifecycleHook{"": {"x"}}))

	inv := lastInvocation(run.records(t))
	require.NotNil(t, inv)
	assert.Equal(t, workspacelogs.InvocationCanceled, inv.State)
}

func TestExecLifecycleHookWithoutRecorderIsUnchanged(t *testing.T) {
	stdoutPath, _ := redirectStdio(t)

	require.NoError(t, ExecLifecycleHook(execParams(t, nil), "postCreateCommand",
		types.LifecycleHook{"": {"x"}}))
	assert.Equal(t, "fake-out\n", readFile(t, stdoutPath))

	t.Setenv("FAKE_EXIT", "1")
	err := ExecLifecycleHook(execParams(t, nil), "postCreateCommand",
		types.LifecycleHook{"": {"x"}})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "full output")
}
