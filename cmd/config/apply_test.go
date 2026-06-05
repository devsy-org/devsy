package config

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testContainerName = "my-container"
	testWorkspacePath = "/workspaces/project"
	testEnvFlag       = "-e"
	testEnvBaz        = "BAZ=qux"
	testEnvFoo        = "FOO=bar"
	testWorkdirFlag   = "--workdir"
	testCmdEcho       = "echo"
	testCmdHello      = "hello"
)

func TestNewApplyCmd_CommandName(t *testing.T) {
	cmd := NewApplyCmd(&flags.GlobalFlags{})
	assert.Equal(t, "apply", cmd.Use)
}

func TestNewApplyCmd_ContainerFlagRequired(t *testing.T) {
	cmd := NewApplyCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(flagApplyContainer)
	require.NotNil(t, f)

	annotations := cmd.MarkFlagRequired(flagApplyContainer)
	assert.NoError(t, annotations)
}

func TestNewApplyCmd_ConfigFlagOptional(t *testing.T) {
	cmd := NewApplyCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(flagApplyConfig)
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestNewApplyCmd_WorkspaceFolderFlagOptional(t *testing.T) {
	cmd := NewApplyCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(flagApplyWorkspaceFolder)
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestNewApplyCmd_DockerPathFlagOptional(t *testing.T) {
	cmd := NewApplyCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(flagApplyDockerPath)
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestNewApplyCmd_FailsWithoutContainer(t *testing.T) {
	cmd := NewApplyCmd(&flags.GlobalFlags{})
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "required flag")
}

func TestBuildContainerEnvArgs(t *testing.T) {
	env := map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	}
	args := buildContainerEnvArgs(env)
	expected := []string{testEnvFlag, testEnvBaz, testEnvFlag, testEnvFoo}
	assert.Equal(t, expected, args)
}

func TestBuildContainerEnvArgs_Empty(t *testing.T) {
	args := buildContainerEnvArgs(nil)
	assert.Nil(t, args)
}

func TestApplyCmd_ResolveDockerPath_Default(t *testing.T) {
	cmd := &ApplyCmd{GlobalFlags: &flags.GlobalFlags{}}
	assert.Equal(t, workspace.DefaultDockerCommand, cmd.resolveDockerPath())
}

func TestApplyCmd_ResolveDockerPath_Custom(t *testing.T) {
	cmd := &ApplyCmd{
		GlobalFlags: &flags.GlobalFlags{},
		DockerPath:  "/usr/local/bin/podman",
	}
	assert.Equal(t, "/usr/local/bin/podman", cmd.resolveDockerPath())
}
