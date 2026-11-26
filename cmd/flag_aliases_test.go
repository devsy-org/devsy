package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/flags/names"
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

func TestLogFormatAlias_IsHidden(t *testing.T) {
	rootCmd, _ := BuildRoot()
	f := rootCmd.PersistentFlags().Lookup(names.LogFormat)
	require.NotNil(t, f)
	assert.True(t, f.Hidden, flagLogFormat+" alias should be hidden")
}
