package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/require"
)

const (
	ciphertextTestKey        = "key"
	ciphertextTestValue      = "value"
	ciphertextTestPassphrase = "correct horse"
)

func TestFileBackendLoadClassifiesDecryptErrors(t *testing.T) {
	t.Run("wrong passphrase is an unlock failure", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), EncryptedFileName)
		original := newPassphraseBackend(t, path, ciphertextTestPassphrase)
		require.NoError(
			t,
			original.store(map[string]string{ciphertextTestKey: ciphertextTestValue}),
		)

		wrong := newPassphraseBackend(t, path, "wrong battery")
		_, err := wrong.load()
		require.ErrorIs(t, err, ErrUnlockFailed)
		require.NotErrorIs(t, err, ErrStoreCorrupt)
		require.ErrorContains(t, err, "secret store unlock failed")
		require.NotContains(t, err.Error(), "identity did not match")
	})

	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "malformed ciphertext", mutate: func([]byte) []byte { return []byte("not an age file") }},
		{name: "truncated ciphertext", mutate: func(data []byte) []byte { return data[:len(data)/2] }},
	} {
		t.Run(tc.name+" is store corruption", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), EncryptedFileName)
			backend := newPassphraseBackend(t, path, ciphertextTestPassphrase)
			require.NoError(
				t,
				backend.store(map[string]string{ciphertextTestKey: ciphertextTestValue}),
			)
			data, err := os.ReadFile(
				path,
			) // #nosec G304 -- fixed ciphertext fixture in a temporary directory.
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, tc.mutate(data), 0o600))

			_, err = backend.load()
			require.ErrorIs(t, err, ErrStoreCorrupt)
			require.NotErrorIs(t, err, ErrUnlockFailed)
			require.ErrorContains(t, err, "secret store corrupt")
			require.NotContains(t, err.Error(), "failed to read header")
		})
	}
}

func TestFileBackendLoadClassifiesPayloadAuthenticationFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  func(*testing.T, string) *fileKey
	}{
		{
			name: "passphrase key",
			key: func(t *testing.T, _ string) *fileKey {
				backend := newPassphraseBackend(t, "", ciphertextTestPassphrase)
				return backend.key
			},
		},
		{
			name: "automatic key",
			key: func(t *testing.T, dir string) *fileKey {
				key, err := resolveAutoKey(dir, fileKeyStore{path: filepath.Join(dir, KeyFileName)}, keySourceAutoFile)
				require.NoError(t, err)
				return key
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, EncryptedFileName)
			backend := newFileBackend(path, tc.key(t, dir))
			require.NoError(
				t,
				backend.store(map[string]string{ciphertextTestKey: strings.Repeat("secret", 20)}),
			)
			data, err := os.ReadFile(
				path,
			) // #nosec G304 -- fixed ciphertext fixture in a temporary directory.
			require.NoError(t, err)
			data[len(data)-1] ^= 1
			// #nosec G703 -- fixed ciphertext fixture in a temporary directory.
			require.NoError(t, os.WriteFile(path, data, 0o600))

			_, err = backend.load()
			require.ErrorIs(t, err, ErrStoreCorrupt)
			require.NotErrorIs(t, err, ErrUnlockFailed)
			require.ErrorContains(t, err, "secret store corrupt")
		})
	}
}

func TestFileBackendLoadClassifiesInvalidJSONAsStoreCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), EncryptedFileName)
	backend := newPassphraseBackend(t, path, ciphertextTestPassphrase)
	var ciphertext bytes.Buffer
	writer, err := age.Encrypt(&ciphertext, backend.key.recipient)
	require.NoError(t, err)
	_, err = writer.Write([]byte("{"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, os.WriteFile(path, ciphertext.Bytes(), 0o600))

	_, err = backend.load()
	require.ErrorIs(t, err, ErrStoreCorrupt)
	require.NotErrorIs(t, err, ErrUnlockFailed)
	require.ErrorContains(t, err, "secret store corrupt")
}
