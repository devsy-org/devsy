package cmd

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRunUserCommandsCmd_CommandName(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	assert.Equal(t, "run-user-commands", cmd.Use)
}

func TestNewRunUserCommandsCmdAlias_IsHidden(t *testing.T) {
	cmd := NewRunUserCommandsCmdAlias(&flags.GlobalFlags{})
	assert.Equal(t, "runUserCommands", cmd.Use)
	assert.True(t, cmd.Hidden, "camelCase alias should be hidden")
}

func TestNewRunUserCommandsCmdAlias_RegisteredInRoot(t *testing.T) {
	rootCmd := BuildRoot()
	found := false
	for _, sub := range rootCmd.Commands() {
		if sub.Use == "runUserCommands" {
			found = true
			assert.True(t, sub.Hidden)
			break
		}
	}
	assert.True(t, found, "runUserCommands alias should be registered in root")
}

func TestNewRunUserCommandsCmd_WorkspaceFolderRequired(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "required flag")
}

func TestNewRunUserCommandsCmd_IDLabelFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup("id-label")
	require.NotNil(t, f)
	assert.Equal(t, "stringArray", f.Value.Type())
}

func TestBuildLifecycleEnvArgs_Nil(t *testing.T) {
	args := buildLifecycleEnvArgs(nil)
	assert.Nil(t, args)
}

func TestBuildLifecycleEnvArgs_NilMergedConfig(t *testing.T) {
	result := &devcconfig.Result{}
	args := buildLifecycleEnvArgs(result)
	assert.Nil(t, args)
}

func TestBuildLifecycleEnvArgs_EmptyEnv(t *testing.T) {
	result := &devcconfig.Result{
		MergedConfig: &devcconfig.MergedDevContainerConfig{
			DevContainerConfigBase: devcconfig.DevContainerConfigBase{
				RemoteEnv: map[string]*string{},
			},
		},
	}
	args := buildLifecycleEnvArgs(result)
	assert.Nil(t, args)
}

func TestBuildLifecycleEnvArgs_WithValues(t *testing.T) {
	val := "bar"
	result := &devcconfig.Result{
		MergedConfig: &devcconfig.MergedDevContainerConfig{
			DevContainerConfigBase: devcconfig.DevContainerConfigBase{
				RemoteEnv: map[string]*string{
					"FOO": &val,
				},
			},
		},
	}
	args := buildLifecycleEnvArgs(result)
	assert.Equal(t, []string{"-e", "FOO=bar"}, args)
}

func TestBuildLifecycleEnvArgs_NilValueSkipped(t *testing.T) {
	val := "keep"
	result := &devcconfig.Result{
		MergedConfig: &devcconfig.MergedDevContainerConfig{
			DevContainerConfigBase: devcconfig.DevContainerConfigBase{
				RemoteEnv: map[string]*string{
					"KEEP":   &val,
					"REMOVE": nil,
				},
			},
		},
	}
	args := buildLifecycleEnvArgs(result)
	assert.Contains(t, args, "-e")
	assert.Contains(t, args, "KEEP=keep")
	assert.NotContains(t, args, "REMOVE")
}

func TestRunUserCommandsCmd_RegisteredInRoot(t *testing.T) {
	rootCmd := BuildRoot()
	found := false
	for _, sub := range rootCmd.Commands() {
		if sub.Use == "run-user-commands" {
			found = true
			break
		}
	}
	assert.True(t, found, "run-user-commands should be registered in root")
}
