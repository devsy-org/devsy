package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/exitcode"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	devsy                   = "devsy"
	testInvalidValue        = "invalid"
	testInvalidLevelMessage = "invalid log level"
)

type payloadLevelCase struct {
	name       string
	payload    string
	env        string
	argument   string
	want       string
	wantErr    string
	fromSender bool
}

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
		Context: map[string]string{ //nolint:gosec // test credential fixture
			"token": "root-error-secret-846302",
		},
	}, false)
	require.NoError(t, w.Close())
	os.Stderr = original
	data, readErr := io.ReadAll(r)
	require.NoError(t, readErr)
	output := string(data)
	assert.NotContains(t, output, "root-error-secret-846302")
	assert.Contains(t, output, "***")
}

func TestRenderCLIErrorMachineModeWritesDirectEnvelope(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	original := os.Stderr
	os.Stderr = w
	renderCLIError(&clierr.CLIError{Code: clierr.CodeUnknown, Message: "machine failure"}, true)
	require.NoError(t, w.Close())
	os.Stderr = original
	var envelope struct {
		Kind    string `json:"kind"`
		Outcome string `json:"outcome"`
		Message string `json:"message"`
	}
	require.NoError(t, json.NewDecoder(r).Decode(&envelope))
	assert.Equal(t, "error", envelope.Kind)
	assert.Equal(t, "error", envelope.Outcome)
	assert.Equal(t, "machine failure", envelope.Message)
}

func TestRenderCLIErrorMachineModeIgnoresDiagnosticLevel(t *testing.T) {
	for _, level := range log.ValidLevels() {
		t.Run(level, func(t *testing.T) {
			log.Init(log.Config{Level: level})
			r, w, err := os.Pipe()
			require.NoError(t, err)
			original := os.Stderr
			os.Stderr = w
			renderCLIError(
				&clierr.CLIError{Code: clierr.CodeUnknown, Message: "machine failure"},
				true,
			)
			require.NoError(t, w.Close())
			os.Stderr = original

			var envelope devcconfig.ErrorEnvelope
			require.NoError(t, json.NewDecoder(r).Decode(&envelope))
			assert.Equal(t, "error", envelope.Kind)
			assert.Equal(t, "error", envelope.Outcome)
			assert.Equal(t, "machine failure", envelope.Message)
		})
	}
}

func TestRootPreRunValidatesPersistedLevelUnlessOverridden(t *testing.T) {
	config.ResetPathManager()
	t.Cleanup(func() { log.Init(log.Config{}) })
	t.Cleanup(config.ResetPathManager)
	t.Setenv(config.EnvHome, t.TempDir())
	t.Setenv("DEVSY_LOG_FORMAT", "")
	t.Setenv("DEVSY_RESULT_FORMAT", "")
	t.Setenv("DEVSY_NO_STATUS", "")
	t.Setenv(config.EnvLogLevel, "")
	require.NoError(t, config.SaveConfig(&config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{
			config.DefaultContext: {Options: map[string]config.OptionValue{
				config.ContextOptionLogLevel: {Value: testInvalidValue},
			}},
		},
	}))
	root, global := BuildRoot()
	root.SetContext(t.Context())
	err := rootPreRun(root, global, root.PersistentFlags())
	require.ErrorContains(t, err, testInvalidLevelMessage)
	global.LogLevel = "debug"
	require.NoError(t, rootPreRun(root, global, root.PersistentFlags()))
}

func TestRootPreRunRejectsExplicitEmptyOutputFlags(t *testing.T) {
	t.Setenv("DEVSY_LOG_LEVEL", "")
	t.Setenv("DEVSY_LOG_FORMAT", "")
	t.Setenv("DEVSY_RESULT_FORMAT", "")
	t.Setenv("DEVSY_NO_STATUS", "")
	for _, name := range []string{"log-level", "log-format", "result-format"} {
		t.Run(name, func(t *testing.T) {
			root, global := BuildRoot()
			require.NoError(t, root.PersistentFlags().Set(name, ""))
			err := rootPreRun(root, global, root.PersistentFlags())
			require.ErrorContains(t, err, "--"+name+" cannot be empty")
		})
	}
}

func TestWorkspaceUpPayloadLogLevelPrecedence(t *testing.T) {
	for _, tc := range []payloadLevelCase{
		{name: "sender trace", fromSender: true, want: log.LevelTraceName},
		{name: "sender info", fromSender: true, want: log.LevelInfoName},
		{name: "sender warn", fromSender: true, want: log.LevelWarnName},
		{name: "absent payload falls back to context", payload: `{}`, want: log.LevelWarnName},
		{
			name: "environment wins", payload: `{"logLevel":"trace"}`,
			env: log.LevelInfoName, want: log.LevelInfoName,
		},
		{
			name: "argument wins", payload: `{"logLevel":"trace"}`,
			env: log.LevelInfoName, argument: log.LevelWarnName, want: log.LevelWarnName,
		},
		{name: "invalid payload", payload: `{"logLevel":"invalid"}`, wantErr: testInvalidLevelMessage},
		{
			name: "environment overrides invalid payload", payload: `{"logLevel":"invalid"}`,
			env: log.LevelWarnName, want: log.LevelWarnName,
		},
		{name: "malformed selected payload", payload: `{`, wantErr: "decode up options"},
		{
			name: "malformed selected level type", payload: `{"logLevel":123,"secret":"payload-secret-4982"}`,
			wantErr: "decode up options",
		},
		{name: "argument overrides malformed payload", payload: `{`, argument: log.LevelInfoName, want: log.LevelInfoName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkPayloadLogLevel(t, tc)
		})
	}
}

func checkPayloadLogLevel(t *testing.T, tc payloadLevelCase) {
	t.Helper()
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
	t.Cleanup(func() { log.Init(log.Config{}) })
	t.Setenv(config.EnvHome, t.TempDir())
	payload := tc.payload
	if tc.fromSender {
		encoded, err := json.Marshal(client.UpOptions{LogLevel: tc.want})
		require.NoError(t, err)
		payload = string(encoded)
	}
	t.Setenv(config.EnvFlagsUp, payload)
	t.Setenv(config.EnvLogLevel, tc.env)
	t.Setenv(config.ContextOptionLogLevel, "")
	t.Setenv("DEVSY_LOG_FORMAT", "")
	t.Setenv("DEVSY_RESULT_FORMAT", "")
	t.Setenv("DEVSY_NO_STATUS", "")
	require.NoError(t, config.SaveConfig(&config.Config{
		DefaultContext: config.DefaultContext,
		Contexts:       map[string]*config.ContextConfig{config.DefaultContext: {}},
	}))
	root, global := BuildRoot()
	up, _, err := root.Find([]string{"workspace", "up"})
	require.NoError(t, err)
	up.SetContext(t.Context())
	if tc.argument != "" {
		require.NoError(t, root.PersistentFlags().Set(names.LogLevel, tc.argument))
	}
	err = rootPreRun(up, global, root.PersistentFlags())
	if tc.wantErr != "" {
		require.ErrorContains(t, err, tc.wantErr)
		assert.NotContains(t, err.Error(), "payload-secret-4982")
		return
	}
	require.NoError(t, err)
	assert.Equal(t, tc.want, log.LevelString())
	assert.Equal(t, []string{"--log-level", tc.want}, log.ForwardFlagArgs())
}

func TestNonUpCommandIgnoresTaskPayload(t *testing.T) {
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
	t.Cleanup(func() { log.Init(log.Config{}) })
	t.Setenv(config.EnvHome, t.TempDir())
	t.Setenv(config.EnvFlagsUp, `{`)
	t.Setenv(config.EnvLogLevel, "")
	t.Setenv(config.ContextOptionLogLevel, "")
	t.Setenv("DEVSY_LOG_FORMAT", "")
	t.Setenv("DEVSY_RESULT_FORMAT", "")
	t.Setenv("DEVSY_NO_STATUS", "")
	require.NoError(t, config.SaveConfig(&config.Config{
		DefaultContext: config.DefaultContext,
		Contexts:       map[string]*config.ContextConfig{config.DefaultContext: {}},
	}))
	root, global := BuildRoot()
	list, _, err := root.Find([]string{"workspace", cmdList})
	require.NoError(t, err)
	list.SetContext(t.Context())
	require.NoError(t, rootPreRun(list, global, root.PersistentFlags()))
	assert.Equal(t, log.LevelWarnName, log.LevelString())
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
		{"space-separated log-format", []string{"up", flagLogFormat, logOutputJSON}, logOutputJSON},
		{
			"equals-form log-format",
			[]string{"up", flagLogFormat + "=" + logOutputJSON},
			logOutputJSON,
		},
		{
			"last format wins",
			[]string{flagLogFormat, logOutputJSON, flagLogFormat, logOutputText},
			logOutputText,
		},
		{"child arguments ignored", []string{"exec", "--", flagLogFormat, logOutputJSON}, ""},
		{
			"flag before unknown command",
			[]string{flagLogFormat, logOutputJSON, "bogus"},
			logOutputJSON,
		},
		{"trailing flag with no value returns empty", []string{"up", flagLogFormat}, ""},
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
		{"internal errors stay machine-readable with text logs", logOutputText, true, "", true},
		{
			"desktop errors stay machine-readable with text logs",
			logOutputText,
			false,
			config.BoolTrue,
			true,
		},
		{"invalid format retains internal machine mode", testInvalidValue, true, "", true},
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
		{"json machine", []string{"up", flagLogFormat, logOutputJSON}, false, true},
		{"logfmt machine", []string{"up", flagLogFormat, logOutputLogfmt}, false, true},
		{"explicit text is human", []string{"up", flagLogFormat, logOutputText}, false, false},
		{"internal stays silent", []string{internalCommand}, true, true},
	}

	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.EnvUI, "")
			rootCmd, globalFlags := BuildRoot()
			os.Args = append([]string{devsy}, tc.args...)
			machineMode := configureOutput(rootCmd, globalFlags, tc.isInternal)
			assert.Equal(t, tc.wantSilent, machineMode)
			assert.True(t, rootCmd.SilenceErrors)
			assert.Equal(t, tc.wantSilent, rootCmd.SilenceUsage)
		})
	}
}

func TestConfigureOutput_DesktopTextLogsKeepMachineErrors(t *testing.T) {
	t.Setenv(config.EnvUI, config.BoolTrue)
	t.Cleanup(func() { log.Init(log.Config{}) })
	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })
	root, global := BuildRoot()
	global.LogLevel = log.LevelInfoName
	os.Args = []string{devsy, flagLogFormat, logOutputText}
	require.True(t, configureOutput(root, global, false))
	var sink bytes.Buffer
	remove := log.AddSink(&sink)
	defer remove()
	log.Info("root format test")
	require.NoError(t, log.Sync())
	assertTextLogLine(t, strings.TrimSpace(sink.String()))
}

func TestConfigureOutput_InvalidFormatUsesMachineFallback(t *testing.T) {
	t.Cleanup(func() { log.Init(log.Config{}) })
	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })
	root, global := BuildRoot()
	global.LogLevel = log.LevelInfoName
	os.Args = []string{devsy, internalCommand, flagLogFormat, testInvalidValue}
	require.True(t, configureOutput(root, global, true))
	var sink bytes.Buffer
	remove := log.AddSink(&sink)
	defer remove()
	log.Info("early configuration error")
	require.NoError(t, log.Sync())
	var record map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(sink.Bytes()), &record))
	assert.Equal(t, "early configuration error", record["msg"])
}

func TestConfigureOutput_SelectsDiagnosticLogFormat(t *testing.T) {
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	cases := []struct {
		name string
		flag string
	}{
		{name: "json", flag: logOutputJSON},
		{name: "logfmt", flag: logOutputLogfmt},
		{name: "text", flag: logOutputText},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rootCmd, globalFlags := BuildRoot()
			globalFlags.LogLevel = log.LevelInfoName
			os.Args = []string{devsy, flagLogFormat, tc.flag}
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
			assertDiagnosticLogFormat(t, tc.flag, lines[len(lines)-1])
		})
	}
}

func assertDiagnosticLogFormat(t *testing.T, format, line string) {
	t.Helper()
	assertions := map[string]func(*testing.T, string){
		logOutputJSON:   assertJSONLogLine,
		logOutputLogfmt: assertLogfmtLine,
		logOutputText:   assertTextLogLine,
	}
	assertion, ok := assertions[format]
	if !ok {
		t.Fatalf("unexpected log format %q", format)
	}
	assertion(t, line)
}

func assertJSONLogLine(t *testing.T, line string) {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, line)
	}
}

func assertLogfmtLine(t *testing.T, line string) {
	t.Helper()
	if !strings.Contains(line, "level=info") || !strings.Contains(line, "msg=") {
		t.Fatalf("log line is not logfmt: %q", line)
	}
}

func assertTextLogLine(t *testing.T, line string) {
	t.Helper()
	if !strings.Contains(line, "INFO") || !strings.Contains(line, "root format test") {
		t.Fatalf("log line is not text: %q", line)
	}
}

func TestConfigureOutput_DefaultMachineModeUsesJSONLogs(t *testing.T) {
	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })
	rootCmd, globalFlags := BuildRoot()
	globalFlags.LogLevel = log.LevelInfoName
	os.Args = []string{devsy, internalCommand}
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

func TestExitCodeForErrorPreservesWorkspaceCommandExit(t *testing.T) {
	for _, machine := range []bool{false, true} {
		err := fmt.Errorf("execute workspace command: %w", &workspace.CommandExitError{Code: 17})
		assert.Equal(t, 17, exitCodeForError(err, machine))
	}
}
