package cmd

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const formatJSON = "json"

func TestGlobalFlags_LogFormatAlias(t *testing.T) {
	rootCmd, globalFlags := BuildRoot()
	rootCmd.SetArgs([]string{flagLogFormat, formatJSON, "--version"})
	err := rootCmd.Execute()
	require.NoError(t, err)
	assert.Equal(t, formatJSON, globalFlags.LogOutput)
}

func TestLogFormatAlias_IsHidden(t *testing.T) {
	rootCmd, _ := BuildRoot()
	f := rootCmd.PersistentFlags().Lookup(names.LogFormat)
	require.NotNil(t, f)
	assert.True(t, f.Hidden, flagLogFormat+" alias should be hidden")
}
