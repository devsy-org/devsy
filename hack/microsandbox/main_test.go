package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	labelFlag           = "--label"
	unknownProviderMode = "unknown"
	expectedCountFlag   = "--expected-count"
	reportFlag          = "--report"
	providerModeFlag    = "--provider-mode"
	cliReportLabel      = "cli-report"
)

func TestCLIRejectsInvalidArguments(t *testing.T) {
	cases := [][]string{
		nil,
		{"invalid-command"},
		{runSuiteCommand, "--unknown-flag"},
		{runSuiteCommand, labelFlag, "suite", expectedCountFlag, "3"},
		{runSuiteCommand, "unexpected-positional"},
		{verifyReportCommand},
		{
			verifyReportCommand,
			reportFlag,
			"missing.json",
			labelFlag,
			cliReportLabel,
			expectedCountFlag,
			"3",
		},
		{
			verifyReportCommand,
			reportFlag, "report.json",
			labelFlag, cliReportLabel,
			expectedCountFlag, "3",
			providerModeFlag, unknownProviderMode,
		},
		{installLinuxCommand, "unexpected-positional"},
	}
	for _, args := range cases {
		var stdout, stderr bytes.Buffer
		require.Equal(t, 2, runCLI(args, &stdout, &stderr), "%v: %s", args, stderr.String())
		require.NotEmpty(t, stderr.String())
		require.Empty(t, stdout.String())
	}
}

func TestCLIVerifyReportRejectsMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, os.WriteFile(path, []byte("{"), 0o600))
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{
		verifyReportCommand, reportFlag, path, labelFlag, cliReportLabel, expectedCountFlag, "2",
	}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stderr.String(), "::error::")
	require.Empty(t, stdout.String())
}

func TestCLIVerifyReportAcceptsExternalMode(t *testing.T) {
	path := writeReportFixture(t, validExternalReportFor(reportTestLabel))
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{
		verifyReportCommand,
		reportFlag, path,
		labelFlag, reportTestLabel,
		expectedCountFlag, "3",
		providerModeFlag, providerModeExternal,
	}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	require.Contains(t, stdout.String(), "3 specs passed")
}

func TestCLIRunForwardsExternalModeIntoReportValidation(t *testing.T) {
	cfg, tempDir := newSuiteRunFixture(t, reportTestLabel)
	var stdout, stderr bytes.Buffer
	var testCommandSeen bool
	run := func(cmd *exec.Cmd) error {
		if suiteCommandIsChown(cmd) {
			return nil
		}
		testCommandSeen = true
		return writeExternalSuiteReportFromCommand(cmd)
	}
	code := runSuiteCLIWithRunner([]string{
		labelFlag, cfg.label,
		expectedCountFlag, "3",
		providerModeFlag, providerModeExternal,
		"--repository-dir", cfg.repositoryDir,
		"--artifacts-dir", tempDir,
		"--test-timeout", cfg.testTimeout,
	}, &stdout, &stderr, run)
	require.Equal(t, 0, code, stderr.String())
	require.True(t, testCommandSeen)
	require.Contains(t, stdout.String(), "3 specs passed")
}
