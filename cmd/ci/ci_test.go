package ci

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	cmdMake = "make"
	cmdTest = "test"
	cmdEcho = "echo"
)

func TestValidateEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     []string
		wantErr bool
	}{
		{name: "valid", env: []string{"FOO=bar", "BAZ=qux=extra"}, wantErr: false},
		{name: "empty slice", env: nil, wantErr: false},
		{name: "missing equals", env: []string{"INVALID"}, wantErr: true},
		{name: "empty key", env: []string{"=value"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &CICmd{GlobalFlags: &flags.GlobalFlags{}, RemoteEnv: tt.env}
			err := cmd.validateEnv()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "must be KEY=VALUE format")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func runSplit(t *testing.T, args []string, runCmdString string) (string, []string, error) {
	t.Helper()
	var (
		gotSource string
		gotCmd    []string
		gotErr    error
	)
	cobraCmd := &cobra.Command{
		Use: "ci",
		RunE: func(c *cobra.Command, a []string) error {
			gotSource, gotCmd, gotErr = splitArgs(c, a, runCmdString)
			return nil
		},
	}
	cobraCmd.SetArgs(args)
	require.NoError(t, cobraCmd.Execute())
	return gotSource, gotCmd, gotErr
}

func TestSplitArgs_CommandAfterDash(t *testing.T) {
	source, cmd, err := runSplit(t, []string{"--", cmdMake, cmdTest}, "")
	require.NoError(t, err)
	assert.Empty(t, source)
	assert.Equal(t, []string{cmdMake, cmdTest}, cmd)
}

func TestSplitArgs_SourceAndCommand(t *testing.T) {
	source, cmd, err := runSplit(t, []string{"my-ws", "--", cmdEcho, "hi"}, "")
	require.NoError(t, err)
	assert.Equal(t, "my-ws", source)
	assert.Equal(t, []string{cmdEcho, "hi"}, cmd)
}

func TestSplitArgs_RunCmdWrapsInShell(t *testing.T) {
	source, cmd, err := runSplit(t, nil, "make ci")
	require.NoError(t, err)
	assert.Empty(t, source)
	assert.Equal(t, []string{"sh", "-c", "make ci"}, cmd)
}

func TestSplitArgs_MissingCommand(t *testing.T) {
	_, _, err := runSplit(t, []string{"my-ws"}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a command to run is required")
}

func TestSplitArgs_DashAndRunCmdConflict(t *testing.T) {
	_, _, err := runSplit(t, []string{"--", cmdMake, cmdTest}, "make ci")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not both")
}

func TestSplitArgs_TooManySources(t *testing.T) {
	_, _, err := runSplit(t, []string{"ws-one", "ws-two", "--", cmdEcho}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at most one workspace source")
}

func TestNewCICmd_Flags(t *testing.T) {
	ciCmd := NewCICmd(&flags.GlobalFlags{})
	for _, name := range []string{
		"run-cmd", "remote-env", "keep", "devcontainer", "no-cache", "platform",
		"cache-from", "workspace-env", "workspace-env-file", "init-env",
		"secrets-file", "feature-secrets-file", "features",
		"secret", "env", "build-secret", "git-token", "git-token-username",
	} {
		assert.NotNil(t, ciCmd.Flags().Lookup(name), "expected flag %q to be registered", name)
	}
	assert.Equal(t, "false", ciCmd.Flags().Lookup("keep").DefValue)
}
