package framework

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/stretchr/testify/require"
)

const (
	diagnosticFixtureExecutable = "devsy"
	diagnosticFixtureValue      = "external-tool-fixture"
	diagnosticFixtureText       = "public command detail"
	diagnosticFixture           = "Authorization:\n Bearer " + diagnosticFixtureValue + "\n" + diagnosticFixtureText
	fixtureCommandFailure       = "printf '%s' \"$DEVSY_DIAGNOSTIC_FIXTURE\"\nexit 7\n"
	fixtureCommandSuccess       = "exit 0\n"
)

func TestGPGCommandFailuresMaskFoldedDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name          string
		importCommand string
		agentCommand  string
		detail        string
	}{
		{"import", fixtureCommandFailure, fixtureCommandSuccess, "import gpg private key"},
		{"connect agent", fixtureCommandSuccess, fixtureCommandFailure, "start gpg-agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := externalCommandFixture(t)
			writeDiagnosticFixture(t, bin, "gpg", tc.importCommand)
			writeDiagnosticFixture(t, bin, "gpg-connect-agent", tc.agentCommand)
			assertMaskedExternalFailure(
				t,
				ImportGpgKey(filepath.Join(bin, "fixture.key")),
				tc.detail,
			)
		})
	}
}

func TestSSHAgentCommandFailuresMaskFoldedDiagnostics(t *testing.T) {
	bin := externalCommandFixture(t)
	writeDiagnosticFixture(t, bin, "ssh-keygen", fixtureCommandFailure)
	writeDiagnosticFixture(t, bin, "ssh-add", fixtureCommandFailure)
	key := filepath.Join(bin, "fixture.key")
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"ssh-keygen", func() error { return generateAgentKey(key) }},
		{"ssh-add", func() error { return addKeyToAgent(filepath.Join(bin, "socket"), key) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertMaskedExternalFailure(t, tc.run(), tc.name)
		})
	}
}

func TestForwardedGPGKeyMismatchMasksFoldedDiagnostics(t *testing.T) {
	bin := externalCommandFixture(t)
	writeDiagnosticFixture(
		t,
		bin,
		diagnosticFixtureExecutable,
		"printf '%s' \"$DEVSY_DIAGNOSTIC_FIXTURE\"\n",
	)
	f := &Framework{DevsyBinDir: bin, DevsyBinName: diagnosticFixtureExecutable}
	err := f.DevsySSHGpgSecretKeyForwarded(t.Context(), "fixture-workspace", "expected-fingerprint")
	require.ErrorContains(t, err, "expected-fingerprint not reachable")
	require.ErrorContains(t, err, diagnosticFixtureText)
	require.NotContains(t, err.Error(), diagnosticFixtureValue)
	require.Contains(t, err.Error(), "***")
}

func TestFrameworkCommandFailuresMaskDiagnosticCopies(t *testing.T) {
	bin := externalCommandFixture(t)
	writeDiagnosticFixture(t, bin, diagnosticFixtureExecutable, "printf '%s' 'functional stdout'\n"+
		"printf '%s\\n' \"$DEVSY_DIAGNOSTIC_DETAIL\" \"$DEVSY_DIAGNOSTIC_FIXTURE\" >&2\nexit 7\n")
	f := &Framework{DevsyBinDir: bin, DevsyBinName: diagnosticFixtureExecutable}
	for _, tc := range []struct {
		name   string
		detail string
		cause  error
		run    func(*testing.T, *Framework) error
	}{
		{"ssh", "ordinary SSH failure", nil, fixtureSSHOnce},
		{"runtime unavailable", "AGENT_INJECTION_RUNTIME_UNAVAILABLE", docker.ErrRuntimeUnavailable, fixtureSSHOnce},
		{"provider in use", "because workspace exists", errProviderInUse, fixtureProviderDelete},
		{"JSON result", "JSON command failure", nil, fixtureCommandJSON},
		{"setup logs", "setup logs failure", nil, fixtureSetupLogs},
		{"workspace edit", "workspace edit failure", nil, fixtureWorkspaceEdit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DEVSY_DIAGNOSTIC_DETAIL", tc.detail)
			err := tc.run(t, f)
			require.ErrorContains(t, err, tc.detail)
			require.ErrorContains(t, err, diagnosticFixtureText)
			require.NotContains(t, err.Error(), diagnosticFixtureValue)
			if tc.cause != nil {
				require.ErrorIs(t, err, tc.cause)
			}
		})
	}
}

func fixtureSSHOnce(t *testing.T, f *Framework) error {
	_, err := f.DevsySSHOnce(t.Context(), "fixture-workspace", "true")
	return err
}

func fixtureProviderDelete(t *testing.T, f *Framework) error {
	return f.DevsyProviderDelete(t.Context(), "fixture-provider")
}

func fixtureCommandJSON(t *testing.T, f *Framework) error {
	_, err := f.ExecCommandJSON(t.Context(), []string{"fixture"})
	return err
}

func fixtureSetupLogs(t *testing.T, f *Framework) error {
	t.Helper()
	stdout, stderr, err := f.DevsySetupLogs(t.Context())
	require.Equal(t, "functional stdout", stdout)
	require.Contains(t, stderr, diagnosticFixture)
	return err
}

func fixtureWorkspaceEdit(t *testing.T, f *Framework) error {
	_, err := f.DevsyWorkspaceEditJSON(t.Context(), "fixture-workspace")
	return err
}

func TestWorkspaceEditDecodeErrorMasksDiagnosticCopy(t *testing.T) {
	bin := externalCommandFixture(t)
	writeDiagnosticFixture(
		t,
		bin,
		diagnosticFixtureExecutable,
		"printf '%s' \"$DEVSY_DIAGNOSTIC_FIXTURE\"\n",
	)
	f := &Framework{DevsyBinDir: bin, DevsyBinName: diagnosticFixtureExecutable}
	_, err := f.DevsyWorkspaceEditResult(t.Context(), "fixture-workspace")
	require.ErrorContains(t, err, "decode workspace edit result")
	require.ErrorContains(t, err, diagnosticFixtureText)
	require.NotContains(t, err.Error(), diagnosticFixtureValue)
}

func externalCommandFixture(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX executable fixtures")
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	t.Setenv("DEVSY_DIAGNOSTIC_FIXTURE", diagnosticFixture)
	return bin
}

func writeDiagnosticFixture(t *testing.T, bin, name, script string) {
	t.Helper()
	root, err := os.OpenRoot(bin)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	require.NoError(t, root.WriteFile(name, []byte("#!/bin/sh\n"+script), 0o700))
}

func assertMaskedExternalFailure(t *testing.T, err error, detail string) {
	t.Helper()
	require.ErrorContains(t, err, detail)
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 7, exit.ExitCode())
	require.ErrorContains(t, err, diagnosticFixtureText)
	require.NotContains(t, err.Error(), diagnosticFixtureValue)
	require.Contains(t, err.Error(), "***")
}
