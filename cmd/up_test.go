package cmd

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
		{name: "consistent", value: "consistent"},
		{name: "cached", value: "cached"},
		{name: "delegated", value: "delegated"},
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
		{name: "consistent", value: "consistent", wantErr: false},
		{name: "cached", value: "cached", wantErr: false},
		{name: "delegated", value: "delegated", wantErr: false},
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
