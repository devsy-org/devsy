package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/require"
)

const (
	legacyRecoveryPassphrase = "legacy recovery fixture passphrase"
	legacyRecoveryOldName    = "OLD"
	legacyRecoveryNewName    = "NEW"
	legacyRecoveryOldValue   = "legacy payload"
	legacyRecoveryNewValue   = "new payload"
)

type unavailableLegacyKeyring struct{ *systemBackendRegistry }

func (r unavailableLegacyKeyring) Probe(kind Backend, idx *index, key string) (bool, bool) {
	if kind == BackendKeyring {
		return false, false
	}
	return r.systemBackendRegistry.Probe(kind, idx, key)
}

// Keep production recovery and credential checks, reducing only subsequent writes.
func (r unavailableLegacyKeyring) Open(
	kind Backend,
	idx *index,
	intent BackendOpenIntent,
) (backend, error) {
	b, err := r.systemBackendRegistry.Open(kind, idx, intent)
	if err == nil {
		if file, ok := b.(*fileBackend); ok {
			if recipient, ok := file.key.recipient.(*age.ScryptRecipient); ok {
				recipient.SetWorkFactor(testScryptWorkFactor)
			}
		}
	}
	return b, err
}

func legacyRecoveryFixture(t *testing.T, credential string) (*localStore, *fileBackend) {
	t.Helper()
	dir := t.TempDir()
	key := testPassphraseFileKey(t, legacyRecoveryPassphrase)
	blob := newFileBackend(filepath.Join(dir, EncryptedFileName), key)
	require.NoError(
		t,
		blob.set(backendKey(testContext, legacyRecoveryOldName), legacyRecoveryOldValue),
	)
	idx, err := loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	idx.put(SecretMeta{Name: legacyRecoveryOldName, Context: testContext, Kind: KindSecret})
	require.NoError(t, idx.save())
	resolver := DefaultUnlockResolver{
		ExplicitPassphrase: credential,
		LookupEnv:          func(string) string { return "" },
		ReadRemembered:     func() (string, error) { return "", nil },
	}
	registry := unavailableLegacyKeyring{
		newSystemBackendRegistry(dir, resolver).(*systemBackendRegistry),
	}
	return newLocalStoreWithRegistry(BackendFile, idx.path, registry), blob
}

func TestLegacyFileMutationRecoversProtectionWithoutClaimingOwnership(t *testing.T) {
	s, blob := legacyRecoveryFixture(t, legacyRecoveryPassphrase)
	require.NoError(
		t,
		s.Set(testContext, legacyRecoveryNewName, legacyRecoveryNewValue, KindSecret),
	)
	idx, err := loadIndex(s.indexPath)
	require.NoError(t, err)
	require.Equal(t, string(keySourcePassphrase), idx.data.KeySource)
	old, exists := idx.get(testContext, legacyRecoveryOldName)
	require.True(t, exists)
	require.Empty(t, old.Backend, "unavailable keyring prevents proving legacy ownership")
	current, exists := idx.get(testContext, legacyRecoveryNewName)
	require.True(t, exists)
	require.Equal(t, BackendFile, current.Backend)
	values, err := blob.load()
	require.NoError(t, err)
	require.Equal(t, legacyRecoveryOldValue, values[backendKey(testContext, legacyRecoveryOldName)])
	require.Equal(t, legacyRecoveryNewValue, values[backendKey(testContext, legacyRecoveryNewName)])
	require.NoFileExists(t, filepath.Join(filepath.Dir(s.indexPath), KeyFileName))
}

func TestLegacyFileRecoveryFailureDoesNotRewriteStore(t *testing.T) {
	for _, tc := range []struct {
		name       string
		credential string
		want       error
	}{
		{"missing credential", "", ErrUnlockRequired},
		{"incorrect credential", "incorrect fixture credential", ErrUnlockFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, blob := legacyRecoveryFixture(t, tc.credential)
			catalog, err := os.ReadFile(s.indexPath) // #nosec G304 -- temporary test catalog.
			require.NoError(t, err)
			ciphertext, err := os.ReadFile(blob.path) // #nosec G304 -- temporary test ciphertext.
			require.NoError(t, err)
			err = s.Set(testContext, legacyRecoveryNewName, legacyRecoveryNewValue, KindSecret)
			require.ErrorIs(t, err, tc.want)
			require.False(t, errors.Is(err, ErrStoreCorrupt))
			afterCatalog, err := os.ReadFile(s.indexPath) // #nosec G304 -- temporary test catalog.
			require.NoError(t, err)
			afterCiphertext, err := os.ReadFile(
				blob.path,
			) // #nosec G304 -- temporary test ciphertext.
			require.NoError(t, err)
			require.Equal(t, catalog, afterCatalog)
			require.Equal(t, ciphertext, afterCiphertext)
			require.NoFileExists(t, filepath.Join(filepath.Dir(s.indexPath), KeyFileName))
		})
	}
}
