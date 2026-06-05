package workspace

import (
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusCmd_PlainPrintsAtDefaultVerbosity is the regression guard: plain
// status output must reach stdout even when the logger is at its default
// (error-only) level. Previously the result was emitted via log.Infof, which
// the default verbosity silenced, leaving the command output empty.
func TestStatusCmd_PlainPrintsAtDefaultVerbosity(t *testing.T) {
	log.Init(log.Config{Verbosity: 0})

	cases := []struct {
		name   string
		status client.Status
		want   string
	}{
		{"running", client.StatusRunning, `Workspace "node-js" is "Running"`},
		{"stopped", client.StatusStopped, `Workspace "node-js" is "Stopped"`},
		{"busy", client.StatusBusy, `Workspace "node-js" is "Busy"`},
		{"notfound", client.StatusNotFound, `Workspace "node-js" is "NotFound"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &StatusCmd{
				GlobalFlags: &flags.GlobalFlags{ResultFormat: "plain"},
			}
			fake := &fakeWorkspaceClient{workspace: "node-js", status: tc.status}

			out := captureStdout(t, func() {
				require.NoError(t, cmd.Run(t.Context(), fake))
			})

			assert.Contains(t, out, tc.want)
		})
	}
}

func TestStatusCmd_JSONOutput(t *testing.T) {
	log.Init(log.Config{Verbosity: 0})

	cmd := &StatusCmd{
		GlobalFlags: &flags.GlobalFlags{ResultFormat: "json"},
	}
	fake := &fakeWorkspaceClient{
		workspace: "node-js",
		context:   "default",
		provider:  "docker",
		status:    client.StatusRunning,
	}

	out := captureStdout(t, func() {
		require.NoError(t, cmd.Run(t.Context(), fake))
	})

	var got client.WorkspaceStatus
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, client.WorkspaceStatus{
		ID:       "node-js",
		Context:  "default",
		Provider: "docker",
		State:    string(client.StatusRunning),
	}, got)
}
