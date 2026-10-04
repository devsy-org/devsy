package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	reviewLegacyName  = "LEGACY"
	reviewSecretValue = "value"
)

type reviewKeyStore struct {
	value string
	err   error
}

func (s reviewKeyStore) load() (string, error) { return s.value, s.err }
func (reviewKeyStore) save(string) error       { return nil }

func TestOpenKeyFromStoreClassifiesMissingAutoAndKeyringKeysAsUnavailable(t *testing.T) {
	for _, source := range []keySource{keySourceAutoFile, keySourceKeyring} {
		t.Run(string(source), func(t *testing.T) {
			_, err := openKeyFromStore(reviewKeyStore{}, source)
			require.ErrorIs(t, err, ErrBackendUnavailable)
			require.NotErrorIs(t, err, ErrSecretNotFound)
			require.NotErrorIs(t, err, ErrStoreCorrupt)
		})
	}

	t.Run("other store errors are preserved", func(t *testing.T) {
		storeErr := errors.New("credential service unavailable")
		_, err := openKeyFromStore(reviewKeyStore{err: storeErr}, keySourceKeyring)
		require.ErrorIs(t, err, ErrBackendUnavailable)
		require.ErrorIs(t, err, storeErr)
	})
}

func TestProbeFileRespectsPromptPolicyForLegacyOwnership(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := t.TempDir()
	key, err := passphraseFileKey(passphrase)
	require.NoError(t, err)
	path := filepath.Join(dir, EncryptedFileName)
	require.NoError(
		t,
		newFileBackend(path, key).set(backendKey(testContext, reviewLegacyName), reviewSecretValue),
	)

	indexPath := filepath.Join(dir, IndexFileName)
	require.NoError(
		t,
		// #nosec G304 -- indexPath is derived from t.TempDir().
		os.WriteFile(
			indexPath,
			[]byte(
				"contexts:\n  default:\n    LEGACY:\n      name: LEGACY\n      context: default\n      kind: secret\n",
			),
			0o600,
		),
	)
	idx, err := loadIndex(indexPath)
	require.NoError(t, err)

	promptCalls := 0
	resolver := DefaultUnlockResolver{
		LookupEnv:      func(string) string { return "" },
		ReadRemembered: func() (string, error) { return "", nil },
		Prompt: func(context.Context, UnlockRequest) (string, error) {
			promptCalls++
			return passphrase, nil
		},
	}
	registry := newSystemBackendRegistry(dir, resolver).(*systemBackendRegistry)

	present, conclusive := registry.probeFile(idx, backendKey(testContext, reviewLegacyName))
	require.False(t, present)
	require.False(t, conclusive)
	require.Zero(t, promptCalls)

	store := newLocalStoreWithRegistry(BackendFile, indexPath, registry)
	_, err = store.Get(testContext, reviewLegacyName)
	require.Error(t, err)
	require.Zero(t, promptCalls)
	err = store.Delete(testContext, reviewLegacyName)
	require.Error(t, err)
	require.Zero(t, promptCalls)

	// A credential supplied without prompting remains usable when prompting is
	// disabled, including the legacy passphrase-key fallback.
	explicit := DefaultUnlockResolver{ExplicitPassphrase: passphrase}
	registry = newSystemBackendRegistry(dir, explicit).(*systemBackendRegistry)
	present, conclusive = registry.probeFile(idx, backendKey(testContext, reviewLegacyName))
	require.True(t, present)
	require.True(t, conclusive)
	value, err := newFileBackend(path, key).get(backendKey(testContext, reviewLegacyName))
	require.NoError(t, err)
	require.Equal(t, reviewSecretValue, value)
}

func TestMissingAutomaticKeyReportsBackendUnavailableWithoutOrphaning(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, KeyFileName)
	key, err := resolveAutoKey(dir, fileKeyStore{path: keyPath}, keySourceAutoFile)
	require.NoError(t, err)
	ciphertextPath := filepath.Join(dir, EncryptedFileName)
	require.NoError(
		t,
		newFileBackend(
			ciphertextPath,
			key,
		).set(backendKey(testContext, reviewLegacyName), reviewSecretValue),
	)
	indexPath := filepath.Join(dir, IndexFileName)
	idx, err := loadIndex(indexPath)
	require.NoError(t, err)
	idx.data.KeySource = string(keySourceAutoFile)
	idx.put(
		SecretMeta{
			Context: testContext,
			Name:    reviewLegacyName,
			Kind:    KindSecret,
			Backend: BackendFile,
		},
	)
	require.NoError(t, idx.save())
	// #nosec G304 -- fixed ciphertext fixture in a temporary directory.
	before, err := os.ReadFile(ciphertextPath)
	require.NoError(t, err)
	// #nosec G703 -- fixed key fixture in a temporary directory.
	require.NoError(t, os.Remove(keyPath))
	store := newLocalStoreWithRegistry(BackendFile, indexPath, newSystemBackendRegistry(dir))
	inspected, err := store.Inspect(testContext)
	require.NoError(t, err)
	require.Len(t, inspected, 1)
	require.Equal(t, SecretBackendUnavailable, inspected[0].Availability)
	require.Equal(t, "backend_unavailable", inspected[0].ReasonCode)
	listed, err := store.List(testContext)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.False(t, listed[0].Orphaned)
	_, err = store.Get(testContext, reviewLegacyName)
	require.ErrorIs(t, err, ErrBackendUnavailable)
	require.NotErrorIs(t, err, ErrSecretNotFound)
	_, err = os.Stat(keyPath)
	require.ErrorIs(t, err, os.ErrNotExist)
	// #nosec G304 -- fixed ciphertext fixture in a temporary directory.
	after, err := os.ReadFile(ciphertextPath)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
