package cmd

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpCmd_ConfigAlias(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	err := upCmd.ParseFlags([]string{"--config", ".devcontainer/custom.json"})
	require.NoError(t, err)

	val, err := upCmd.Flags().GetString("devcontainer-path")
	require.NoError(t, err)
	assert.Equal(t, ".devcontainer/custom.json", val)
}

func TestBuildCmd_ConfigAlias(t *testing.T) {
	buildCmd := NewBuildCmd(&flags.GlobalFlags{})
	err := buildCmd.ParseFlags([]string{"--config", "path/to/devcontainer.json"})
	require.NoError(t, err)

	val, err := buildCmd.Flags().GetString("devcontainer-path")
	require.NoError(t, err)
	assert.Equal(t, "path/to/devcontainer.json", val)
}

func TestGlobalFlags_LogFormatAlias(t *testing.T) {
	rootCmd := BuildRoot()
	rootCmd.SetArgs([]string{"--log-format", "json", "version"})
	err := rootCmd.Execute()
	require.NoError(t, err)
	assert.Equal(t, "json", globalFlags.LogOutput)
}

func TestConfigAlias_IsHidden(t *testing.T) {
	upCmd := NewUpCmd(&flags.GlobalFlags{})
	f := upCmd.Flags().Lookup("config")
	require.NotNil(t, f)
	assert.True(t, f.Hidden, "--config alias should be hidden")
}

func TestLogFormatAlias_IsHidden(t *testing.T) {
	rootCmd := BuildRoot()
	f := rootCmd.PersistentFlags().Lookup("log-format")
	require.NotNil(t, f)
	assert.True(t, f.Hidden, "--log-format alias should be hidden")
}

func TestConfigAlias_E2E(t *testing.T) {
	rootCmd := BuildRoot()
	rootCmd.SetArgs([]string{"up", "--config", "/tmp/test.json", "--help"})
	err := rootCmd.Execute()
	require.NoError(t, err)
}
