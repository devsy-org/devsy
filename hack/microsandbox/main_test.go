package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCLIRejectsInvalidArguments(t *testing.T) {
	cases := [][]string{
		nil,
		{"unknown"},
		{runSuiteCommand, "--unknown-flag"},
		{runSuiteCommand, "unexpected-positional"},
		{verifyReportCommand},
		{
			verifyReportCommand,
			"--report",
			"missing.json",
			"--label",
			"cli-report",
			"--expected-count",
			"3",
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
		verifyReportCommand, "--report", path, "--label", "cli-report", "--expected-count", "2",
	}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stderr.String(), "::error::")
	require.Empty(t, stdout.String())
}
