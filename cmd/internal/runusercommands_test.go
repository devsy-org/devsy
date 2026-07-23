package cmdinternal

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/workspace"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	flagContainerID    = "--container-id"
	testContainerID    = "abc"
	testContainerIDHex = "abc123"
	testEnvFoo         = "FOO=bar"
	testEnvBaz         = "BAZ=qux"

	hookOnCreate      = "onCreateCommand"
	hookUpdateContent = "updateContentCommand"
	hookPostCreate    = "postCreateCommand"
	hookPostStart     = "postStartCommand"
	hookPostAttach    = "postAttachCommand"

	tmpDir = "/tmp"
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

func TestNewRunUserCommandsCmdAlias_RegisteredInInternal(t *testing.T) {
	internalCmd := NewInternalCmd(&flags.GlobalFlags{})
	found := false
	for _, sub := range internalCmd.Commands() {
		if sub.Use == "runUserCommands" {
			found = true
			assert.True(t, sub.Hidden)
			break
		}
	}
	assert.True(t, found, "runUserCommands alias should be registered under internal")
}

func TestNewRunUserCommandsCmd_RequiresWorkspaceFolderOrContainerID(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"at least one of the flags in the group [workspace-folder container-id] is required")
}

func TestNewRunUserCommandsCmd_ContainerIDWithoutConfigFails(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	cmd.SetArgs([]string{flagContainerID, testContainerIDHex})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"--config is required when --container-id is used without --workspace-folder")
}

func TestNewRunUserCommandsCmd_ContainerIDFlagExists(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(names.ContainerID)
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestNewRunUserCommandsCmd_IDLabelFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(names.IDLabel)
	require.NotNil(t, f)
	assert.Equal(t, "stringArray", f.Value.Type())
}

func TestNewRunUserCommandsCmd_DockerPathFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(names.DockerPath)
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestNewRunUserCommandsCmd_ConfigFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(names.Config)
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestNewRunUserCommandsCmd_OverrideConfigFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(names.OverrideConfig)
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestNewRunUserCommandsCmd_RemoteEnvFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(names.RemoteEnv)
	require.NotNil(t, f)
	assert.Equal(t, "stringArray", f.Value.Type())
}

func TestNewRunUserCommandsCmd_PrebuildFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(names.Prebuild)
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestNewRunUserCommandsCmd_SkipNonBlockingCommandsFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(names.SkipNonBlockingCommands)
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestRunUserCommandsCmd_SkipPostCreateFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(names.SkipPostCreate)
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestRunUserCommandsCmd_SkipPostStartFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(names.SkipPostStart)
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestRunUserCommandsCmd_SkipPostAttachFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(names.SkipPostAttach)
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestRunUserCommandsCmd_SkipOnCreateFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(names.SkipOnCreate)
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestRunUserCommandsCmd_SkipUpdateContentFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup(names.SkipUpdateContent)
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestRunUserCommandsCmd_SkipFlagsParseValues(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	err := cmd.ParseFlags([]string{
		"--workspace-folder", tmpDir,
		"--skip-post-create",
		"--skip-post-start",
		"--skip-post-attach",
		"--skip-on-create",
		"--skip-update-content",
	})
	require.NoError(t, err)

	val, err := cmd.Flags().GetBool(names.SkipPostCreate)
	require.NoError(t, err)
	assert.True(t, val)

	val, err = cmd.Flags().GetBool(names.SkipPostStart)
	require.NoError(t, err)
	assert.True(t, val)

	val, err = cmd.Flags().GetBool(names.SkipPostAttach)
	require.NoError(t, err)
	assert.True(t, val)

	val, err = cmd.Flags().GetBool(names.SkipOnCreate)
	require.NoError(t, err)
	assert.True(t, val)

	val, err = cmd.Flags().GetBool(names.SkipUpdateContent)
	require.NoError(t, err)
	assert.True(t, val)
}

func TestRunUserCommandsCmd_NewFlagsParseValues(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	err := cmd.ParseFlags([]string{
		"--workspace-folder", tmpDir,
		"--docker-path", "/usr/local/bin/podman",
		"--config", ".devcontainer/devcontainer.json",
		"--override-config", "/tmp/override.json",
		"--remote-env", testEnvFoo,
		"--remote-env", testEnvBaz,
		"--prebuild",
		"--skip-non-blocking-commands",
		flagContainerID, testContainerIDHex,
	})
	require.NoError(t, err)

	dockerPath, err := cmd.Flags().GetString(names.DockerPath)
	require.NoError(t, err)
	assert.Equal(t, "/usr/local/bin/podman", dockerPath)

	configPath, err := cmd.Flags().GetString(names.Config)
	require.NoError(t, err)
	assert.Equal(t, ".devcontainer/devcontainer.json", configPath)

	overridePath, err := cmd.Flags().GetString(names.OverrideConfig)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/override.json", overridePath)

	remoteEnv, err := cmd.Flags().GetStringArray(names.RemoteEnv)
	require.NoError(t, err)
	assert.Equal(t, []string{testEnvFoo, testEnvBaz}, remoteEnv)

	prebuild, err := cmd.Flags().GetBool(names.Prebuild)
	require.NoError(t, err)
	assert.True(t, prebuild)

	skipNonBlocking, err := cmd.Flags().GetBool(names.SkipNonBlockingCommands)
	require.NoError(t, err)
	assert.True(t, skipNonBlocking)

	containerID, err := cmd.Flags().GetString(names.ContainerID)
	require.NoError(t, err)
	assert.Equal(t, testContainerIDHex, containerID)
}

func TestRunUserCommandsCmd_ValidateRemoteEnv(t *testing.T) {
	tests := []struct {
		name      string
		env       []string
		wantErr   bool
		errSubstr string
	}{
		{"valid", []string{testEnvFoo, "BAZ=qux=extra"}, false, ""},
		{"empty", []string{}, false, ""},
		{"missing equals", []string{"INVALID"}, true, "must be KEY=VALUE format"},
		{"empty key", []string{"=value"}, true, "must be KEY=VALUE format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &RunUserCommandsCmd{
				GlobalFlags: &flags.GlobalFlags{},
				RemoteEnv:   tt.env,
			}
			err := cmd.validateRemoteEnv()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRunUserCommandsCmd_Validate(t *testing.T) {
	tests := []struct {
		name      string
		cmd       *RunUserCommandsCmd
		wantErr   bool
		errSubstr string
	}{
		{
			"workspace-folder only",
			&RunUserCommandsCmd{GlobalFlags: &flags.GlobalFlags{}, WorkspaceFolder: tmpDir},
			false, "",
		},
		{
			"container-id with config",
			&RunUserCommandsCmd{
				GlobalFlags: &flags.GlobalFlags{},
				ContainerID: testContainerID,
				Config:      "path",
			},
			false,
			"",
		},
		{
			"container-id with workspace-folder",
			&RunUserCommandsCmd{
				GlobalFlags:     &flags.GlobalFlags{},
				ContainerID:     testContainerID,
				WorkspaceFolder: tmpDir,
			},
			false,
			"",
		},
		{
			"container-id without config or workspace",
			&RunUserCommandsCmd{GlobalFlags: &flags.GlobalFlags{}, ContainerID: testContainerID},
			true, "--config is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cmd.validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestResolveWaitForBoundary(t *testing.T) {
	tests := []struct {
		name    string
		waitFor string
		want    int
	}{
		{"default (empty)", "", 1},
		{hookOnCreate, hookOnCreate, 0},
		{hookUpdateContent, hookUpdateContent, 1},
		{hookPostCreate, hookPostCreate, 2},
		{hookPostStart, hookPostStart, 3},
		{hookPostAttach, hookPostAttach, 4},
		{"unknown falls back to 1", "unknownHook", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &devcconfig.Result{
				MergedConfig: &devcconfig.MergedDevContainerConfig{
					DevContainerConfigBase: devcconfig.DevContainerConfigBase{
						WaitFor: tt.waitFor,
					},
				},
			}
			got := resolveWaitForBoundary(result)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveWaitForBoundary_NilResult(t *testing.T) {
	assert.Equal(t, 1, resolveWaitForBoundary(nil))
}

func TestBuildLifecycleEnvArgs_Nil(t *testing.T) {
	args := workspace.BuildLifecycleEnvArgs(nil)
	assert.Nil(t, args)
}

func TestBuildLifecycleEnvArgs_NilMergedConfig(t *testing.T) {
	result := &devcconfig.Result{}
	args := workspace.BuildLifecycleEnvArgs(result)
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
	args := workspace.BuildLifecycleEnvArgs(result)
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
	args := workspace.BuildLifecycleEnvArgs(result)
	assert.Equal(t, []string{"-e", testEnvFoo}, args)
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
	args := workspace.BuildLifecycleEnvArgs(result)
	assert.Contains(t, args, "-e")
	assert.Contains(t, args, "KEEP=keep")
	assert.NotContains(t, args, "REMOVE")
}

func TestRunUserCommandsCmd_RegisteredInInternal(t *testing.T) {
	internalCmd := NewInternalCmd(&flags.GlobalFlags{})
	found := false
	for _, sub := range internalCmd.Commands() {
		if sub.Use == "run-user-commands" {
			found = true
			break
		}
	}
	assert.True(t, found, "run-user-commands should be registered under internal")
}

func TestRunUserCommandsCmd_BuildCLIRemoteEnvArgs(t *testing.T) {
	cmd := &RunUserCommandsCmd{
		GlobalFlags: &flags.GlobalFlags{},
		RemoteEnv:   []string{testEnvFoo, testEnvBaz},
	}
	args := cmd.buildCLIRemoteEnvArgs()
	assert.Equal(t, []string{"-e", testEnvFoo, "-e", testEnvBaz}, args)
}

func TestRunUserCommandsCmd_BuildCLIRemoteEnvArgs_Empty(t *testing.T) {
	cmd := &RunUserCommandsCmd{
		GlobalFlags: &flags.GlobalFlags{},
		RemoteEnv:   []string{},
	}
	args := cmd.buildCLIRemoteEnvArgs()
	assert.Nil(t, args)
}
