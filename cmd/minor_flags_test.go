package cmd

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/workspace"
	"github.com/devsy-org/devsy/cmd/workspace/up"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpCmd_ContainerDataFolderFlag(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup(names.ContainerDataFolder)
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestUpCmd_ContainerDataFolderFlagParsesValue(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--container-data-folder", "/tmp/data"})
	require.NoError(t, err)
	val, err := upCmd.Flags().GetString(names.ContainerDataFolder)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/data", val)
}

func TestUpCmd_MountWorkspaceGitRootFlag(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup(names.MountWorkspaceGitRoot)
	require.NotNil(t, flag)
	assert.Equal(t, "true", flag.DefValue)
}

func TestUpCmd_MountWorkspaceGitRootFlagParsesValue(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--mount-workspace-git-root=false"})
	require.NoError(t, err)
	val, err := upCmd.Flags().GetBool(names.MountWorkspaceGitRoot)
	require.NoError(t, err)
	assert.False(t, val)
}

func TestUpCmd_TerminalColumnsFlag(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup(names.TerminalColumns)
	require.NotNil(t, flag)
	assert.Equal(t, "0", flag.DefValue)
}

func TestUpCmd_TerminalColumnsFlagParsesValue(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--terminal-columns", "120"})
	require.NoError(t, err)
	val, err := upCmd.Flags().GetInt(names.TerminalColumns)
	require.NoError(t, err)
	assert.Equal(t, 120, val)
}

func TestUpCmd_TerminalRowsFlag(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup(names.TerminalRows)
	require.NotNil(t, flag)
	assert.Equal(t, "0", flag.DefValue)
}

func TestUpCmd_TerminalRowsFlagParsesValue(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--terminal-rows", "40"})
	require.NoError(t, err)
	val, err := upCmd.Flags().GetInt(names.TerminalRows)
	require.NoError(t, err)
	assert.Equal(t, 40, val)
}

func TestUpCmd_SkipPostCreateFlagParsesValue(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--skip-post-create"})
	require.NoError(t, err)
	val, err := upCmd.Flags().GetBool(names.SkipPostCreate)
	require.NoError(t, err)
	assert.True(t, val)
}

func TestUpCmd_SkipNonBlockingCommandsFlag(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup(names.SkipNonBlockingCommands)
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestUpCmd_SkipNonBlockingCommandsFlagParsesValue(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--skip-non-blocking-commands"})
	require.NoError(t, err)
	val, err := upCmd.Flags().GetBool(names.SkipNonBlockingCommands)
	require.NoError(t, err)
	assert.True(t, val)
}

func TestUpCmd_DotfilesTargetPathFlag(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup(names.DotfilesTargetPath)
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestUpCmd_DotfilesTargetPathFlagParsesValue(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--dotfiles-target-path", "~/dotfiles"})
	require.NoError(t, err)
	val, err := upCmd.Flags().GetString(names.DotfilesTargetPath)
	require.NoError(t, err)
	assert.Equal(t, "~/dotfiles", val)
}

func TestBuildCmd_LabelFlag(t *testing.T) {
	buildCmd := workspace.NewBuildCmd(&flags.GlobalFlags{})
	flag := buildCmd.Flags().Lookup(names.Label)
	require.NotNil(t, flag)
}

func TestBuildCmd_LabelFlagParsesValue(t *testing.T) {
	buildCmd := workspace.NewBuildCmd(&flags.GlobalFlags{})
	labelVal := "org.opencontainers.image.source=https://github.com/example"
	err := buildCmd.ParseFlags([]string{"--label", labelVal})
	require.NoError(t, err)
	val, err := buildCmd.Flags().GetStringArray(names.Label)
	require.NoError(t, err)
	assert.Equal(t, []string{labelVal}, val)
}

func TestBuildCmd_OutputFlag(t *testing.T) {
	buildCmd := workspace.NewBuildCmd(&flags.GlobalFlags{})
	flag := buildCmd.Flags().Lookup(names.Output)
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestBuildCmd_OutputFlagParsesValue(t *testing.T) {
	buildCmd := workspace.NewBuildCmd(&flags.GlobalFlags{})
	err := buildCmd.ParseFlags([]string{"--output", "oci"})
	require.NoError(t, err)
	val, err := buildCmd.Flags().GetString(names.Output)
	require.NoError(t, err)
	assert.Equal(t, "oci", val)
}

func TestBuildCmd_ExperimentalLockfileFlag(t *testing.T) {
	buildCmd := workspace.NewBuildCmd(&flags.GlobalFlags{})
	flag := buildCmd.Flags().Lookup(names.ExperimentalLockfile)
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestBuildCmd_ExperimentalLockfileFlagParsesValue(t *testing.T) {
	buildCmd := workspace.NewBuildCmd(&flags.GlobalFlags{})
	err := buildCmd.ParseFlags([]string{"--experimental-lockfile", "/path/to/lockfile"})
	require.NoError(t, err)
	val, err := buildCmd.Flags().GetString(names.ExperimentalLockfile)
	require.NoError(t, err)
	assert.Equal(t, "/path/to/lockfile", val)
}

func TestExecCmd_ContainerIDFlag(t *testing.T) {
	execCmd := workspace.NewExecCmd(&flags.GlobalFlags{})
	flag := execCmd.Flags().Lookup(names.ContainerID)
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestExecCmd_ContainerIDFlagParsesValue(t *testing.T) {
	execCmd := workspace.NewExecCmd(&flags.GlobalFlags{})
	err := execCmd.ParseFlags([]string{"--container-id", "abc123"})
	require.NoError(t, err)
	val, err := execCmd.Flags().GetString(names.ContainerID)
	require.NoError(t, err)
	assert.Equal(t, "abc123", val)
}
