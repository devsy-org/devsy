package cmd

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpCmd_ContainerDataFolderFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("container-data-folder")
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestUpCmd_ContainerDataFolderFlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--container-data-folder", "/tmp/data"})
	require.NoError(t, err)
	val, err := upCmd.Flags().GetString("container-data-folder")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/data", val)
}

func TestUpCmd_MountWorkspaceGitRootFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("mount-workspace-git-root")
	require.NotNil(t, flag)
	assert.Equal(t, "true", flag.DefValue)
}

func TestUpCmd_MountWorkspaceGitRootFlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--mount-workspace-git-root=false"})
	require.NoError(t, err)
	val, err := upCmd.Flags().GetBool("mount-workspace-git-root")
	require.NoError(t, err)
	assert.False(t, val)
}

func TestUpCmd_TerminalColumnsFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("terminal-columns")
	require.NotNil(t, flag)
	assert.Equal(t, "0", flag.DefValue)
}

func TestUpCmd_TerminalColumnsFlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--terminal-columns", "120"})
	require.NoError(t, err)
	val, err := upCmd.Flags().GetInt("terminal-columns")
	require.NoError(t, err)
	assert.Equal(t, 120, val)
}

func TestUpCmd_TerminalRowsFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("terminal-rows")
	require.NotNil(t, flag)
	assert.Equal(t, "0", flag.DefValue)
}

func TestUpCmd_TerminalRowsFlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--terminal-rows", "40"})
	require.NoError(t, err)
	val, err := upCmd.Flags().GetInt("terminal-rows")
	require.NoError(t, err)
	assert.Equal(t, 40, val)
}

func TestUpCmd_SkipPostCreateFlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{flagSkipPostCreate})
	require.NoError(t, err)
	val, err := upCmd.Flags().GetBool("skip-post-create")
	require.NoError(t, err)
	assert.True(t, val)
}

func TestUpCmd_SkipNonBlockingCommandsFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("skip-non-blocking-commands")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestUpCmd_SkipNonBlockingCommandsFlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--skip-non-blocking-commands"})
	require.NoError(t, err)
	val, err := upCmd.Flags().GetBool("skip-non-blocking-commands")
	require.NoError(t, err)
	assert.True(t, val)
}

func TestUpCmd_DotfilesTargetPathFlag(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	flag := upCmd.Flags().Lookup("dotfiles-target-path")
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestUpCmd_DotfilesTargetPathFlagParsesValue(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--dotfiles-target-path", "~/dotfiles"})
	require.NoError(t, err)
	val, err := upCmd.Flags().GetString("dotfiles-target-path")
	require.NoError(t, err)
	assert.Equal(t, "~/dotfiles", val)
}

func TestBuildCmd_LabelFlag(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	flag := buildCmd.Flags().Lookup("label")
	require.NotNil(t, flag)
}

func TestBuildCmd_LabelFlagParsesValue(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	labelVal := "org.opencontainers.image.source=https://github.com/example"
	err := buildCmd.ParseFlags([]string{"--label", labelVal})
	require.NoError(t, err)
	val, err := buildCmd.Flags().GetStringArray("label")
	require.NoError(t, err)
	assert.Equal(t, []string{labelVal}, val)
}

func TestBuildCmd_OutputFlag(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	flag := buildCmd.Flags().Lookup("output")
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestBuildCmd_OutputFlagParsesValue(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	err := buildCmd.ParseFlags([]string{"--output", "oci"})
	require.NoError(t, err)
	val, err := buildCmd.Flags().GetString("output")
	require.NoError(t, err)
	assert.Equal(t, "oci", val)
}

func TestBuildCmd_ExperimentalLockfileFlag(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	flag := buildCmd.Flags().Lookup("experimental-lockfile")
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestBuildCmd_ExperimentalLockfileFlagParsesValue(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	err := buildCmd.ParseFlags([]string{"--experimental-lockfile", "/path/to/lockfile"})
	require.NoError(t, err)
	val, err := buildCmd.Flags().GetString("experimental-lockfile")
	require.NoError(t, err)
	assert.Equal(t, "/path/to/lockfile", val)
}

func TestExecCmd_ContainerIDFlag(t *testing.T) {
	execCmd := NewExecCmd(&flags.GlobalFlags{})
	flag := execCmd.Flags().Lookup("container-id")
	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestExecCmd_ContainerIDFlagParsesValue(t *testing.T) {
	execCmd := NewExecCmd(&flags.GlobalFlags{})
	err := execCmd.ParseFlags([]string{"--container-id", "abc123"})
	require.NoError(t, err)
	val, err := execCmd.Flags().GetString("container-id")
	require.NoError(t, err)
	assert.Equal(t, "abc123", val)
}
