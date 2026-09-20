package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/require"
)

const testContext = "default"

func TestResolveBackend_EnvOverridesContextOption(t *testing.T) {
	t.Setenv(EnvBackend, "keyring")
	cfg := &config.Config{
		DefaultContext: testContext,
		Contexts: map[string]*config.ContextConfig{
			testContext: {Options: map[string]config.OptionValue{
				config.ContextOptionSecretsBackend: {Value: "file"},
			}},
		},
	}
	if got := resolveBackend(cfg); got != BackendKeyring {
		t.Fatalf("env override = %q, want keyring", got)
	}
}

func TestResolveBackend_ContextOption(t *testing.T) {
	t.Setenv(EnvBackend, "")
	cfg := &config.Config{
		DefaultContext: testContext,
		Contexts: map[string]*config.ContextConfig{
			testContext: {Options: map[string]config.OptionValue{
				config.ContextOptionSecretsBackend: {Value: "file"},
			}},
		},
	}
	if got := resolveBackend(cfg); got != BackendFile {
		t.Fatalf("context option = %q, want file", got)
	}
}

func TestResolveBackend_DefaultsToAuto(t *testing.T) {
	t.Setenv(EnvBackend, "")
	cfg := &config.Config{
		DefaultContext: testContext,
		Contexts:       map[string]*config.ContextConfig{testContext: {}},
	}
	if got := resolveBackend(cfg); got != BackendAuto {
		t.Fatalf("empty preference = %q, want auto", got)
	}
}

func TestResolveBackend_IgnoresGarbageEnv(t *testing.T) {
	t.Setenv(EnvBackend, "nonsense")
	cfg := &config.Config{
		DefaultContext: testContext,
		Contexts: map[string]*config.ContextConfig{
			testContext: {Options: map[string]config.OptionValue{
				config.ContextOptionSecretsBackend: {Value: "keyring"},
			}},
		},
	}
	if got := resolveBackend(cfg); got != BackendKeyring {
		t.Fatalf("garbage env should fall through to context option, got %q", got)
	}
}

type mapBackend struct {
	values map[string]string
}

func newMapBackend() *mapBackend { return &mapBackend{values: map[string]string{}} }

type mapBackendRegistry struct {
	backends map[Backend]*mapBackend
}

func (r mapBackendRegistry) Open(kind Backend, _ *index, _ bool) (backend, error) {
	b, ok := r.backends[kind]
	if !ok {
		return nil, fmt.Errorf("missing test backend %q", kind)
	}
	return b, nil
}

func (r mapBackendRegistry) ResolveForNewSecret(preference Backend, _ *index) (Backend, error) {
	if _, ok := r.backends[preference]; !ok {
		return "", fmt.Errorf("missing test backend %q", preference)
	}
	return preference, nil
}

func (r mapBackendRegistry) Probe(kind Backend, _ *index, key string) (bool, bool) {
	b, ok := r.backends[kind]
	if !ok {
		return false, false
	}
	return probePresence(b, key)
}

func (m *mapBackend) set(key, value string) error {
	m.values[key] = value
	return nil
}

func (m *mapBackend) get(key string) (string, error) {
	v, ok := m.values[key]
	if !ok {
		return "", ErrSecretNotFound
	}
	return v, nil
}

func (m *mapBackend) remove(key string) error {
	delete(m.values, key)
	return nil
}

func newTestStore(t *testing.T, b backend) *localStore {
	t.Helper()
	return newLocalStore(b, filepath.Join(t.TempDir(), IndexFileName))
}

func TestStore_SetGetDelete(t *testing.T) {
	s := newTestStore(t, newMapBackend())

	if err := s.Set("default", "API_KEY", "abc123", KindSecret); err != nil {
		t.Fatal(err)
	}

	got, err := s.Get("default", "API_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if got != "abc123" {
		t.Fatalf("Get = %q, want %q", got, "abc123")
	}

	if err := s.Delete("default", "API_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("default", "API_KEY"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Get after delete = %v, want ErrSecretNotFound", err)
	}
}

func TestStore_GetMissing(t *testing.T) {
	s := newTestStore(t, newMapBackend())
	if _, err := s.Get("default", "NOPE"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Get missing = %v, want ErrSecretNotFound", err)
	}
}

func TestStore_DeleteIdempotent(t *testing.T) {
	s := newTestStore(t, newMapBackend())
	if err := s.Delete("default", "NEVER_EXISTED"); err != nil {
		t.Fatalf("Delete of absent secret should be nil, got %v", err)
	}
}

func TestStore_ContextIsolation(t *testing.T) {
	s := newTestStore(t, newMapBackend())
	if err := s.Set("ctx-a", "TOKEN", "a", KindSecret); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("ctx-b", "TOKEN", "b", KindSecret); err != nil {
		t.Fatal(err)
	}

	a, _ := s.Get("ctx-a", "TOKEN")
	b, _ := s.Get("ctx-b", "TOKEN")
	if a != "a" || b != "b" {
		t.Fatalf("context isolation broken: a=%q b=%q", a, b)
	}

	list, err := s.List("ctx-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "TOKEN" {
		t.Fatalf("List(ctx-a) = %+v, want single TOKEN", list)
	}
}

func TestStore_ListSorted(t *testing.T) {
	s := newTestStore(t, newMapBackend())
	for _, n := range []string{"ZED", "ALPHA", "MIKE"} {
		if err := s.Set("default", n, "v", KindSecret); err != nil {
			t.Fatal(err)
		}
	}

	list, err := s.List("default")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ALPHA", "MIKE", "ZED"}
	for i, meta := range list {
		if meta.Name != want[i] {
			t.Fatalf("List[%d] = %q, want %q", i, meta.Name, want[i])
		}
	}
}

func TestStore_ListFlagsMissingValues(t *testing.T) {
	mb := newMapBackend()
	s := newTestStore(t, mb)

	if err := s.Set("default", "PRESENT", "v", KindSecret); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("default", "GONE", "v", KindSecret); err != nil {
		t.Fatal(err)
	}

	delete(mb.values, backendKey("default", "GONE"))

	list, err := s.List("default")
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]SecretMeta{}
	for _, m := range list {
		byName[m.Name] = m
	}
	if byName["PRESENT"].Orphaned {
		t.Error("PRESENT should not be orphaned")
	}
	if !byName["GONE"].Orphaned {
		t.Error("GONE should be flagged orphaned")
	}
}

func TestStore_SetUpdatePreservesCreated(t *testing.T) {
	s := newTestStore(t, newMapBackend())
	if err := s.Set("default", "K", "v1", KindSecret); err != nil {
		t.Fatal(err)
	}
	first, _ := s.List("default")
	created := first[0].Created

	if err := s.Set("default", "K", "v2", KindSecret); err != nil {
		t.Fatal(err)
	}
	second, _ := s.List("default")
	if !second[0].Created.Equal(created) {
		t.Errorf("Created changed on update: %v != %v", second[0].Created, created)
	}
	if v, _ := s.Get("default", "K"); v != "v2" {
		t.Errorf("value not updated: %q", v)
	}
}

func TestStore_UsesRecordedBackendAfterPreferenceChanges(t *testing.T) {
	keyring := newMapBackend()
	file := newMapBackend()
	indexPath := filepath.Join(t.TempDir(), IndexFileName)
	s := newLocalStoreWithRegistry(BackendKeyring, indexPath, mapBackendRegistry{
		backends: map[Backend]*mapBackend{
			BackendKeyring: keyring,
			BackendFile:    file,
		},
	})

	require.NoError(t, s.Set(testContext, "TOKEN", "v1", KindSecret))
	meta, err := s.Meta(testContext, "TOKEN")
	require.NoError(t, err)
	require.Equal(t, BackendKeyring, meta.Backend)

	s.preference = BackendFile
	got, err := s.Get(testContext, "TOKEN")
	require.NoError(t, err)
	require.Equal(t, "v1", got)
	require.NoError(t, s.Set(testContext, "TOKEN", "v2", KindSecret))
	list, err := s.List(testContext)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, BackendKeyring, list[0].Backend)
	got, err = s.Get(testContext, "TOKEN")
	require.NoError(t, err)
	require.Equal(t, "v2", got)
	require.NoError(t, s.Delete(testContext, "TOKEN"))
	require.NotContains(t, keyring.values, backendKey(testContext, "TOKEN"))
	require.NotContains(t, file.values, backendKey(testContext, "TOKEN"))
}

func TestValidateName(t *testing.T) {
	valid := []string{"API_KEY", "db_password", "TOKEN2", "a"}
	for _, n := range valid {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", n, err)
		}
	}

	invalid := []string{"", "with-dash", "with space", "ctx/name", "dollar$", "emoji😀"}
	for _, n := range invalid {
		if err := ValidateName(n); err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", n)
		}
	}
}

func TestStore_InvalidNameRejected(t *testing.T) {
	s := newTestStore(t, newMapBackend())
	if err := s.Set("default", "bad name", "v", KindSecret); err == nil {
		t.Fatal("Set with invalid name should error")
	}
}

func newPassphraseBackend(t *testing.T, path, passphrase string) *fileBackend {
	t.Helper()
	key, err := resolveFileKey(t.TempDir(), passphrase)
	if err != nil {
		t.Fatal(err)
	}
	return newFileBackend(path, key)
}

func TestFileBackend_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), EncryptedFileName)
	fb := newPassphraseBackend(t, path, "correct horse battery staple")

	if err := fb.set("default/K", "value"); err != nil {
		t.Fatal(err)
	}
	got, err := fb.get("default/K")
	if err != nil {
		t.Fatal(err)
	}
	if got != "value" {
		t.Fatalf("get = %q, want %q", got, "value")
	}

	wrong := newPassphraseBackend(t, path, "wrong passphrase")
	if _, err := wrong.get("default/K"); err == nil {
		t.Fatal("get with wrong passphrase should fail")
	}

	if err := fb.remove("default/K"); err != nil {
		t.Fatal(err)
	}
	if _, err := fb.get("default/K"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("get after remove = %v, want ErrSecretNotFound", err)
	}
}

func TestResolveFileKey_PassphraseSource(t *testing.T) {
	key, err := resolveFileKey(t.TempDir(), "s3cr3t")
	if err != nil {
		t.Fatal(err)
	}
	if key.source != keySourcePassphrase {
		t.Fatalf("source = %q, want %q", key.source, keySourcePassphrase)
	}
}

func TestResolveAutoKey_CreatesThenReuses(t *testing.T) {
	dir := t.TempDir()
	store := fileKeyStore{path: filepath.Join(dir, KeyFileName)}

	first, err := resolveAutoKey(dir, store, keySourceAutoFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(store.path); statErr != nil {
		t.Fatalf("expected key file to be created: %v", statErr)
	}

	second, err := resolveAutoKey(dir, store, keySourceAutoFile)
	if err != nil {
		t.Fatal(err)
	}

	// The same persisted identity must be reused, or previously encrypted
	// secrets would become permanently undecryptable.
	if identityString(t, first) != identityString(t, second) {
		t.Fatal("auto key was regenerated on second resolve; must be stable")
	}
}

func TestFileBackend_AutoKeyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EncryptedFileName)
	store := fileKeyStore{path: filepath.Join(dir, KeyFileName)}

	key, err := resolveAutoKey(dir, store, keySourceAutoFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := newFileBackend(path, key).set("default/K", "auto-value"); err != nil {
		t.Fatal(err)
	}

	key2, err := resolveAutoKey(dir, store, keySourceAutoFile)
	if err != nil {
		t.Fatal(err)
	}
	got, err := newFileBackend(path, key2).get("default/K")
	if err != nil {
		t.Fatal(err)
	}
	if got != "auto-value" {
		t.Fatalf("get = %q, want %q", got, "auto-value")
	}
}

func legacyIndexYAML(entries string) string {
	return "contexts:\n  default:\n" + entries
}

func writeLegacyIndex(t *testing.T, entries string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), IndexFileName)
	if err := os.WriteFile(path, []byte(legacyIndexYAML(entries)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newLegacyStore(t *testing.T, entries string) (*localStore, map[Backend]*mapBackend) {
	t.Helper()
	backends := map[Backend]*mapBackend{
		BackendKeyring: newMapBackend(),
		BackendFile:    newMapBackend(),
	}
	s := newLocalStoreWithRegistry(
		BackendKeyring,
		writeLegacyIndex(t, entries),
		mapBackendRegistry{backends: backends},
	)
	return s, backends
}

func TestLoadIndex_ToleratesUnownedLegacySecret(t *testing.T) {
	path := writeLegacyIndex(
		t,
		"    UNOWNED:\n      name: UNOWNED\n      context: default\n      kind: secret\n",
	)

	if _, err := loadIndex(path); err != nil {
		t.Fatalf("loadIndex on legacy entry = %v, want nil", err)
	}
}

func TestLoadIndex_RejectsInvalidBackend(t *testing.T) {
	path := writeLegacyIndex(
		t,
		"    BAD:\n      name: BAD\n      context: default\n      kind: secret\n      backend: vault\n",
	)

	if _, err := loadIndex(path); err == nil ||
		!strings.Contains(err.Error(), "invalid secrets backend") {
		t.Fatalf("expected invalid backend error, got %v", err)
	}
}

func TestStore_RepairsProvenLegacyOwnership(t *testing.T) {
	s, backends := newLegacyStore(
		t,
		"    LEGACY:\n      name: LEGACY\n      context: default\n      kind: secret\n",
	)
	backends[BackendKeyring].values[backendKey(testContext, "LEGACY")] = "recovered"

	got, err := s.Get(testContext, "LEGACY")
	require.NoError(t, err)
	require.Equal(t, "recovered", got)

	meta, err := s.Meta(testContext, "LEGACY")
	require.NoError(t, err)
	require.Equal(t, BackendKeyring, meta.Backend)

	stored, err := os.ReadFile(s.indexPath)
	require.NoError(t, err)
	require.Contains(t, string(stored), "backend: keyring")

	list, err := s.List(testContext)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.False(t, list[0].Orphaned)

	require.NoError(t, s.Set(testContext, "LEGACY", "updated", KindSecret))
	got, err = s.Get(testContext, "LEGACY")
	require.NoError(t, err)
	require.Equal(t, "updated", got)

	require.NoError(t, s.Delete(testContext, "LEGACY"))
	require.NotContains(t, backends[BackendKeyring].values, backendKey(testContext, "LEGACY"))
}

func TestStore_UnownedValueNotFoundIsRecoverable(t *testing.T) {
	s, backends := newLegacyStore(
		t,
		"    GONE:\n      name: GONE\n      context: default\n      kind: secret\n",
	)

	if _, err := s.Get(testContext, "GONE"); err == nil ||
		!strings.Contains(err.Error(), "no proven owning backend") {
		t.Fatalf("expected actionable unowned error, got %v", err)
	}

	list, err := s.List(testContext)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.True(t, list[0].Orphaned)

	require.NoError(t, s.Set(testContext, "GONE", "fresh", KindSecret))
	got, err := s.Get(testContext, "GONE")
	require.NoError(t, err)
	require.Equal(t, "fresh", got)
	require.Equal(t, "fresh", backends[BackendKeyring].values[backendKey(testContext, "GONE")])

	require.NoError(t, s.Delete(testContext, "GONE"))
	_, err = s.Get(testContext, "GONE")
	require.ErrorIs(t, err, ErrSecretNotFound)
}

func TestStore_NeverAssignsAmbiguousOwnership(t *testing.T) {
	s, backends := newLegacyStore(
		t,
		"    BOTH:\n      name: BOTH\n      context: default\n      kind: secret\n",
	)
	backends[BackendKeyring].values[backendKey(testContext, "BOTH")] = "from-keyring"
	backends[BackendFile].values[backendKey(testContext, "BOTH")] = "from-file"

	if _, err := s.Get(testContext, "BOTH"); err == nil ||
		!strings.Contains(err.Error(), "no proven owning backend") {
		t.Fatalf("ambiguous ownership must not be guessed, got %v", err)
	}
	meta, err := s.Meta(testContext, "BOTH")
	require.NoError(t, err)
	require.Equal(t, Backend(""), meta.Backend)

	require.NoError(t, s.Set(testContext, "BOTH", "explicit", KindSecret))
	got, err := s.Get(testContext, "BOTH")
	require.NoError(t, err)
	require.Equal(t, "explicit", got)

	require.NoError(t, s.Delete(testContext, "BOTH"))
}

func TestStore_UnownedLegacySecretDoesNotBlockEnvWrites(t *testing.T) {
	s, _ := newLegacyStore(
		t,
		"    LEGACY:\n      name: LEGACY\n      context: default\n      kind: secret\n",
	)

	require.NoError(t, s.Set(testContext, "MY_VAR", "plain", KindEnv))
	got, err := s.Get(testContext, "MY_VAR")
	require.NoError(t, err)
	require.Equal(t, "plain", got)

	list, err := s.List(testContext)
	require.NoError(t, err)
	require.Len(t, list, 2)
}

func TestStore_DeleteUnownedRemovesFromEveryBackend(t *testing.T) {
	s, backends := newLegacyStore(
		t,
		"    BOTH:\n      name: BOTH\n      context: default\n      kind: secret\n",
	)
	backends[BackendKeyring].values[backendKey(testContext, "BOTH")] = "from-keyring"
	backends[BackendFile].values[backendKey(testContext, "BOTH")] = "from-file"

	require.NoError(t, s.Delete(testContext, "BOTH"))
	require.NotContains(t, backends[BackendKeyring].values, backendKey(testContext, "BOTH"))
	require.NotContains(t, backends[BackendFile].values, backendKey(testContext, "BOTH"))
	_, err := s.Get(testContext, "BOTH")
	require.ErrorIs(t, err, ErrSecretNotFound)
}

// A key file that already exists must never be overwritten, so a second
// initializer that generated its own identity still adopts the persisted one.
func TestFileKeyStore_SaveDoesNotOverwrite(t *testing.T) {
	store := fileKeyStore{path: filepath.Join(t.TempDir(), KeyFileName)}

	if err := store.save("first-key"); err != nil {
		t.Fatal(err)
	}
	if err := store.save("second-key"); err != nil {
		t.Fatal(err)
	}

	got, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if got != "first-key" {
		t.Fatalf("load = %q, want the first persisted key %q", got, "first-key")
	}
}

func identityString(t *testing.T, key *fileKey) string {
	t.Helper()
	id, ok := key.identity.(*age.X25519Identity)
	if !ok {
		t.Fatalf("identity is not X25519: %T", key.identity)
	}
	return id.String()
}

func TestStore_KeySourceMismatchIsClearError(t *testing.T) {
	indexPath := filepath.Join(t.TempDir(), IndexFileName)

	s := newLocalStore(newMapBackend(), indexPath)
	s.keySource = keySourceAutoFile
	if err := s.Set(testContext, "K", "v", KindSecret); err != nil {
		t.Fatal(err)
	}

	s2 := newLocalStore(newMapBackend(), indexPath)
	s2.keySource = keySourcePassphrase
	_, err := s2.Get(testContext, "K")
	if err == nil {
		t.Fatal("expected key-source mismatch error")
	}
	if !strings.Contains(err.Error(), "key source") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStore_NonSensitiveStoredInline(t *testing.T) {
	mb := newMapBackend()
	s := newTestStore(t, mb)

	if err := s.Set(testContext, "FOO", "bar", KindEnv); err != nil {
		t.Fatal(err)
	}

	// Non-sensitive values must NOT touch the backend.
	if _, ok := mb.values[backendKey(testContext, "FOO")]; ok {
		t.Fatal("non-sensitive value must not be written to the backend")
	}

	got, err := s.Get(testContext, "FOO")
	if err != nil {
		t.Fatal(err)
	}
	if got != "bar" {
		t.Fatalf("Get = %q, want %q", got, "bar")
	}
}

func TestStore_NonSensitiveNeverOrphaned(t *testing.T) {
	s := newTestStore(t, newMapBackend())
	if err := s.Set(testContext, "FOO", "bar", KindEnv); err != nil {
		t.Fatal(err)
	}

	list, err := s.List(testContext)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Orphaned {
		t.Fatalf("non-sensitive entry should never be orphaned: %+v", list)
	}
}

func TestStore_DeleteNonSensitive(t *testing.T) {
	s := newTestStore(t, newMapBackend())
	if err := s.Set(testContext, "FOO", "bar", KindEnv); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(testContext, "FOO"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(testContext, "FOO"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Get after delete = %v, want ErrSecretNotFound", err)
	}
}

func TestStore_SensitiveToNonSensitiveClearsBackend(t *testing.T) {
	mb := newMapBackend()
	s := newTestStore(t, mb)

	if err := s.Set(testContext, "TOKEN", "secret123", KindSecret); err != nil {
		t.Fatal(err)
	}
	if _, ok := mb.values[backendKey(testContext, "TOKEN")]; !ok {
		t.Fatal("sensitive value should be in the backend")
	}

	// Re-set as non-sensitive: the stale backend value must be removed.
	if err := s.Set(testContext, "TOKEN", "plaintext", KindEnv); err != nil {
		t.Fatal(err)
	}
	if _, ok := mb.values[backendKey(testContext, "TOKEN")]; ok {
		t.Fatal("stale sensitive value must be removed from the backend")
	}
	got, err := s.Get(testContext, "TOKEN")
	if err != nil {
		t.Fatal(err)
	}
	if got != "plaintext" {
		t.Fatalf("Get = %q, want %q", got, "plaintext")
	}
}

func TestStore_MetaHidesValueAndReportsKind(t *testing.T) {
	s := newTestStore(t, newMapBackend())
	if err := s.Set(testContext, "ENVVAR", "v", KindEnv); err != nil {
		t.Fatal(err)
	}
	meta, err := s.Meta(testContext, "ENVVAR")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Sensitive() {
		t.Error("env var should not be sensitive")
	}
	if meta.Value != "" {
		t.Error("Meta must not return the value")
	}
}

func TestStore_RepairsOwnershipWhenOtherBackendUnprobeable(t *testing.T) {
	backends := map[Backend]*mapBackend{BackendFile: newMapBackend()}
	backends[BackendFile].values[backendKey(testContext, "LEGACY")] = "recovered"
	s := newLocalStoreWithRegistry(
		BackendFile,
		writeLegacyIndex(
			t,
			"    LEGACY:\n      name: LEGACY\n      context: default\n      kind: secret\n",
		),
		mapBackendRegistry{backends: backends},
	)

	got, err := s.Get(testContext, "LEGACY")
	require.NoError(t, err)
	require.Equal(t, "recovered", got)
	meta, err := s.Meta(testContext, "LEGACY")
	require.NoError(t, err)
	require.Equal(t, BackendFile, meta.Backend)
}

// Exercises the real registry against a legacy index and an age-encrypted
// secrets.enc written before backend ownership and key source were persisted.
func TestStore_RepairsLegacyFileBackendWithPassphrase(t *testing.T) {
	t.Setenv(EnvPassphrase, "correct horse battery staple")
	dir := t.TempDir()

	fk, err := openPassphraseFileKey()
	require.NoError(t, err)
	require.NoError(t, newFileBackend(filepath.Join(dir, EncryptedFileName), fk).
		set(backendKey(testContext, "LEGACY"), "recovered"))
	indexPath := filepath.Join(dir, IndexFileName)
	raw := "contexts:\n  default:\n    LEGACY:\n      name: LEGACY\n      context: default\n      kind: secret\n"
	require.NoError(t, os.WriteFile(indexPath, []byte(raw), 0o600))

	s := newLocalStoreWithRegistry(BackendAuto, indexPath, newSystemBackendRegistry(dir))
	got, err := s.Get(testContext, "LEGACY")
	require.NoError(t, err)
	require.Equal(t, "recovered", got)

	stored, err := os.ReadFile(indexPath)
	require.NoError(t, err)
	require.Contains(t, string(stored), "backend: file")

	require.NoError(t, s.Set(testContext, "MY_VAR", "plain", KindEnv))
	got, err = s.Get(testContext, "MY_VAR")
	require.NoError(t, err)
	require.Equal(t, "plain", got)
}

// With no encrypted file and no reachable keyring, a legacy entry's value is
// nowhere; the user recovers by setting the secret again.
func TestStore_UnownedWithoutBackendStateIsRecoverable(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, IndexFileName)
	raw := "contexts:\n  default:\n    LEGACY:\n      name: LEGACY\n      context: default\n      kind: secret\n"
	require.NoError(t, os.WriteFile(indexPath, []byte(raw), 0o600))

	s := newLocalStoreWithRegistry(BackendAuto, indexPath, newSystemBackendRegistry(dir))
	if _, err := s.Get(testContext, "LEGACY"); err == nil ||
		!strings.Contains(err.Error(), "no proven owning backend") {
		t.Fatalf("expected actionable unowned error, got %v", err)
	}

	require.NoError(t, s.Set(testContext, "LEGACY", "fresh", KindSecret))
	got, err := s.Get(testContext, "LEGACY")
	require.NoError(t, err)
	require.Equal(t, "fresh", got)
}
