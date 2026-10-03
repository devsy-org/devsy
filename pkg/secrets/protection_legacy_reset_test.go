package secrets

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	legacyResetEntryName  = "LEGACY_TOKEN"
	knownKeyringEntryName = "KEYRING_TOKEN"
	legacyResetCiphertext = "ciphertext fixture"
)

func TestProtectionRefusesResetWithUnownedSecretsAndCiphertext(t *testing.T) {
	for _, kind := range []Kind{KindSecret, ""} {
		t.Run(string(kind), func(t *testing.T) {
			p, idx := newLegacyResetFixture(t, kind, true)
			rememberedReads := 0
			p.readRemembered = func() (string, error) { rememberedReads++; return "", nil }
			beforeIndex, err := os.ReadFile(idx.path) // #nosec G304 -- test config fixture.
			require.NoError(t, err)
			for _, inspect := range []func() (ProtectionStatus, error){p.CatalogStatus, p.Status} {
				status, statusErr := inspect()
				require.NoError(t, statusErr)
				require.Equal(t, string(keySourcePassphrase), status.KeySource)
				require.Equal(t, SecretStateUnknown, status.Availability)
				require.Equal(t, "ownership_unknown", status.ReasonCode)
				require.Len(t, status.FileEntries, 1)
			}
			require.Equal(t, 1, rememberedReads, "only Status inspects remembered credentials")
			_, err = p.ResetFileStore()
			require.ErrorIs(t, err, ErrStateIndeterminate)
			_, err = p.ResetFileStoreIfUnchanged(fileEntries(idx))
			require.ErrorIs(t, err, ErrStateIndeterminate)
			require.Equal(
				t,
				1,
				rememberedReads,
				"refused resets must not inspect the credential service",
			)
			afterIndex, err := os.ReadFile(idx.path) // #nosec G304 -- test config fixture.
			require.NoError(t, err)
			require.Equal(t, beforeIndex, afterIndex, "refused reset mutated metadata")
			ciphertext, err := os.ReadFile(
				filepath.Join(p.dir, EncryptedFileName),
			) // #nosec G304 -- test config fixture.
			require.NoError(t, err)
			require.Equal(t, legacyResetCiphertext, string(ciphertext))
			matches, err := filepath.Glob(filepath.Join(p.dir, "secrets.enc.quarantine-*"))
			require.NoError(t, err)
			require.Empty(t, matches)
			require.NoFileExists(t, filepath.Join(p.dir, rekeyJournalName))
		})
	}
}

func TestProtectionResetPreservesUnownedSecretsWithoutCiphertext(t *testing.T) {
	p, _ := newLegacyResetFixture(t, KindSecret, false)
	// Absence of ciphertext means resetting known file entries cannot destroy
	// values belonging to entries with unresolved ownership.
	p.readRemembered = func() (string, error) { return "", nil }
	_, err := p.CatalogStatus()
	require.NoError(t, err)
	_, err = p.ResetFileStore()
	require.NoError(t, err)
	idx, err := loadIndex(filepath.Join(p.dir, IndexFileName))
	require.NoError(t, err)
	require.Empty(t, fileEntries(idx))
	unknown, exists := idx.get("legacy", legacyResetEntryName)
	require.True(t, exists)
	require.Empty(t, unknown.Backend)
	_, exists = idx.get("keyring", knownKeyringEntryName)
	require.True(t, exists)
}

func TestPlaintextLegacyEnvDoesNotBlockFileReset(t *testing.T) {
	p, idx := newLegacyResetFixture(t, KindEnv, true)
	p.readRemembered = func() (string, error) { return "", nil }
	_, err := p.CatalogStatus()
	require.NoError(t, err)
	quarantine, err := p.ResetFileStore()
	require.NoError(t, err)
	require.FileExists(t, quarantine)
	idx, err = loadIndex(idx.path)
	require.NoError(t, err)
	env, exists := idx.get("legacy", legacyResetEntryName)
	require.True(t, exists)
	require.Equal(t, KindEnv, env.Kind)
	require.Equal(t, "plaintext env fixture", env.Value)
	_, exists = idx.get("keyring", knownKeyringEntryName)
	require.True(t, exists)
}

func newLegacyResetFixture(t *testing.T, kind Kind, blob bool) (*ProtectionManager, *index) {
	t.Helper()
	dir := t.TempDir()
	idx, err := loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	idx.data.KeySource = string(keySourcePassphrase)
	idx.put(
		SecretMeta{Context: "known", Name: "FILE_TOKEN", Kind: KindSecret, Backend: BackendFile},
	)
	idx.put(
		SecretMeta{
			Context: "legacy",
			Name:    legacyResetEntryName,
			Kind:    kind,
			Value:   "plaintext env fixture",
		},
	)
	idx.put(
		SecretMeta{
			Context: "keyring",
			Name:    knownKeyringEntryName,
			Kind:    KindSecret,
			Backend: BackendKeyring,
		},
	)
	require.NoError(t, idx.save())
	if blob {
		require.NoError(
			t,
			os.WriteFile(
				filepath.Join(dir, EncryptedFileName),
				[]byte(legacyResetCiphertext),
				0o600,
			),
		)
	}
	resolver := UnlockMaterialResolverFunc(
		func(_ context.Context, _ UnlockRequest) (UnlockMaterial, error) {
			t.Fatal("reset ownership guard must not resolve unlock material")
			return UnlockMaterial{}, ErrUnlockRequired
		},
	)
	p := NewProtectionManager(dir, resolver)
	p.readRemembered = func() (string, error) { t.Fatal("reset ownership guard must not inspect keyring"); return "", nil }
	return p, idx
}
