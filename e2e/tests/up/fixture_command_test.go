package up

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/onsi/gomega"
	"github.com/stretchr/testify/require"
)

func TestFixtureCommandMasksFailureWithoutChangingCapturedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	const fixture = "Authorization:\n Bearer fixture-command-placeholder\npublic command detail\n"
	path := filepath.Join(t.TempDir(), "fixture-output")
	require.NoError(t, os.WriteFile(path, []byte(fixture), 0o600))
	previousGomega := gomega.Default
	gomega.Default = gomega.NewWithT(t)
	t.Cleanup(func() { gomega.Default = previousGomega })
	var captured string
	failures := gomega.InterceptGomegaFailures(func() {
		captured = fixtureCommand(t.Context(), time.Second, nil, "/bin/sh", "-c",
			"cat \"$1\"; exit 7", "fixture", path)
	})
	require.Equal(t, fixture, captured)
	require.Len(t, failures, 1)
	require.Contains(t, failures[0], "public command detail")
	require.NotContains(t, failures[0], "fixture-command-placeholder")
	require.Contains(t, failures[0], "***")
}

func TestExpectFixtureOutputFailureReportsRedactedOutput(t *testing.T) {
	const output = "Authorization:\n Bearer assertion-output-fixture\npublic assertion detail\n"
	previousGomega := gomega.Default
	gomega.Default = gomega.NewWithT(t)
	t.Cleanup(func() { gomega.Default = previousGomega })
	failures := gomega.InterceptGomegaFailures(func() {
		expectFixtureOutput(strings.Contains(output, "absent marker"), output, "marker present")
	})
	require.Len(t, failures, 1)
	require.Contains(t, failures[0], "marker present")
	require.Contains(t, failures[0], "public assertion detail")
	require.NotContains(t, failures[0], "assertion-output-fixture")
	require.Empty(t, gomega.InterceptGomegaFailures(func() {
		expectFixtureOutput(strings.Contains(output, "public assertion"), output, "marker present")
	}))
}

func TestMicrosandboxEgressIPMasksFoldedCommandError(t *testing.T) {
	writeFixtureTool(t, "ip",
		"printf 'Authorization:\\n Bearer egress-tool-fixture\\npublic egress detail'\nexit 7\n")
	err := microsandboxEgressIP(t.Context(), "link", "show")
	require.ErrorContains(t, err, "ip [link show]")
	require.ErrorContains(t, err, "public egress detail")
	assertFixtureToolFailure(t, err, "egress-tool-fixture")
}

func TestRemoteAgentRemovalMasksErrorAndRetainsMissingContainerHandling(t *testing.T) {
	t.Run("reported failure", func(t *testing.T) {
		writeFixtureTool(
			t,
			"docker",
			"printf 'Authorization:\\n Bearer cleanup-tool-fixture\\npublic cleanup detail'\nexit 7\n",
		)
		out, execErr := runFixtureCommand(
			t.Context(),
			time.Second,
			nil,
			"docker",
			"rm",
			"-f",
			"remote-agent-fixture",
		)
		err := remoteAgentRemovalError(out, execErr)
		require.ErrorContains(t, err, "remove remote agent container")
		require.ErrorContains(t, err, "public cleanup detail")
		assertFixtureToolFailure(t, err, "cleanup-tool-fixture")
	})
	t.Run("missing container", func(t *testing.T) {
		writeFixtureTool(
			t,
			"docker",
			"printf 'Authorization:\\n Bearer No such container'\nexit 7\n",
		)
		out, execErr := runFixtureCommand(
			t.Context(),
			time.Second,
			nil,
			"docker",
			"rm",
			"-f",
			"remote-agent-fixture",
		)
		require.Error(t, execErr)
		require.NoError(t, remoteAgentRemovalError(out, execErr))
	})
}

func writeFixtureTool(t *testing.T, name, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	root, err := os.OpenRoot(bin)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	require.NoError(t, root.WriteFile(name, []byte("#!/bin/sh\n"+script), 0o700))
}

func assertFixtureToolFailure(t *testing.T, err error, sensitive string) {
	t.Helper()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 7, exit.ExitCode())
	require.NotContains(t, err.Error(), sensitive)
	require.Contains(t, err.Error(), "***")
}
