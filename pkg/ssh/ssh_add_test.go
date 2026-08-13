package ssh

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindPrivateKeys_MissingSSHDirReturnsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := FindPrivateKeys()
	require.Error(t, err, "a missing .ssh directory must surface a read error")
}

func TestFindPrivateKeys_ReturnsOnlyValidPrivateKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sshDir := filepath.Join(home, ".ssh")
	require.NoError(t, os.MkdirAll(sshDir, 0o700))

	privKey, _, err := rsaKeyGen()
	require.NoError(t, err)
	privPath := filepath.Join(sshDir, "id_ed25519")
	require.NoError(t, os.WriteFile(privPath, []byte(privKey), 0o600))

	// Public keys, config files, and subdirectories are not private keys and
	// must not be returned, even when they live alongside a valid key.
	require.NoError(t, os.WriteFile(
		filepath.Join(sshDir, "id_ed25519.pub"), []byte("ssh-ed25519 AAAAfake test"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(sshDir, "config"), []byte("Host *\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(sshDir, "subdir"), 0o700))

	keys, err := FindPrivateKeys()
	require.NoError(t, err)
	assert.Equal(t, []string{privPath}, keys,
		"only parseable private keys should be returned")
}

func TestFindPrivateKeys_EmptySSHDirReturnsNoKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	require.NoError(t, os.MkdirAll(filepath.Join(home, ".ssh"), 0o700))

	keys, err := FindPrivateKeys()
	require.NoError(t, err)
	assert.Empty(t, keys, "an empty .ssh directory must yield no keys")
}
