package cmdinternal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProviderShellCommandRegistration(t *testing.T) {
	command := NewProviderShellCmd()
	require.Equal(t, "provider-shell", command.Name())
	require.True(t, command.Hidden)
	dir := providerShellCLITestDir(t)
	script := filepath.Join(dir, "script")
	require.NoError(t, os.WriteFile(script, []byte("exit 0"), 0o600))
	token := strings.Repeat("0123456789abcdef", 4)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ack-key"), []byte(token), 0o600))
	command.SetArgs([]string{
		"--script-file", script, "--force-file", filepath.Join(dir, "force"),
		"--ack-file", filepath.Join(dir, "ack"),
	})
	require.NoError(t, command.Execute())
	_, err := os.Lstat(filepath.Join(dir, "ack-key"))
	require.ErrorIs(t, err, os.ErrNotExist)
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	ack, readErr := root.ReadFile("ack")
	closeErr := root.Close()
	require.NoError(t, readErr)
	require.NoError(t, closeErr)
	require.Equal(t, token, string(ack))
}

func TestProviderShellCommandRejectsMissingToken(t *testing.T) {
	dir := providerShellCLITestDir(t)
	script := filepath.Join(dir, "script")
	require.NoError(t, os.WriteFile(script, []byte("printf should-not-run"), 0o600))
	_, err := os.Lstat(filepath.Join(dir, "ack-key"))
	require.ErrorIs(t, err, os.ErrNotExist)
	command := NewProviderShellCmd()
	command.SetArgs([]string{
		"--script-file", script, "--force-file", filepath.Join(dir, "force"),
		"--ack-file", filepath.Join(dir, "ack"),
	})
	require.ErrorContains(t, command.Execute(), "invalid provider shell acknowledgement token")
	_, err = os.Lstat(filepath.Join(dir, "ack"))
	require.ErrorIs(t, err, os.ErrNotExist)
}
