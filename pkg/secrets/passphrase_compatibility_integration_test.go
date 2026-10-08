//go:build crypto_integration

package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProductionPassphraseEncryptionCompatibility(t *testing.T) {
	const (
		passphrase = "production compatibility test passphrase"
		value      = "exact recovered production fixture value"
	)
	key := backendKey("default", "LEGACY")
	dir := t.TempDir()
	resolver := DefaultUnlockResolver{ExplicitPassphrase: passphrase}
	manager := NewProtectionManager(dir, resolver)
	require.NoError(t, manager.SetPassphrase(passphrase))

	indexPath := filepath.Join(dir, IndexFileName)
	idx, err := loadIndex(indexPath)
	require.NoError(t, err)
	require.Equal(t, string(keySourcePassphrase), idx.data.KeySource)
	require.Equal(t, string(keySourcePassphrase), idx.data.FileStore.KeySource)
	fk, err := openExistingFileKeyWithResolver(dir, idx, resolver)
	require.NoError(t, err)
	blobPath := filepath.Join(dir, EncryptedFileName)
	initialBlob, err := os.ReadFile(blobPath) // #nosec G304 -- isolated test directory.
	require.NoError(t, err)
	assertScryptHeader(t, initialBlob, "18")
	backend := newFileBackend(blobPath, fk)
	values, err := backend.load()
	require.NoError(t, err)
	require.Empty(t, values)
	require.NoError(t, backend.set(key, value))

	freshKey, err := openExistingFileKeyWithResolver(dir, idx, resolver)
	require.NoError(t, err)
	values, err = newFileBackend(blobPath, freshKey).load()
	require.NoError(t, err)
	require.Equal(t, map[string]string{key: value}, values)
	blob, err := os.ReadFile(blobPath) // #nosec G304 -- isolated test directory.
	require.NoError(t, err)
	assertScryptHeader(t, blob, "18")
	assertProductionFilePermissions(t, blobPath)

	wrongKey, err := openExistingFileKeyWithResolver(dir, idx,
		DefaultUnlockResolver{ExplicitPassphrase: "incorrect compatibility passphrase"})
	require.NoError(t, err)
	values, err = newFileBackend(blobPath, wrongKey).load()
	require.ErrorIs(t, err, ErrUnlockFailed)
	require.Nil(t, values)
	unchanged, err := os.ReadFile(blobPath) // #nosec G304 -- isolated test directory.
	require.NoError(t, err)
	require.Equal(t, blob, unchanged)
	assertProductionLegacyCompatibility(t, resolver, blob, map[string]string{key: value})
}

func assertProductionFilePermissions(t *testing.T, path string) {
	t.Helper()
	const windowsOS = "windows"
	if runtime.GOOS == windowsOS {
		return
	}
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func assertProductionLegacyCompatibility(
	t *testing.T, resolver UnlockMaterialResolver, blob []byte, expected map[string]string,
) {
	t.Helper()

	// Reuse the production ciphertext with the catalog layout of older CLIs.
	legacyDir := t.TempDir()
	legacyBlobPath := filepath.Join(legacyDir, EncryptedFileName)
	legacyIndexPath := filepath.Join(legacyDir, IndexFileName)
	legacyCatalog := []byte(
		"contexts:\n  default:\n    LEGACY:\n      name: LEGACY\n      context: default\n      kind: secret\n",
	)
	require.NoError(
		t,
		os.WriteFile(legacyBlobPath, blob, 0o600),
	) // #nosec G703 -- isolated test directory.
	require.NoError(t, os.WriteFile(legacyIndexPath, legacyCatalog, 0o600))
	legacyIdx, err := loadIndex(legacyIndexPath)
	require.NoError(t, err)
	require.Empty(t, legacyIdx.data.KeySource)
	registry, ok := newSystemBackendRegistry(legacyDir, resolver).(*systemBackendRegistry)
	require.True(t, ok)
	present, conclusive := registry.probeFile(legacyIdx, backendKey("default", "LEGACY"))
	require.True(t, present)
	require.True(t, conclusive)
	legacyKey, err := openPassphraseFileKeyWithResolver(resolver)
	require.NoError(t, err)
	values, err := newFileBackend(legacyBlobPath, legacyKey).load()
	require.NoError(t, err)
	require.Equal(t, expected, values)
	legacyBlobAfter, err := os.ReadFile(legacyBlobPath) // #nosec G304 -- isolated test directory.
	require.NoError(t, err)
	require.Equal(t, blob, legacyBlobAfter)
	legacyCatalogAfter, err := os.ReadFile(
		legacyIndexPath,
	) // #nosec G304 -- isolated test directory.
	require.NoError(t, err)
	require.Equal(t, legacyCatalog, legacyCatalogAfter)
}
