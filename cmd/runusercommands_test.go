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

func TestNewRunUserCommandsCmd_RequiresWorkspaceFolderOrContainerID(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "either --workspace-folder or --container-id must be provided")
}

func TestNewRunUserCommandsCmd_ContainerIDWithoutConfigFails(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	cmd.SetArgs([]string{"--container-id", "abc123"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(),
		"--config is required when --container-id is used without --workspace-folder")
}

func TestNewRunUserCommandsCmd_ContainerIDWithWorkspaceFolderNoConfigOK(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup("container-id")
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestNewRunUserCommandsCmd_IDLabelFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup("id-label")
	require.NotNil(t, f)
	assert.Equal(t, "stringArray", f.Value.Type())
}

func TestNewRunUserCommandsCmd_DockerPathFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup("docker-path")
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestNewRunUserCommandsCmd_ConfigFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup("config")
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestNewRunUserCommandsCmd_OverrideConfigFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup("override-config")
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestNewRunUserCommandsCmd_RemoteEnvFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup("remote-env")
	require.NotNil(t, f)
	assert.Equal(t, "stringArray", f.Value.Type())
}

func TestNewRunUserCommandsCmd_PrebuildFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup("prebuild")
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestNewRunUserCommandsCmd_SkipNonBlockingCommandsFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup("skip-non-blocking-commands")
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestRunUserCommandsCmd_SkipPostCreateFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup("skip-post-create")
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestRunUserCommandsCmd_SkipPostStartFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup("skip-post-start")
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestRunUserCommandsCmd_SkipPostAttachFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup("skip-post-attach")
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestRunUserCommandsCmd_SkipOnCreateFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup("skip-on-create")
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestRunUserCommandsCmd_SkipUpdateContentFlag(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	f := cmd.Flags().Lookup("skip-update-content")
	require.NotNil(t, f)
	assert.Equal(t, "false", f.DefValue)
}

func TestRunUserCommandsCmd_SkipFlagsParseValues(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	err := cmd.ParseFlags([]string{
		flagWorkspaceFolder, testTmpDir,
		flagSkipPostCreate,
		"--skip-post-start",
		"--skip-post-attach",
		"--skip-on-create",
		"--skip-update-content",
	})
	require.NoError(t, err)

	val, err := cmd.Flags().GetBool("skip-post-create")
	require.NoError(t, err)
	assert.True(t, val)

	val, err = cmd.Flags().GetBool("skip-post-start")
	require.NoError(t, err)
	assert.True(t, val)

	val, err = cmd.Flags().GetBool("skip-post-attach")
	require.NoError(t, err)
	assert.True(t, val)

	val, err = cmd.Flags().GetBool("skip-on-create")
	require.NoError(t, err)
	assert.True(t, val)

	val, err = cmd.Flags().GetBool("skip-update-content")
	require.NoError(t, err)
	assert.True(t, val)
}

func TestRunUserCommandsCmd_NewFlagsParseValues(t *testing.T) {
	cmd := NewRunUserCommandsCmd(&flags.GlobalFlags{})
	err := cmd.ParseFlags([]string{
		flagWorkspaceFolder, testTmpDir,
		"--docker-path", "/usr/local/bin/podman",
		"--config", ".devcontainer/devcontainer.json",
		"--override-config", "/tmp/override.json",
		"--remote-env", "FOO=bar",
		"--remote-env", "BAZ=qux",
		"--prebuild",
		"--skip-non-blocking-commands",
		"--container-id", "abc123",
	})
	require.NoError(t, err)

	dockerPath, err := cmd.Flags().GetString("docker-path")
	require.NoError(t, err)
	assert.Equal(t, "/usr/local/bin/podman", dockerPath)

	configPath, err := cmd.Flags().GetString("config")
	require.NoError(t, err)
	assert.Equal(t, ".devcontainer/devcontainer.json", configPath)

	overridePath, err := cmd.Flags().GetString("override-config")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/override.json", overridePath)

	remoteEnv, err := cmd.Flags().GetStringArray("remote-env")
	require.NoError(t, err)
	assert.Equal(t, []string{"FOO=bar", "BAZ=qux"}, remoteEnv)

	prebuild, err := cmd.Flags().GetBool("prebuild")
	require.NoError(t, err)
	assert.True(t, prebuild)

	skipNonBlocking, err := cmd.Flags().GetBool("skip-non-blocking-commands")
	require.NoError(t, err)
	assert.True(t, skipNonBlocking)

	containerID, err := cmd.Flags().GetString("container-id")
	require.NoError(t, err)
	assert.Equal(t, "abc123", containerID)
}

func TestRunUserCommandsCmd_ValidateRemoteEnv(t *testing.T) {
	tests := []struct {
		name      string
		env       []string
		wantErr   bool
		errSubstr string
	}{
		{"valid", []string{"FOO=bar", "BAZ=qux=extra"}, false, ""},
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
			&RunUserCommandsCmd{GlobalFlags: &flags.GlobalFlags{}, WorkspaceFolder: "/tmp"},
			false, "",
		},
		{
			"container-id with config",
			&RunUserCommandsCmd{
				GlobalFlags: &flags.GlobalFlags{},
				ContainerID: "abc",
				Config:      "path",
			},
			false,
			"",
		},
		{
			"container-id with workspace-folder",
			&RunUserCommandsCmd{
				GlobalFlags:     &flags.GlobalFlags{},
				ContainerID:     "abc",
				WorkspaceFolder: "/tmp",
			},
			false,
			"",
		},
		{
			"neither provided",
			&RunUserCommandsCmd{GlobalFlags: &flags.GlobalFlags{}},
			true, "either --workspace-folder or --container-id",
		},
		{
			"container-id without config or workspace",
			&RunUserCommandsCmd{GlobalFlags: &flags.GlobalFlags{}, ContainerID: "abc"},
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
		{"onCreateCommand", "onCreateCommand", 0},
		{"updateContentCommand", "updateContentCommand", 1},
		{"postCreateCommand", "postCreateCommand", 2},
		{"postStartCommand", "postStartCommand", 3},
		{"postAttachCommand", "postAttachCommand", 4},
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

func TestRunUserCommandsCmd_BuildCLIRemoteEnvArgs(t *testing.T) {
	cmd := &RunUserCommandsCmd{
		GlobalFlags: &flags.GlobalFlags{},
		RemoteEnv:   []string{"FOO=bar", "BAZ=qux"},
	}
	args := cmd.buildCLIRemoteEnvArgs()
	assert.Equal(t, []string{"-e", "FOO=bar", "-e", "BAZ=qux"}, args)
}

func TestRunUserCommandsCmd_BuildCLIRemoteEnvArgs_Empty(t *testing.T) {
	cmd := &RunUserCommandsCmd{
		GlobalFlags: &flags.GlobalFlags{},
		RemoteEnv:   []string{},
	}
	args := cmd.buildCLIRemoteEnvArgs()
	assert.Nil(t, args)
}
