package up

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const probeNone = "none"

func TestUpCmd_ValidateDefaultUserEnvProbe(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "empty is valid", value: "", wantErr: false},
		{name: "none", value: probeNone, wantErr: false},
		{name: "loginShell", value: "loginShell", wantErr: false},
		{name: "interactiveShell", value: "interactiveShell", wantErr: false},
		{name: "loginInteractiveShell", value: "loginInteractiveShell", wantErr: false},
		{name: "invalid value", value: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &UpCmd{
				GlobalFlags: &flags.GlobalFlags{},
			}
			cmd.DefaultUserEnvProbe = tt.value
			err := cmd.validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid userEnvProbe")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestUpCmd_FlagRegistered(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("default-user-env-probe")
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestUpCmd_FlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--default-user-env-probe", probeNone})
	require.NoError(t, err)

	flag := upCmd.Flags().Lookup("default-user-env-probe")
	assert.Equal(t, probeNone, flag.Value.String())
}

func TestUpCmd_WorkspaceMountConsistencyFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("workspace-mount-consistency")
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestUpCmd_WorkspaceMountConsistencyFlagParsesValue(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "consistent", value: MountConsistencyConsistent},
		{name: "cached", value: MountConsistencyCached},
		{name: "delegated", value: MountConsistencyDelegated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upCmd := NewUpCmd(&flags.GlobalFlags{})
			err := upCmd.ParseFlags([]string{"--workspace-mount-consistency", tt.value})
			require.NoError(t, err)

			flag := upCmd.Flags().Lookup("workspace-mount-consistency")
			assert.Equal(t, tt.value, flag.Value.String())
		})
	}
}

func TestUpCmd_ValidateWorkspaceMountConsistency(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "empty is valid", value: "", wantErr: false},
		{name: "consistent", value: MountConsistencyConsistent, wantErr: false},
		{name: "cached", value: MountConsistencyCached, wantErr: false},
		{name: "delegated", value: MountConsistencyDelegated, wantErr: false},
		{name: "invalid value", value: "bogus", wantErr: true},
		{name: "partial match", value: "cache", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
			cmd.WorkspaceMountConsistency = tt.value
			err := cmd.validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid --workspace-mount-consistency")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestUpCmd_SkipPostCreateFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("skip-post-create")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestUpCmd_SkipPostStartFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("skip-post-start")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestUpCmd_SkipPostAttachFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("skip-post-attach")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestUpCmd_SkipFlagsParseValues(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{
		"--skip-post-create",
		"--skip-post-start",
		"--skip-post-attach",
	})
	require.NoError(t, err)

	val, err := upCmd.Flags().GetBool("skip-post-create")
	require.NoError(t, err)
	assert.True(t, val)

	val, err = upCmd.Flags().GetBool("skip-post-start")
	require.NoError(t, err)
	assert.True(t, val)

	val, err = upCmd.Flags().GetBool("skip-post-attach")
	require.NoError(t, err)
	assert.True(t, val)
}

func TestUpCmd_ContainerUserFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("container-user")
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestUpCmd_ContainerUserFlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--container-user", "devuser"})
	require.NoError(t, err)

	flag := upCmd.Flags().Lookup("container-user")
	assert.Equal(t, "devuser", flag.Value.String())
}

func TestUpCmd_RemoteUserFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("remote-user")
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestUpCmd_RemoteUserFlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--remote-user", "vscode"})
	require.NoError(t, err)

	flag := upCmd.Flags().Lookup("remote-user")
	assert.Equal(t, "vscode", flag.Value.String())
}
