package cmd

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
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

func TestNewSetUpCmd_CommandName(t *testing.T) {
	cmd := NewSetUpCmd(&flags.GlobalFlags{})
	assert.Equal(t, "set-up", cmd.Use)
}

func TestNewSetUpCmd_ContainerFlagRequired(t *testing.T) {
	cmd := NewSetUpCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(flagSetUpContainer)
	require.NotNil(t, f)

	annotations := cmd.MarkFlagRequired(flagSetUpContainer)
	assert.NoError(t, annotations)
}

func TestNewSetUpCmd_ConfigFlagOptional(t *testing.T) {
	cmd := NewSetUpCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(flagSetUpConfig)
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestNewSetUpCmd_WorkspaceFolderFlagOptional(t *testing.T) {
	cmd := NewSetUpCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(flagSetUpWorkspaceFolder)
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestNewSetUpCmd_DockerPathFlagOptional(t *testing.T) {
	cmd := NewSetUpCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(flagSetUpDockerPath)
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestNewSetUpCmd_FailsWithoutContainer(t *testing.T) {
	cmd := NewSetUpCmd(&flags.GlobalFlags{})
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

func TestBuildDockerExecArgs_SingleStringCommand(t *testing.T) {
	args := buildDockerExecArgs(dockerExecArgs{
		container:       testContainerName,
		envArgs:         []string{testEnvFlag, testEnvFoo},
		workspaceFolder: testWorkspacePath,
		command:         []string{"touch /tmp/test"},
	})
	expected := []string{
		dockerExecSubcommand, testEnvFlag, testEnvFoo,
		testWorkdirFlag, testWorkspacePath,
		testContainerName, "sh", "-c", "touch /tmp/test",
	}
	assert.Equal(t, expected, args)
}

func TestBuildDockerExecArgs_MultipleCommandParts(t *testing.T) {
	args := buildDockerExecArgs(dockerExecArgs{
		container:       testContainerName,
		workspaceFolder: testWorkspacePath,
		command:         []string{"ls", "-la", "/tmp"},
	})
	expected := []string{
		dockerExecSubcommand,
		testWorkdirFlag, testWorkspacePath,
		testContainerName, "ls", "-la", "/tmp",
	}
	assert.Equal(t, expected, args)
}

func TestBuildDockerExecArgs_NoWorkdir(t *testing.T) {
	args := buildDockerExecArgs(dockerExecArgs{
		container: testContainerName,
		command:   []string{testCmdEcho, testCmdHello},
	})
	expected := []string{
		dockerExecSubcommand,
		testContainerName, testCmdEcho, testCmdHello,
	}
	assert.Equal(t, expected, args)
}

func TestSetUpCmd_ResolveDockerPath_Default(t *testing.T) {
	cmd := &SetUpCmd{GlobalFlags: &flags.GlobalFlags{}}
	assert.Equal(t, defaultDockerCommand, cmd.resolveDockerPath())
}

func TestSetUpCmd_ResolveDockerPath_Custom(t *testing.T) {
	cmd := &SetUpCmd{
		GlobalFlags: &flags.GlobalFlags{},
		DockerPath:  "/usr/local/bin/podman",
	}
	assert.Equal(t, "/usr/local/bin/podman", cmd.resolveDockerPath())
}
