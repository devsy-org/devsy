package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	formatJSON = "json"
	flagHelp   = "--help"
)

func TestGlobalFlags_LogFormat(t *testing.T) {
	rootCmd, globalFlags := BuildRoot()
	rootCmd.SetArgs([]string{flagLogFormat, formatJSON, "--version"})
	err := rootCmd.Execute()
	require.NoError(t, err)
	assert.Equal(t, formatJSON, globalFlags.LogFormat)
}

func TestRemovedVerbosityShortFlagsRejectWithoutVersion(t *testing.T) {
	for _, arg := range []string{"-v", "-vv", "-vvv"} {
		t.Run(arg, func(t *testing.T) {
			t.Setenv(config.EnvHome, t.TempDir())
			root, _ := BuildRoot()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs([]string{arg})
			require.Error(t, root.Execute())
			assert.NotContains(t, out.String(), root.Version)
		})
	}
}

func TestGlobalFlags_CLIOverridesEnvironment(t *testing.T) {
	t.Setenv("DEVSY_LOG_LEVEL", "info")
	t.Setenv("DEVSY_LOG_FORMAT", "json")
	t.Setenv("DEVSY_RESULT_FORMAT", "json")
	t.Setenv("DEVSY_NO_STATUS", "true")
	root, global := BuildRoot()
	root.SetArgs([]string{
		"--log-level=trace",
		"--log-format=text",
		"--result-format=plain",
		"--no-status=false",
		"--version",
	})
	require.NoError(t, root.Execute())
	assert.Equal(t, "trace", global.LogLevel)
	assert.Equal(t, "text", global.LogFormat)
	assert.Equal(t, "plain", global.ResultFormat)
	assert.False(t, global.NoStatus)
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
