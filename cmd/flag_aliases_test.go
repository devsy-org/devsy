package cmd

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	flagConfig           = "--config"
	flagDevcontainerPath = "devcontainer-path"
	flagLogFormat        = "--log-format"
	formatJSON           = "json"
)

func TestUpCmd_ConfigAlias(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
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
	upCmd := NewUpCmd(&flags.GlobalFlags{})
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
