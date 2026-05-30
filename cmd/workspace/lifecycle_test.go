package workspace

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	lifecycleContainerName = "my-container"
	lifecycleWorkspacePath = "/workspaces/project"
	lifecycleEnvFlag       = "-e"
	lifecycleEnvFoo        = "FOO=bar"
	lifecycleWorkdirFlag   = "--workdir"
	lifecycleCmdEcho       = "echo"
	lifecycleCmdHello      = "hello"
)

func TestBuildDockerExecArgs_SingleStringCommand(t *testing.T) {
	args := BuildDockerExecArgs(DockerExecArgs{
		Container:       lifecycleContainerName,
		EnvArgs:         []string{lifecycleEnvFlag, lifecycleEnvFoo},
		WorkspaceFolder: lifecycleWorkspacePath,
		Command:         []string{"touch /tmp/test"},
	})
	expected := []string{
		DockerExecSubcommand, lifecycleEnvFlag, lifecycleEnvFoo,
		lifecycleWorkdirFlag, lifecycleWorkspacePath,
		lifecycleContainerName, "sh", "-c", "touch /tmp/test",
	}
	assert.Equal(t, expected, args)
}

func TestBuildDockerExecArgs_MultipleCommandParts(t *testing.T) {
	args := BuildDockerExecArgs(DockerExecArgs{
		Container:       lifecycleContainerName,
		WorkspaceFolder: lifecycleWorkspacePath,
		Command:         []string{"ls", "-la", "/tmp"},
	})
	expected := []string{
		DockerExecSubcommand,
		lifecycleWorkdirFlag, lifecycleWorkspacePath,
		lifecycleContainerName, "ls", "-la", "/tmp",
	}
	assert.Equal(t, expected, args)
}

func TestBuildDockerExecArgs_NoWorkdir(t *testing.T) {
	args := BuildDockerExecArgs(DockerExecArgs{
		Container: lifecycleContainerName,
		Command:   []string{lifecycleCmdEcho, lifecycleCmdHello},
	})
	expected := []string{
		DockerExecSubcommand,
		lifecycleContainerName, lifecycleCmdEcho, lifecycleCmdHello,
	}
	assert.Equal(t, expected, args)
}

func TestBuildDockerExecArgs_WithUser(t *testing.T) {
	args := BuildDockerExecArgs(DockerExecArgs{
		Container:       lifecycleContainerName,
		User:            "vscode",
		WorkspaceFolder: lifecycleWorkspacePath,
		Command:         []string{lifecycleCmdEcho, lifecycleCmdHello},
	})
	expected := []string{
		DockerExecSubcommand,
		lifecycleWorkdirFlag, lifecycleWorkspacePath,
		"--user", "vscode",
		lifecycleContainerName, lifecycleCmdEcho, lifecycleCmdHello,
	}
	assert.Equal(t, expected, args)
}
