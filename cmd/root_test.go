package cmd

import (
	"fmt"
	"os"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/exitcode"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExitCodeForError_WorkspaceNotFound(t *testing.T) {
	err := fmt.Errorf("get workspace: %w", workspace.ErrWorkspaceNotFound)
	assert.Equal(t, exitcode.Retryable, exitCodeForError(err, true))
}

func TestExitCodeForError_GenericFailure(t *testing.T) {
	assert.Equal(t, exitcode.Failure, exitCodeForError(fmt.Errorf("boom"), true))
}

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
		{"absent returns empty", []string{cmdProvider, cmdList}, ""},
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
		{"trailing flag with no value returns empty", []string{"up", flagLogOutput}, ""},
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

func TestIsMachineConsumer(t *testing.T) {
	cases := []struct {
		name       string
		logOutput  string
		isInternal bool
		devsyUI    string
		want       bool
	}{
		{"internal subtree", "", true, "", true},
		{"desktop provenance", "", false, config.BoolTrue, true},
		{"explicit json", logOutputJSON, false, "", true},
		{"explicit logfmt", logOutputLogfmt, false, "", true},
		{"explicit text is human", logOutputText, false, "", false},
		{"internal wins over text", logOutputText, true, "", true},
		{"desktop wins over text", logOutputText, false, config.BoolTrue, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.EnvUI, tc.devsyUI)
			assert.Equal(t, tc.want, isMachineConsumer(tc.logOutput, tc.isInternal))
		})
	}
}

func TestIsMachineConsumer_TTYFallback(t *testing.T) {
	t.Setenv(config.EnvUI, "")
	assert.True(t, isMachineConsumer("", false),
		"non-terminal stderr with no explicit format should be machine mode")
}

func TestConfigureOutput_SilencesCobra(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		isInternal bool
		wantSilent bool
	}{
		{"json machine", []string{"up", flagLogOutput, logOutputJSON}, false, true},
		{"logfmt machine", []string{"up", flagLogOutput, logOutputLogfmt}, false, true},
		{"explicit text is human", []string{"up", flagLogOutput, logOutputText}, false, false},
		{"internal stays silent", []string{internalCommand}, true, true},
	}

	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.EnvUI, "")
			rootCmd, globalFlags := BuildRoot()
			os.Args = append([]string{"devsy"}, tc.args...)
			machineMode := configureOutput(rootCmd, globalFlags, tc.isInternal)
			assert.Equal(t, tc.wantSilent, machineMode)
			assert.True(t, rootCmd.SilenceErrors)
			assert.Equal(t, tc.wantSilent, rootCmd.SilenceUsage)
		})
	}
}
