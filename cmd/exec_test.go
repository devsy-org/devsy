package cmd

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRemoteEnv_Valid(t *testing.T) {
	cmd := &ExecCmd{
		GlobalFlags: &flags.GlobalFlags{},
		RemoteEnv:   []string{"FOO=bar", "BAZ=qux=extra"},
	}
	assert.NoError(t, cmd.validateRemoteEnv())
}

func TestValidateRemoteEnv_Empty(t *testing.T) {
	cmd := &ExecCmd{
		GlobalFlags: &flags.GlobalFlags{},
		RemoteEnv:   []string{},
	}
	assert.NoError(t, cmd.validateRemoteEnv())
}

func TestValidateRemoteEnv_MissingEquals(t *testing.T) {
	cmd := &ExecCmd{
		GlobalFlags: &flags.GlobalFlags{},
		RemoteEnv:   []string{"INVALID"},
	}
	err := cmd.validateRemoteEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be KEY=VALUE format")
}

func TestValidateRemoteEnv_EmptyKey(t *testing.T) {
	cmd := &ExecCmd{
		GlobalFlags: &flags.GlobalFlags{},
		RemoteEnv:   []string{"=value"},
	}
	err := cmd.validateRemoteEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be KEY=VALUE format")
}

func TestNewExecCmd_RequiresWorkspaceFolder(t *testing.T) {
	execCmd := NewExecCmd(&flags.GlobalFlags{})
	execCmd.SetArgs([]string{"--", "echo", "hello"})
	err := execCmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace-folder")
}

func TestNewExecCmd_RequiresArgs(t *testing.T) {
	execCmd := NewExecCmd(&flags.GlobalFlags{})
	execCmd.SetArgs([]string{"--workspace-folder", "/tmp/test"})
	err := execCmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires at least 1 arg")
}

func TestResolveDockerCommand_NilWorkspace(t *testing.T) {
	result := resolveDockerCommand(nil)
	assert.Equal(t, "docker", result)
}
