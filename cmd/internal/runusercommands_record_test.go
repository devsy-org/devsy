//go:build !windows

package cmdinternal

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/cmd/workspace"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fakeDockerParams(t *testing.T) *workspace.LifecycleExecParams {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docker")
	script := "#!/bin/sh\necho hook-out\nexit \"${FAKE_EXIT:-0}\"\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700)) //nolint:gosec // test script

	return &workspace.LifecycleExecParams{
		Ctx: context.Background(), Helper: &docker.DockerHelper{DockerCommand: path},
		ContainerID: "c1",
	}
}

func TestRunRecordingWithoutWorkspaceIdentityRecordsNothing(t *testing.T) {
	t.Setenv("DEVSY_HOME", t.TempDir())
	cmd := &RunUserCommandsCmd{}
	params := fakeDockerParams(t)

	recording := cmd.beginRunRecording(params)

	assert.Nil(t, recording)
	assert.Nil(t, params.Recorder)
	recording.end(context.Background(), nil)
}

func TestRunRecordingCreatesFreshBoundRun(t *testing.T) {
	t.Setenv("DEVSY_HOME", t.TempDir())
	cmd := &RunUserCommandsCmd{identity: &workspaceIdentity{
		context: "default", uid: "uid-1", id: "my-ws",
	}}
	hooks := []types.LifecycleHook{{"": {"echo a"}}, {"named": {"echo b"}}}

	var runIDs []string
	for range 2 {
		params := fakeDockerParams(t)
		recording := cmd.beginRunRecording(params)
		require.NotNil(t, recording)
		err := execLifecycleHooks(params, "postStartCommand", hooks)
		recording.end(context.Background(), err)
		require.NoError(t, err)
		runIDs = append(runIDs, recording.runID)
	}
	assert.NotEqual(t, runIDs[0], runIDs[1], "every invocation allocates a new run")

	store, err := workspacelogs.OpenDefault()
	require.NoError(t, err)
	resp, err := store.Read(workspacelogs.ReadRequest{RunID: runIDs[0]})
	require.NoError(t, err)
	require.NotNil(t, resp.Run.WorkspaceUID)
	assert.Equal(t, "uid-1", *resp.Run.WorkspaceUID)
	assert.Equal(t, workspacelogs.CommandSucceeded, resp.Run.CommandState)
	assert.True(t, resp.Complete)

	indexes := map[int]string{}
	var out string
	for _, r := range resp.Records {
		if r.Kind == workspacelogs.KindInvocation && r.State == workspacelogs.InvocationSucceeded {
			indexes[*r.HookIndex] = r.HookKey
		}
		if r.Kind == workspacelogs.KindOutput {
			out += r.Text
		}
	}
	assert.Equal(t, map[int]string{0: "", 1: "named"}, indexes)
	assert.Equal(t, "hook-out\nhook-out\n", out)
}

func TestRunRecordingMarksFailedCommand(t *testing.T) {
	t.Setenv("DEVSY_HOME", t.TempDir())
	t.Setenv("FAKE_EXIT", "9")
	cmd := &RunUserCommandsCmd{identity: &workspaceIdentity{context: "default", uid: "uid-1"}}
	params := fakeDockerParams(t)

	recording := cmd.beginRunRecording(params)
	require.NotNil(t, recording)
	err := execLifecycleHooks(params, "postCreateCommand", []types.LifecycleHook{{"": {"x"}}})
	require.Error(t, err)
	recording.end(context.Background(), err)

	store, err := workspacelogs.OpenDefault()
	require.NoError(t, err)
	resp, err := store.Read(workspacelogs.ReadRequest{RunID: recording.runID})
	require.NoError(t, err)
	assert.Equal(t, workspacelogs.CommandFailed, resp.Run.CommandState)
}
