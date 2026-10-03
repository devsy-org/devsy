package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/stretchr/testify/require"
)

const testInspectedValue = "test-inspected-value"

func TestUnlockResolverPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential")
	require.NoError(t, os.WriteFile(path, []byte("file value\n"), 0o600))
	env := map[string]string{EnvPassphrase: "env value", EnvPassphraseFile: path}
	resolver := DefaultUnlockResolver{
		ExplicitPassphrase: "explicit value",
		LookupEnv:          func(k string) string { return env[k] },
		ReadRemembered:     func() (string, error) { return "remembered value", nil },
		Prompt:             func(context.Context, UnlockRequest) (string, error) { return "prompt value", nil },
	}
	for _, expected := range []PassphraseSource{
		PassphraseExplicit, PassphraseEnv, PassphraseFile, PassphraseRemembered, PassphrasePrompt,
	} {
		material, err := resolver.ResolvePassphrase(
			context.Background(),
			UnlockRequest{AllowPrompt: true},
		)
		require.NoError(t, err)
		require.Equal(t, expected, material.Source)
		switch expected {
		case PassphraseExplicit:
			resolver.ExplicitPassphrase = ""
		case PassphraseEnv:
			delete(env, EnvPassphrase)
		case PassphraseFile:
			delete(env, EnvPassphraseFile)
		case PassphraseRemembered:
			resolver.ReadRemembered = func() (string, error) { return "", errors.New("unavailable") }
		}
	}
	_, err := resolver.ResolvePassphrase(context.Background(), UnlockRequest{})
	require.ErrorIs(t, err, ErrUnlockRequired)
}

func TestPassphraseFilePreservesSpacesAndOnlyOneNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential")
	for _, tc := range []struct{ raw, want string }{
		{"  phrase  \r\n", "  phrase  "},
		{"phrase\n\n", "phrase\n"},
		{"phrase", "phrase"},
	} {
		require.NoError(t, os.WriteFile(path, []byte(tc.raw), 0o600))
		value, err := readPassphraseFile(path)
		require.NoError(t, err)
		require.Equal(t, tc.want, value)
	}
	require.NoError(t, os.WriteFile(path, []byte("\n"), 0o600))
	_, err := readPassphraseFile(path)
	require.Error(t, err)
	require.NoError(t, os.WriteFile(path, make([]byte, MaxPassphraseFileSize+1), 0o600))
	_, err = readPassphraseFile(path)
	require.Error(t, err)
}

func TestInspectLockedIsPerEntryAndNeverPrompts(t *testing.T) {
	dir := t.TempDir()
	idx, err := loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	idx.data.KeySource = string(keySourcePassphrase)
	idx.put(SecretMeta{Name: "FILE", Context: testContext, Kind: KindSecret, Backend: BackendFile})
	require.NoError(t, idx.save())
	calls := 0
	resolver := DefaultUnlockResolver{
		LookupEnv:      func(string) string { return "" },
		ReadRemembered: func() (string, error) { return "", nil },
		Prompt:         func(context.Context, UnlockRequest) (string, error) { calls++; return "prompt", nil },
	}
	registry := newSystemBackendRegistry(dir, resolver).(*systemBackendRegistry)
	registry.allowPrompt = true
	s := newLocalStoreWithRegistry(BackendFile, idx.path, registry)
	entries, err := s.Inspect(testContext)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, SecretLocked, entries[0].Availability)
	require.Zero(t, calls)
	metadata, err := s.ListMeta(testContext)
	require.NoError(t, err)
	require.Len(t, metadata, 1)
	require.Zero(t, calls)
}

func TestExistingAutoFileStoreIgnoresInvocationPassphrase(t *testing.T) {
	dir := t.TempDir()
	key, err := resolveAutoKey(
		dir,
		fileKeyStore{path: filepath.Join(dir, KeyFileName)},
		keySourceAutoFile,
	)
	require.NoError(t, err)
	require.NoError(
		t,
		newFileBackend(filepath.Join(dir, EncryptedFileName), key).set("default/FIRST", "first"),
	)
	idx, err := loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	idx.data.KeySource = string(keySourceAutoFile)
	registry := newSystemBackendRegistry(
		dir,
		UnlockMaterialResolverFunc(func(context.Context, UnlockRequest) (UnlockMaterial, error) {
			t.Fatal("auto key must not evaluate passphrase resolver")
			return UnlockMaterial{}, nil
		}),
	)
	b, err := registry.Open(BackendFile, idx, BackendInitializeNew)
	require.NoError(t, err)
	require.NoError(t, b.set("default/SECOND", "second"))
	value, err := newFileBackend(filepath.Join(dir, EncryptedFileName), key).get("default/FIRST")
	require.NoError(t, err)
	require.Equal(t, "first", value)
}

func TestUnlockFailedMessageDoesNotExposeCause(t *testing.T) {
	err := &UnlockFailedError{
		Backend: BackendFile,
		Cause:   errors.New("credential: extremely private"),
	}
	require.NotContains(t, err.Error(), "extremely private")
	require.ErrorIs(t, err, ErrUnlockFailed)
}

type countingIdentity struct {
	inner age.Identity
	calls *int
}

func (i countingIdentity) Unwrap(stanzas []*age.Stanza) ([]byte, error) {
	*i.calls++
	return i.inner.Unwrap(stanzas)
}

func TestInspectDecryptsFileOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	key := &fileKey{recipient: identity.Recipient(), identity: identity, source: keySourceAutoFile}
	fb := newFileBackend(filepath.Join(dir, EncryptedFileName), key)
	idx, err := loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	values := map[string]string{}
	for n := range 20 {
		name := fmt.Sprintf("TOKEN_%d", n)
		idx.put(
			SecretMeta{Name: name, Context: testContext, Kind: KindSecret, Backend: BackendFile},
		)
		values[backendKey(testContext, name)] = testInspectedValue
	}
	require.NoError(t, fb.store(values))
	require.NoError(t, idx.save())
	calls := 0
	key.identity = countingIdentity{inner: identity, calls: &calls}
	s := newLocalStore(fb, idx.path)
	result, err := s.Inspect(testContext)
	require.NoError(t, err)
	require.Len(t, result, 20)
	require.Equal(t, 1, calls)
	for _, entry := range result {
		require.Equal(t, SecretAvailable, entry.Availability)
	}
}

func TestWrongHigherPriorityCredentialNeverFallsBack(t *testing.T) {
	dir := t.TempDir()
	key, err := passphraseFileKey("correct phrase")
	require.NoError(t, err)
	require.NoError(
		t,
		newFileBackend(
			filepath.Join(dir, EncryptedFileName),
			key,
		).set("default/TOKEN", testInspectedValue),
	)
	idx, err := loadIndex(filepath.Join(dir, IndexFileName))
	require.NoError(t, err)
	idx.data.KeySource = string(keySourcePassphrase)
	resolver := DefaultUnlockResolver{
		ExplicitPassphrase: "wrong phrase",
		LookupEnv: func(string) string {
			t.Fatal("explicit material must prevent fallback")
			return "correct phrase"
		},
		ReadRemembered: func() (string, error) { t.Fatal("must never fallback"); return "correct phrase", nil },
	}
	registry := newSystemBackendRegistry(dir, resolver)
	b, err := registry.Open(BackendFile, idx, BackendOpenExisting)
	require.NoError(t, err)
	_, err = b.get("default/TOKEN")
	require.ErrorIs(t, err, ErrUnlockFailed)
}

func TestMetadataNeverRepairsOrOpensLegacyOwnership(t *testing.T) {
	s, _ := newLegacyStore(
		t,
		"    LEGACY:\n      name: LEGACY\n      context: default\n      kind: secret\n",
	)
	s.backends = forbiddenBackendRegistry{t: t}
	original, err := os.ReadFile(s.indexPath)
	require.NoError(t, err)
	meta, err := s.Meta(testContext, "LEGACY")
	require.NoError(t, err)
	require.Empty(t, meta.Backend)
	metadata, err := s.ListMeta(testContext)
	require.NoError(t, err)
	require.Len(t, metadata, 1)
	require.Empty(t, metadata[0].Backend)
	final, err := os.ReadFile(s.indexPath)
	require.NoError(t, err)
	require.Equal(t, original, final)
}

type forbiddenBackendRegistry struct{ t *testing.T }

func (r forbiddenBackendRegistry) Open(Backend, *index, BackendOpenIntent) (backend, error) {
	r.t.Fatal("metadata opened a secret backend")
	return nil, nil
}

func (r forbiddenBackendRegistry) ResolveForNewSecret(Backend, *index) (Backend, error) {
	r.t.Fatal("metadata resolved a secret backend")
	return "", nil
}

func (r forbiddenBackendRegistry) Probe(Backend, *index, string) (bool, bool) {
	r.t.Fatal("metadata probed a secret backend")
	return false, false
}

func TestInitializationDoesNotReadFileOrRememberedUnlockMaterial(t *testing.T) {
	resolver := DefaultUnlockResolver{LookupEnv: func(name string) string {
		if name == EnvPassphraseFile {
			t.Fatal("initialization must not read passphrase file configuration")
		}
		return ""
	}, ReadRemembered: func() (string, error) {
		t.Fatal("initialization must not load remembered unlock material")
		return "", nil
	}}
	material, err := resolver.ResolvePassphrase(
		context.Background(),
		UnlockRequest{Purpose: "initialize"},
	)
	require.ErrorIs(t, err, ErrUnlockRequired)
	require.Empty(t, material.Passphrase)
}

func TestPassphraseFileRejectsFIFOWithoutBlocking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix FIFO fixture")
	}
	if _, err := exec.LookPath("mkfifo"); err != nil {
		t.Skip("mkfifo unavailable")
	}
	path := filepath.Join(t.TempDir(), "credential-fifo")
	// #nosec G204 -- fixed mkfifo executable and a generated temporary fixture path.
	require.NoError(t, exec.Command("mkfifo", path).Run())
	completed := make(chan error, 1)
	go func() { _, err := readPassphraseFile(path); completed <- err }()
	select {
	case err := <-completed:
		require.ErrorContains(t, err, "must be a regular file")
	case <-time.After(time.Second):
		t.Fatal("passphrase file reader blocked on FIFO")
	}
}
