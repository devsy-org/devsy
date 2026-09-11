package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/exitcode"
	"github.com/devsy-org/devsy/pkg/log"
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

func TestRenderCLIErrorRedactsEnvironmentSecrets(t *testing.T) {
	t.Setenv("DEVSY_ROOT_ERROR_SECRET", "root-error-secret-846302")
	r, w, err := os.Pipe()
	require.NoError(t, err)
	original := os.Stderr
	os.Stderr = w
	renderCLIError(&clierr.CLIError{
		Code:    clierr.CodeUnknown,
		Message: "failed with root-error-secret-846302",
		Hint:    "remove root-error-secret-846302 and retry",
		Context: map[string]string{"token": "root-error-secret-846302"},
	}, false)
	require.NoError(t, w.Close())
	os.Stderr = original
	data, readErr := io.ReadAll(r)
	require.NoError(t, readErr)
	output := string(data)
	assert.NotContains(t, output, "root-error-secret-846302")
	assert.Contains(t, output, "***")
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

func TestConfigureOutput_SelectsDiagnosticLogFormat(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	cases := []struct {
		name string
		flag string
		want func(t *testing.T, line string)
	}{
		{
			name: "json",
			flag: logOutputJSON,
			want: func(t *testing.T, line string) {
				var record map[string]any
				if err := json.Unmarshal([]byte(line), &record); err != nil {
					t.Fatalf("log line is not JSON: %v (%q)", err, line)
				}
			},
		},
		{
			name: "logfmt",
			flag: logOutputLogfmt,
			want: func(t *testing.T, line string) {
				if !strings.Contains(line, "level=info") || !strings.Contains(line, "msg=") {
					t.Fatalf("log line is not logfmt: %q", line)
				}
			},
		},
		{
			name: "text",
			flag: logOutputText,
			want: func(t *testing.T, line string) {
				if !strings.Contains(line, "INFO") || !strings.Contains(line, "root format test") {
					t.Fatalf("log line is not text: %q", line)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rootCmd, globalFlags := BuildRoot()
			globalFlags.Verbosity = 1
			os.Args = []string{"devsy", flagLogOutput, tc.flag}
			configureOutput(rootCmd, globalFlags, false)

			var sink bytes.Buffer
			remove := log.AddSink(&sink)
			defer remove()
			log.Infof("root format test")
			_ = log.Sync()
			lines := strings.Split(strings.TrimSpace(sink.String()), "\n")
			if len(lines) == 0 || lines[0] == "" {
				t.Fatalf("no diagnostic line captured: %q", sink.String())
			}
			tc.want(t, lines[len(lines)-1])
		})
	}
}

func TestConfigureOutput_DefaultMachineModeUsesJSONLogs(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })
	rootCmd, globalFlags := BuildRoot()
	globalFlags.Verbosity = 1
	os.Args = []string{"devsy", internalCommand}
	assert.True(t, configureOutput(rootCmd, globalFlags, true))

	var sink bytes.Buffer
	remove := log.AddSink(&sink)
	defer remove()
	log.Infof("machine default test")
	_ = log.Sync()

	line := strings.TrimSpace(sink.String())
	var record map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &record), "machine-mode default log: %q", line)
}
