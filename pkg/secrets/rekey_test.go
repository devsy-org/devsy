package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testRekeyBackendKey     = testContext + "/" + testResolverToken
	testRekeyProtectedValue = "protected-value"
)

func testProtectionManager(t *testing.T, dir, passphrase string) *ProtectionManager {
	t.Helper()
	p := NewProtectionManager(dir, DefaultUnlockResolver{ExplicitPassphrase: passphrase})
	p.readRemembered = func() (string, error) { return "", nil }
	p.forgetRemembered = func() error { return nil }
	return p
}

func TestRekeyCrashRecovery(t *testing.T) {
	for _, phase := range []string{"encrypted", "journal", "swapped", "metadata", "verified", "committed", "cleanup"} {
		t.Run(phase, func(t *testing.T) {
			oldPassphrase := "old-long-test-passphrase"
			newPassphrase := "new-long-test-passphrase"
			p := testPassphraseProtectionFixture(t, oldPassphrase)
			dir := p.dir
			path := filepath.Join(dir, EncryptedFileName)
			before, err := os.ReadFile(path) // #nosec G304 -- fixed fixture in temporary directory.
			require.NoError(t, err)
			crashed := errors.New("simulated process interruption")
			p.phase = func(at string) error {
				if at == phase {
					return crashed
				}
				return nil
			}
			if err = p.ChangePassphrase(newPassphrase); !errors.Is(err, crashed) {
				t.Fatalf("expected injected crash, got %v", err)
			}
			// #nosec G304 -- fixed journal fixture in temporary directory.
			if raw, err := os.ReadFile(
				filepath.Join(dir, rekeyJournalName),
			); err == nil {
				for _, sensitive := range []string{oldPassphrase, newPassphrase, testRekeyProtectedValue} {
					require.NotContains(t, string(raw), sensitive)
				}
			}
			unlock, err := acquireFlock(dir, IndexFileName+".lock")
			require.NoError(t, err)
			require.NoError(t, RecoverRekey(dir))
			require.NoError(t, RecoverRekey(dir))
			unlock()
			passphrase := oldPassphrase
			if phase == "committed" || phase == "cleanup" {
				passphrase = newPassphrase
			} else {
				after, err := os.ReadFile(
					path,
				) // #nosec G304 -- fixed fixture in temporary directory.
				require.NoError(t, err)
				require.Equal(t, before, after, "rollback did not restore original ciphertext")
			}
			key, err := passphraseFileKey(passphrase)
			require.NoError(t, err)
			values, err := newFileBackend(path, key).load()
			require.NoError(t, err)
			require.Equal(
				t,
				testRekeyProtectedValue,
				values[testRekeyBackendKey],
				"secret value lost after recovery",
			)
		})
	}
}

func TestRekeyAutomaticTransitionAndReset(t *testing.T) {
	dir := t.TempDir()
	// Avoid dependency on an OS credential service by directly initializing
	// the supported local automatic key source.
	key, err := resolveAutoKey(
		dir,
		fileKeyStore{path: filepath.Join(dir, KeyFileName)},
		keySourceAutoFile,
	)
	require.NoError(t, err)
	require.NoError(
		t,
		newFileBackend(
			filepath.Join(dir, EncryptedFileName),
			key,
		).store(map[string]string{"one/TOKEN": "rekey-first-value", "two/TOKEN": "second"}), // #nosec G101 -- fixtures.
	)
	idx, err := loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	idx.data.KeySource = string(keySourceAutoFile)
	for _, ctx := range []string{"one", "two"} {
		idx.put(
			SecretMeta{
				Name:    testResolverToken,
				Context: ctx,
				Kind:    KindSecret,
				Backend: BackendFile,
			},
		)
	}
	idx.put(SecretMeta{Name: "KEYRING", Context: "one", Kind: KindSecret, Backend: BackendKeyring})
	require.NoError(t, idx.save())
	envPath := filepath.Join(dir, "env.yaml")
	require.NoError(t, os.WriteFile(envPath, []byte("untouched"), 0o600))
	p := testProtectionManager(t, dir, "")
	require.NoError(t, p.SetPassphrase("test-long-passphrase"))
	idx, err = loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	require.Equal(t, string(keySourcePassphrase), idx.data.KeySource)
	p.resolver = DefaultUnlockResolver{ExplicitPassphrase: "test-long-passphrase"}
	require.NoError(t, p.RemovePassphrase())
	idx, err = loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	automatic, err := openExistingFileKeyWithResolver(dir, idx, DefaultUnlockResolver{})
	require.NoError(t, err)
	value, err := newFileBackend(filepath.Join(dir, EncryptedFileName), automatic).get("two/TOKEN")
	require.NoError(t, err)
	require.Equal(t, "second", value)
	quarantine, err := p.ResetFileStore()
	require.NoError(t, err)
	require.FileExists(t, quarantine)
	idx, err = loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	if len(fileEntries(idx)) != 0 || idx.data.KeySource != "" {
		t.Fatal("reset left file metadata")
	}
	if _, exists := idx.get("one", "KEYRING"); !exists {
		t.Fatal("reset removed keyring entry")
	}
	// #nosec G304 -- environment fixture in temporary directory.
	if raw, err := os.ReadFile(envPath); err != nil || string(raw) != "untouched" {
		t.Fatal("reset changed environment store")
	}
}

func TestRememberVerifiesCredentialAndForgetDoesNotChangeBlob(t *testing.T) {
	dir := t.TempDir()
	credential := "remember-test-passphrase"
	key, err := passphraseFileKey(credential)
	require.NoError(t, err)
	path := filepath.Join(dir, EncryptedFileName)
	require.NoError(
		t,
		newFileBackend(path, key).store(map[string]string{testRekeyBackendKey: "plaintext"}),
	)
	idx, err := loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	idx.data.KeySource = string(keySourcePassphrase)
	require.NoError(t, idx.save())
	p := testProtectionManager(t, dir, credential)
	saved := ""
	p.saveRemembered = func(value string) error { saved = value; return nil }
	p.readRemembered = func() (string, error) { return saved, nil }
	p.forgetRemembered = func() error { saved = ""; return nil }
	if err = p.Remember("wrong-passphrase"); !errors.Is(err, ErrUnlockFailed) {
		t.Fatalf("unverified remember accepted: %v", err)
	}
	require.Equal(t, "", saved, "stored invalid unlock material")
	require.NoError(t, p.Remember(credential))
	status, err := p.Status()
	require.NoError(t, err)
	require.True(t, status.Remembered)
	before, err := os.ReadFile(path) // #nosec G304 -- fixed fixture in temporary directory.
	require.NoError(t, err)
	require.NoError(t, p.Forget())
	after, err := os.ReadFile(path) // #nosec G304 -- fixed fixture in temporary directory.
	require.NoError(t, err)
	require.Empty(t, saved, "forget preserved credential")
	require.Equal(t, before, after, "forget altered store")
}

func TestRekeyRecoveryPreservesConcurrentCatalogMigration(t *testing.T) {
	dir := t.TempDir()
	p := testProtectionManager(t, dir, "")
	require.NoError(t, p.SetPassphrase("original-test-passphrase"))
	p.resolver = DefaultUnlockResolver{ExplicitPassphrase: "original-test-passphrase"}
	p.phase = func(phase string) error {
		if phase == "metadata" {
			return errors.New("crash")
		}
		return nil
	}
	if err := p.ChangePassphrase("replacement-test-passphrase"); err == nil {
		t.Fatal("crash injection failed")
	}
	idx, err := loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	idx.put(
		SecretMeta{
			Name:    "ADDED_BY_MIGRATION",
			Context: testContext,
			Kind:    KindSecret,
			Backend: BackendKeyring,
		},
	)
	require.NoError(t, idx.save())
	require.NoError(t, RecoverRekey(dir))
	idx, err = loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	if _, exists := idx.get(testContext, "ADDED_BY_MIGRATION"); !exists {
		t.Fatal("recovery reverted unrelated catalog migration")
	}
}

func TestResetCrashRecovery(t *testing.T) {
	for _, phase := range []string{"reset-journal", "reset-quarantined", "reset-metadata", "reset-committed"} {
		t.Run(phase, func(t *testing.T) {
			p := testAutomaticProtectionFixture(t)
			dir := p.dir
			blob := filepath.Join(dir, EncryptedFileName)
			before, err := os.ReadFile(blob) // #nosec G304 -- fixed fixture in temporary directory.
			require.NoError(t, err)
			p.phase = func(at string) error {
				if at == phase {
					return errors.New("simulated crash")
				}
				return nil
			}
			if _, err = p.ResetFileStore(); err == nil {
				t.Fatal("reset fault injection did not fire")
			}
			require.NoError(t, RecoverRekey(dir))
			require.NoError(t, RecoverRekey(dir))
			idx, err := loadIndex(filepath.Join(dir, IndexFileName))
			require.NoError(t, err)
			if phase == "reset-committed" {
				require.Empty(t, fileEntries(idx))
				require.Empty(t, idx.data.KeySource)
				require.NoFileExists(t, blob)
				quarantines, err := filepath.Glob(filepath.Join(dir, "secrets.enc.quarantine-*"))
				require.NoError(t, err)
				require.Len(t, quarantines, 1, "committed reset lost quarantine")
			} else {
				// #nosec G304 -- fixed ciphertext fixture in temporary directory.
				after, err := os.ReadFile(blob)
				require.NoError(t, err)
				require.Equal(t, before, after, "reset rollback did not restore ciphertext")
				require.Len(t, fileEntries(idx), 1)
				require.Equal(t, string(keySourceAutoFile), idx.data.KeySource)
			}
		})
	}
}

func TestResetRequiresUnchangedConfirmationList(t *testing.T) {
	dir := t.TempDir()
	idx, err := loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	idx.put(
		SecretMeta{
			Name:    "ADDED_AFTER_CONFIRMATION",
			Context: testContext,
			Kind:    KindSecret,
			Backend: BackendFile,
		},
	)
	require.NoError(t, idx.save())
	p := testProtectionManager(t, dir, "")
	if _, err = p.ResetFileStoreIfUnchanged([]SecretMeta{}); err == nil {
		t.Fatal("reset removed an unconfirmed entry")
	}
	idx, err = loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	if len(fileEntries(idx)) != 1 {
		t.Fatal("failed confirmation check mutated the catalog")
	}
}

func TestProtectionStatusNeverPromptsAndRememberRequiresCiphertext(t *testing.T) {
	dir := t.TempDir()
	idx, err := loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	idx.data.KeySource = string(keySourcePassphrase)
	require.NoError(t, idx.save())
	prompted := false
	p := testProtectionManager(t, dir, "")
	p.resolver = DefaultUnlockResolver{
		LookupEnv:      func(string) string { return "" },
		ReadRemembered: func() (string, error) { return "", nil },
		Prompt:         func(context.Context, UnlockRequest) (string, error) { prompted = true; return "", nil },
	}
	require.ErrorIs(t, p.Remember("unverifiable-passphrase"), ErrSecretNotFound)
	status, err := p.Status()
	require.NoError(t, err)
	require.Equal(t, SecretMissing, status.Availability)
	require.ErrorIs(t, p.ChangePassphrase("replacement-for-missing-blob"), ErrSecretNotFound)
	key, err := passphraseFileKey("actual-passphrase")
	require.NoError(t, err)
	require.NoError(
		t,
		newFileBackend(filepath.Join(dir, EncryptedFileName), key).store(map[string]string{}),
	)
	status, err = p.Status()
	require.NoError(t, err)
	require.Equal(t, SecretLocked, status.Availability)
	require.False(t, prompted, "status prompted for a passphrase")
	if err = p.SetPassphrase(
		"replacement-passphrase",
	); err == nil ||
		!strings.Contains(err.Error(), "change-passphrase") {
		t.Fatal("set-passphrase silently changed passphrase protection")
	}
}

func testAutomaticProtectionFixture(t *testing.T) *ProtectionManager {
	t.Helper()
	dir := t.TempDir()
	key, err := resolveAutoKey(
		dir,
		fileKeyStore{path: filepath.Join(dir, KeyFileName)},
		keySourceAutoFile,
	)
	require.NoError(t, err)
	values := map[string]string{testRekeyBackendKey: "keep-after-rollback"}
	require.NoError(t, newFileBackend(filepath.Join(dir, EncryptedFileName), key).store(values))
	idx, err := loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	idx.data.KeySource = string(keySourceAutoFile)
	idx.put(
		SecretMeta{
			Name:    testResolverToken,
			Context: testContext,
			Kind:    KindSecret,
			Backend: BackendFile,
		},
	)
	require.NoError(t, idx.save())
	return testProtectionManager(t, dir, "")
}

func TestInvalidRecoveryJournalDoesNotChangeStore(t *testing.T) {
	dir := t.TempDir()
	blob := filepath.Join(dir, EncryptedFileName)
	require.NoError(t, os.WriteFile(blob, []byte("original ciphertext fixture"), 0o600))
	j := rekeyJournal{Version: 1, OldSource: "unknown", NewSource: string(keySourcePassphrase)}
	require.NoError(t, writeRekeyJournal(dir, j))
	require.Error(t, RecoverRekey(dir))
	raw, err := os.ReadFile(blob) // #nosec G304 -- fixed fixture in temporary directory.
	require.NoError(t, err)
	require.Equal(t, "original ciphertext fixture", string(raw))
	j.OldSource = ""
	j.Reset = true
	j.Quarantine = "secrets.enc.quarantine-test"
	j.Entries = []SecretMeta{
		{
			Name:    testResolverToken,
			Kind:    KindSecret,
			Backend: BackendFile,
			Value:   "plaintext fixture",
		},
	}
	require.NoError(t, writeRekeyJournal(dir, j))
	require.Error(t, RecoverRekey(dir))
	raw, err = os.ReadFile(blob) // #nosec G304 -- fixed fixture in temporary directory.
	require.NoError(t, err)
	require.Equal(t, "original ciphertext fixture", string(raw))
}

func testPassphraseProtectionFixture(t *testing.T, passphrase string) *ProtectionManager {
	t.Helper()
	dir := t.TempDir()
	key, err := passphraseFileKey(passphrase)
	require.NoError(t, err)
	values := map[string]string{testRekeyBackendKey: testRekeyProtectedValue}
	require.NoError(t, newFileBackend(filepath.Join(dir, EncryptedFileName), key).store(values))
	idx, err := loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	idx.data.KeySource = string(keySourcePassphrase)
	idx.put(
		SecretMeta{
			Name:    testResolverToken,
			Context: testContext,
			Kind:    KindSecret,
			Backend: BackendFile,
		},
	)
	require.NoError(t, idx.save())
	return testProtectionManager(t, dir, passphrase)
}
