package agentworkspace

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	"github.com/devsy-org/devsy/pkg/driver"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRunner struct {
	devcontainer.Runner

	got      devcontainer.CommandParams
	stdout   string
	stderr   string
	err      error
	commands int
}

func (f *fakeRunner) Command(_ context.Context, p devcontainer.CommandParams) error {
	f.commands++
	f.got = p
	_, _ = p.Stdout.Write([]byte(f.stdout))
	_, _ = p.Stderr.Write([]byte(f.stderr))

	return f.err
}

const testContextName = "default"

func testInfo() *provider2.AgentWorkspaceInfo {
	return &provider2.AgentWorkspaceInfo{
		Workspace: &provider2.Workspace{ID: "ws", UID: "uid-1", Context: testContextName},
	}
}

func newRelay(f *fakeRunner) (*setupRelay, *bytes.Buffer, *bytes.Buffer) {
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}

	return &setupRelay{
		runner: f, info: testInfo(), read: workspacelogs.ReadRequest{RunID: workspacelogs.NewID()},
		stdout: out, stderr: errOut,
	}, out, errOut
}

func TestSetupRelayIsExecOnlyAndRaw(t *testing.T) {
	f := &fakeRunner{stdout: `{"schemaVersion":1}`}
	relay, out, errOut := newRelay(f)

	require.NoError(t, relay.run(context.Background()))

	assert.True(t, f.got.RequireRunning)
	assert.True(t, f.got.RawStdout)
	assert.Equal(t, containerRootUser, f.got.User)
	assert.Equal(t, `{"schemaVersion":1}`, out.String())
	assert.Empty(t, errOut.String())
	assert.Contains(t, f.got.Command, "/usr/local/bin/devsy internal agent container setup-logs")
}

func TestSetupRelayReportsTypedReasons(t *testing.T) {
	tests := []struct {
		name   string
		runner *fakeRunner
		reason string
	}{
		{
			name:   "unsupported runtime",
			runner: &fakeRunner{err: devcontainer.ErrRequireRunningUnsupported},
			reason: workspacelogs.ReasonRefreshUnsupported,
		},
		{
			name:   "not running",
			runner: &fakeRunner{err: driver.ErrNotRunning},
			reason: workspacelogs.ReasonSourceOffline,
		},
		{
			name: "remote marker wins",
			runner: &fakeRunner{
				err:    errors.New("exit 1"),
				stderr: workspacelogs.FormatUnavailable(workspacelogs.ReasonNoSpool),
			},
			reason: workspacelogs.ReasonNoSpool,
		},
		{
			name:   "other failure",
			runner: &fakeRunner{err: errors.New("boom")},
			reason: workspacelogs.ReasonRefreshFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			relay, _, errOut := newRelay(tt.runner)

			err := relay.run(context.Background())

			require.Error(t, err)
			reason, ok := workspacelogs.ParseUnavailable(errOut.String())
			require.True(t, ok)
			assert.Equal(t, tt.reason, reason)
			assert.Equal(t, 1, tt.runner.commands, "no retry and no fallback command")
		})
	}
}

func TestSetupReaderCommandQuotesEveryValue(t *testing.T) {
	info := testInfo()
	info.Workspace.Context = "a b; rm -rf /"
	info.Workspace.UID = "$(touch /tmp/x)"
	info.Agent.Driver = provider2.KubernetesDriver
	info.Agent.Kubernetes.AgentInstallPath = "/home/dev bin/devsy"
	read := workspacelogs.ReadRequest{
		RunID: workspacelogs.NewID(), Hook: "postCreateCommand",
		InvocationID: workspacelogs.NewID(), After: "abc_-", Limit: 25,
	}

	cmd := setupReaderCommand(info, read)

	assert.True(
		t,
		strings.HasPrefix(cmd, "'/home/dev bin/devsy' internal agent container setup-logs"),
	)
	assert.Contains(t, cmd, "--context 'a b; rm -rf /'")
	assert.Contains(t, cmd, "--uid '$(touch /tmp/x)'")
	assert.Contains(t, cmd, "--hook postCreateCommand")
	assert.Contains(t, cmd, "--after abc_-")
	assert.Contains(t, cmd, "--limit 25")
}

func TestSetupSourceRejectsBadSelectorsBeforeAnyRead(t *testing.T) {
	for name, cmd := range map[string]*LogsCmd{
		"run":    {Source: sourceSetup, RunID: "../x"},
		"hook":   {Source: sourceSetup, RunID: workspacelogs.NewID(), Hook: "nope"},
		"cursor": {Source: sourceSetup, RunID: workspacelogs.NewID(), After: "a;b"},
		"limit":  {Source: sourceSetup, RunID: workspacelogs.NewID(), Limit: 99999},
	} {
		t.Run(name, func(t *testing.T) {
			cmd.GlobalFlags = &flags.GlobalFlags{AgentDir: t.TempDir(), Context: testContextName}
			cmd.ID = "ws"

			require.Error(t, cmd.Run(context.Background()))
		})
	}
}

func TestSetupSourceMissingWorkspaceInfoIsUnavailableNotProvisioned(t *testing.T) {
	dir := t.TempDir()
	cmd := &LogsCmd{
		GlobalFlags: &flags.GlobalFlags{AgentDir: dir, Context: testContextName},
		ID:          "ws", Source: sourceSetup, RunID: workspacelogs.NewID(),
	}

	require.Error(t, cmd.Run(context.Background()))
	// Nothing may be created for a history read.
	assert.NoDirExists(t, dir+"/contexts")
}
