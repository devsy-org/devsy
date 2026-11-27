package cmdinternal

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/shell"
	"github.com/stretchr/testify/require"
)

const (
	flagScriptFile     = "--script-file"
	flagForceFile      = "--force-file"
	flagAckFile        = "--ack-file"
	flagParentPID      = "--parent-pid"
	flagExecutablePath = "--executable-path"
)

func TestParseExecutablePaths(t *testing.T) {
	paths, err := parseExecutablePaths([]string{"A=/a b/x", "B=/p=q"})

	require.NoError(t, err)
	require.Equal(t, map[string]string{"A": "/a b/x", "B": "/p=q"}, paths)
}

func TestParseExecutablePathsEmpty(t *testing.T) {
	paths, err := parseExecutablePaths(nil)

	require.NoError(t, err)
	require.Nil(t, paths)
}

func TestParseExecutablePathsRejectsMalformedEntries(t *testing.T) {
	for _, entry := range []string{"no-separator", "=/path"} {
		_, err := parseExecutablePaths([]string{entry})

		require.ErrorContains(t, err, "want NAME=PATH", entry)
	}
}

func TestParseExecutablePathsKeepsEmptyValue(t *testing.T) {
	paths, err := parseExecutablePaths([]string{"A="})

	require.NoError(t, err)
	require.Equal(t, map[string]string{"A": ""}, paths)
}

func TestProviderShellCommandPassesExecutablePathsToTheShell(t *testing.T) {
	var got shell.ProviderShellInvocation
	command := newProviderShellCmd(func(_ context.Context, in shell.ProviderShellInvocation) error {
		got = in
		return nil
	})
	command.SetArgs([]string{
		flagScriptFile, "s", flagForceFile, "f", flagAckFile, "a", flagParentPID, "7",
		flagExecutablePath, "SSH_PROVIDER=C:\\Jane Doe\\a=b\\provider.exe",
		flagExecutablePath, "OTHER=/x y/z",
		flagExecutablePath, "EMPTY=",
	})

	require.NoError(t, command.Execute())

	require.Equal(t, shell.ProviderShellInvocation{
		ScriptPath: "s", ForcePath: "f", AckPath: "a", ParentPID: 7,
		ExecutablePaths: map[string]string{
			"SSH_PROVIDER": "C:\\Jane Doe\\a=b\\provider.exe",
			"OTHER":        "/x y/z",
			"EMPTY":        "",
		},
	}, got)
}

func TestProviderShellCommandRejectsMalformedExecutablePath(t *testing.T) {
	command := newProviderShellCmd(func(context.Context, shell.ProviderShellInvocation) error {
		t.Fatal("the shell must not run")
		return nil
	})
	command.SetArgs([]string{
		flagScriptFile, "s", flagForceFile, "f", flagAckFile, "a", flagParentPID, "7",
		flagExecutablePath, "no-separator",
	})

	require.ErrorContains(t, command.Execute(), "want NAME=PATH")
}

func TestProviderShellExecutablePathFlagIsHidden(t *testing.T) {
	flag := NewProviderShellCmd().Flags().Lookup("executable-path")

	require.NotNil(t, flag)
	require.True(t, flag.Hidden)
}
