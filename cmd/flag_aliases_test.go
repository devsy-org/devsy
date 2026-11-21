package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	formatJSON = "json"
	flagHelp   = "--help"
)

func TestGlobalFlags_LogFormatAlias(t *testing.T) {
	rootCmd, globalFlags := BuildRoot()
	rootCmd.SetArgs([]string{flagLogFormat, formatJSON, "--version"})
	err := rootCmd.Execute()
	require.NoError(t, err)
	assert.Equal(t, formatJSON, globalFlags.LogOutput)
}

func TestGlobalFlags_NoStatus(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "default", args: []string{"ci", flagHelp}},
		{name: "before command", args: []string{"--no-status", "ci", flagHelp}, want: true},
		{name: "after command", args: []string{"ci", "--no-status", flagHelp}, want: true},
		{name: "explicit false", args: []string{"ci", "--no-status=false", flagHelp}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rootCmd, globalFlags := BuildRoot()
			var out bytes.Buffer
			rootCmd.SetOut(&out)
			rootCmd.SetErr(&out)
			rootCmd.SetArgs(tc.args)
			require.NoError(t, rootCmd.Execute())
			assert.Equal(t, tc.want, globalFlags.NoStatus)
			assert.Contains(t, out.String(), "--no-status")
			assert.Contains(
				t,
				strings.Join(strings.Fields(out.String()), " "),
				"Suppress live CLI progress messages; preserve results, errors, and recorded events",
			)
		})
	}
}

func TestLogFormat_IsVisible(t *testing.T) {
	rootCmd, _ := BuildRoot()
	f := rootCmd.PersistentFlags().Lookup(names.LogFormat)
	require.NotNil(t, f)
	assert.False(t, f.Hidden)
}

func TestCanonicalOutputEnvironmentAndExplicitFlags(t *testing.T) {
	t.Setenv(config.EnvLogLevel, "debug")
	t.Setenv(config.EnvLogFormat, logOutputLogfmt)
	t.Setenv(config.EnvResultFormat, "text")
	t.Setenv(config.EnvNoStatus, config.BoolTrue)
	root, globals := BuildRoot()
	assert.Equal(t, "debug", globals.LogLevel)
	assert.Equal(t, logOutputLogfmt, globals.LogOutput)
	assert.Equal(t, "text", globals.ResultFormat)
	assert.True(t, globals.NoStatus)
	root.SetArgs([]string{
		"--log-level=error", flagLogFormat + "=" + formatJSON,
		"--result-format=" + formatJSON, "--no-status=false", "--version",
	})
	require.NoError(t, root.Execute())
	assert.Equal(t, "error", globals.LogLevel)
	assert.Equal(t, formatJSON, globals.LogOutput)
	assert.Equal(t, formatJSON, globals.ResultFormat)
	assert.False(t, globals.NoStatus)
}

func TestCanonicalResultControlBridgesLegacyCommand(t *testing.T) {
	t.Setenv(config.EnvResultFormat, formatJSON)
	root, globals := BuildRoot()
	legacy := &cobra.Command{Use: "legacy-test"}
	legacyJSON := legacy.Flags().Bool(names.JSON, false, "Output JSON")
	require.NoError(t, applyOutputControls(legacy, globals, root.PersistentFlags()))
	assert.True(t, *legacyJSON)
	require.NoError(t, legacy.Flags().Set(names.JSON, config.BoolFalse))
	require.NoError(t, applyOutputControls(legacy, globals, root.PersistentFlags()))
	assert.False(t, *legacyJSON)
}
