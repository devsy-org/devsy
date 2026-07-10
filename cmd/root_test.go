package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopLevelCommand(t *testing.T) {
	rootCmd, _ := BuildRoot()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"daemon stays in sandbox", []string{internalCommand, "daemon-local"}, internalCommand},
		{"nested internal command", []string{internalCommand, "ssh-server"}, internalCommand},
		{"workspace up routes to host", []string{cmdWorkspace, "up", "."}, cmdWorkspace},
		{"workspace ssh routes to host", []string{cmdWorkspace, "ssh", "my-ws"}, cmdWorkspace},
		{"provider list routes to host", []string{cmdProvider, cmdList}, cmdProvider},
		{"bare root", []string{}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found, _, err := rootCmd.Find(tc.args)
			require.NoError(t, err)
			assert.Equal(t, tc.want, topLevelCommand(found))
		})
	}
}

func TestLogOutputFromArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"absent defaults to text", []string{cmdProvider, cmdList}, logOutputText},
		{"space-separated log-output", []string{"up", flagLogOutput, logOutputJSON}, logOutputJSON},
		{
			"equals-form log-output",
			[]string{"up", flagLogOutput + "=" + logOutputJSON},
			logOutputJSON,
		},
		{"log-format alias", []string{"up", flagLogFormat, logOutputLogfmt}, logOutputLogfmt},
		{
			"equals-form log-format alias",
			[]string{flagLogFormat + "=" + logOutputJSON, "up"},
			logOutputJSON,
		},
		{
			"flag before unknown command",
			[]string{flagLogOutput, logOutputJSON, "bogus"},
			logOutputJSON,
		},
		{"trailing flag with no value", []string{"up", flagLogOutput}, logOutputText},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, logOutputFromArgs(tc.args))
		})
	}
}

func TestIsMachineLogFormat(t *testing.T) {
	assert.True(t, isMachineLogFormat(logOutputJSON))
	assert.True(t, isMachineLogFormat(logOutputLogfmt))
	assert.False(t, isMachineLogFormat(logOutputText))
	assert.False(t, isMachineLogFormat(""))
}

// TestConfigureOutput_SilencesCobra locks in the output contract: cobra prints
// its own error/usage only in interactive text mode. Machine formats and the
// internal subtree (which drives the agent protocol on stdout) must stay
// silent so cobra's usage text never corrupts the stream.
func TestConfigureOutput_SilencesCobra(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		isInternal bool
		wantSilent bool
	}{
		{"text interactive", []string{"up"}, false, false},
		{"json machine", []string{"up", flagLogOutput, logOutputJSON}, false, true},
		{"logfmt machine", []string{"up", flagLogOutput, logOutputLogfmt}, false, true},
		{"internal text stays silent", []string{internalCommand}, true, true},
		{
			"internal json stays silent",
			[]string{internalCommand, flagLogOutput, logOutputJSON},
			true,
			true,
		},
	}

	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rootCmd, globalFlags := BuildRoot()
			os.Args = append([]string{"devsy"}, tc.args...)
			configureOutput(rootCmd, globalFlags, tc.isInternal)
			assert.Equal(t, tc.wantSilent, rootCmd.SilenceErrors)
			assert.Equal(t, tc.wantSilent, rootCmd.SilenceUsage)
		})
	}
}
