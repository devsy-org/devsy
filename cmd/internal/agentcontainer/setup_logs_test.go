package agentcontainer

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	logsContext = "default"
	logsUID     = "uid-1"
	flagContext = "--context"
	flagRun     = "--run"
)

func runSetupLogs(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	global := &flags.GlobalFlags{}
	cmd := NewSetupLogsCmd(global)
	// Global flags are persistent on the real root command.
	cmd.Flags().StringVar(&global.Context, "context", "", "")
	cmd.Flags().StringVar(&global.UID, "uid", "", "")
	cmd.SilenceUsage = true
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()

	return out.String(), errOut.String(), err
}

func seedSpool(t *testing.T) (root, runID string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), workspacelogs.SpoolDirName)
	store, err := workspacelogs.Open(workspacelogs.Options{Root: root})
	require.NoError(t, err)
	m, err := store.CreateRun(workspacelogs.RunSpec{
		Context: logsContext, WorkspaceUID: logsUID, Command: "up",
	})
	require.NoError(t, err)
	rec, err := workspacelogs.NewRecorder(workspacelogs.RecorderOptions{
		Backend: store, RunID: m.RunID, Location: workspacelogs.LocationContainer,
	})
	require.NoError(t, err)
	inv := workspacelogs.NewID()
	rec.Invocation(workspacelogs.Invocation{
		ID: inv, Phase: "setup", Hook: "postCreateCommand", State: workspacelogs.InvocationRunning,
	})
	w := rec.Writer(inv, workspacelogs.StreamStdout)
	_, _ = w.Write([]byte("hi\n"))
	_ = w.Close()
	rec.Close(workspacelogs.ProducerTerminal, true)

	return root, m.RunID
}

func withSpool(t *testing.T, root string, err error) {
	t.Helper()
	orig := resolveSpool
	resolveSpool = func() (string, error) { return root, err }
	t.Cleanup(func() { resolveSpool = orig })
}

func TestSetupLogsPrintsOnePageAsJSON(t *testing.T) {
	root, runID := seedSpool(t)
	withSpool(t, root, nil)

	stdout, _, err := runSetupLogs(t, flagRun, runID, flagContext, logsContext, "--uid", logsUID)

	require.NoError(t, err)
	var resp workspacelogs.ReadResponse
	require.NoError(t, json.Unmarshal([]byte(stdout), &resp))
	assert.Equal(t, runID, resp.Run.RunID)
	assert.NotEmpty(t, resp.Records)
}

func TestSetupLogsNoSpoolIsTypedUnavailable(t *testing.T) {
	withSpool(t, "", workspacelogs.ErrNotFound)

	stdout, stderr, err := runSetupLogs(t, flagRun, workspacelogs.NewID(), flagContext, logsContext)

	require.Error(t, err)
	assert.Empty(t, stdout)
	reason, ok := workspacelogs.ParseUnavailable(stderr)
	require.True(t, ok)
	assert.Equal(t, workspacelogs.ReasonNoSpool, reason)
}

func TestSetupLogsRejectsWrongIdentity(t *testing.T) {
	root, runID := seedSpool(t)
	withSpool(t, root, nil)

	for name, args := range map[string][]string{
		"uid":     {flagRun, runID, flagContext, logsContext, "--uid", "uid-2"},
		"no uid":  {flagRun, runID, flagContext, logsContext},
		"context": {flagRun, runID, flagContext, "elsewhere", "--uid", logsUID},
	} {
		stdout, _, err := runSetupLogs(t, args...)
		require.Error(t, err, name)
		assert.Empty(t, stdout, name)
	}
}

func TestSetupLogsValidatesSelectorsBeforeTouchingTheSpool(t *testing.T) {
	called := false
	orig := resolveSpool
	resolveSpool = func() (string, error) { called = true; return "", nil }
	t.Cleanup(func() { resolveSpool = orig })

	for _, args := range [][]string{
		{flagRun, "../escape"},
		{flagRun, workspacelogs.NewID(), "--hook", "nope"},
		{flagRun, workspacelogs.NewID(), "--invocation", "x"},
		{flagRun, workspacelogs.NewID(), "--after", "a b"},
		{flagRun, workspacelogs.NewID(), "--limit", "99999"},
	} {
		_, _, err := runSetupLogs(t, args...)
		require.Error(t, err)
	}
	assert.False(t, called)
}
