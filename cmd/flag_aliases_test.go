package cmd

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/up"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	flagConfig                  = "--config"
	flagDevcontainerPath        = "devcontainer-path"
	flagLogFormat               = "--log-format"
	flagOverrideConfig          = "--override-config"
	flagExtraDevContainerPath   = "extra-devcontainer-path"
	flagDotfilesRepository      = "--dotfiles-repository"
	flagDotfiles                = "dotfiles"
	flagRemoveExistingContainer = "--remove-existing-container"
	flagRecreate                = "recreate"
	formatJSON                  = "json"
)

func TestUpCmd_ConfigAlias(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{flagConfig, ".devcontainer/custom.json"})
	require.NoError(t, err)

	val, err := upCmd.Flags().GetString(flagDevcontainerPath)
	require.NoError(t, err)
	assert.Equal(t, ".devcontainer/custom.json", val)
}

func TestBuildCmd_ConfigAlias(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	err := buildCmd.ParseFlags([]string{flagConfig, "path/to/devcontainer.json"})
	require.NoError(t, err)

	val, err := buildCmd.Flags().GetString(flagDevcontainerPath)
	require.NoError(t, err)
	assert.Equal(t, "path/to/devcontainer.json", val)
}

func TestGlobalFlags_LogFormatAlias(t *testing.T) {
	rootCmd := BuildRoot()
	rootCmd.SetArgs([]string{flagLogFormat, formatJSON, "version"})
	err := rootCmd.Execute()
	require.NoError(t, err)
	assert.Equal(t, formatJSON, globalFlags.LogOutput)
}

func TestConfigAlias_IsHidden(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	f := upCmd.Flags().Lookup("config")
	require.NotNil(t, f)
	assert.True(t, f.Hidden, flagConfig+" alias should be hidden")
}

func TestLogFormatAlias_IsHidden(t *testing.T) {
	rootCmd := BuildRoot()
	f := rootCmd.PersistentFlags().Lookup("log-format")
	require.NotNil(t, f)
	assert.True(t, f.Hidden, flagLogFormat+" alias should be hidden")
}

func TestConfigAlias_E2E(t *testing.T) {
	rootCmd := BuildRoot()
	rootCmd.SetArgs([]string{"up", flagConfig, "/tmp/test.json", "--help"})
	err := rootCmd.Execute()
	require.NoError(t, err)
}

func TestUpCmd_OverrideConfigAlias(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{flagOverrideConfig, "/tmp/override.json"})
	require.NoError(t, err)

	val, err := upCmd.Flags().GetString(flagExtraDevContainerPath)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/override.json", val)
}

func TestOverrideConfigAlias_IsHidden(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	f := upCmd.Flags().Lookup("override-config")
	require.NotNil(t, f)
	assert.True(t, f.Hidden, flagOverrideConfig+" alias should be hidden")
}

func TestUpCmd_DotfilesRepositoryAlias(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{flagDotfilesRepository, "https://github.com/user/dotfiles"})
	require.NoError(t, err)

	val, err := upCmd.Flags().GetString(flagDotfiles)
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/user/dotfiles", val)
}

func TestDotfilesRepositoryAlias_IsHidden(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	f := upCmd.Flags().Lookup("dotfiles-repository")
	require.NotNil(t, f)
	assert.True(t, f.Hidden, flagDotfilesRepository+" alias should be hidden")
}

func TestUpCmd_RemoveExistingContainerAlias(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{flagRemoveExistingContainer})
	require.NoError(t, err)

	val, err := upCmd.Flags().GetBool(flagRecreate)
	require.NoError(t, err)
	assert.True(t, val)
}

func TestRemoveExistingContainerAlias_IsHidden(t *testing.T) {
	upCmd := up.NewUpCmd(&flags.GlobalFlags{})
	f := upCmd.Flags().Lookup("remove-existing-container")
	require.NotNil(t, f)
	assert.True(t, f.Hidden, flagRemoveExistingContainer+" alias should be hidden")
}

func TestGlobalFlags_OutputFormat(t *testing.T) {
	rootCmd := BuildRoot()
	rootCmd.SetArgs([]string{"--output-format", formatJSON, "version"})
	err := rootCmd.Execute()
	require.NoError(t, err)
	assert.Equal(t, formatJSON, globalFlags.OutputFormat)
}

func TestGlobalFlags_OutputFormatDefault(t *testing.T) {
	rootCmd := BuildRoot()
	rootCmd.SetArgs([]string{"version"})
	err := rootCmd.Execute()
	require.NoError(t, err)
	assert.Equal(t, "", globalFlags.OutputFormat)
}
