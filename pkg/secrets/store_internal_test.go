package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/devsy-org/devsy/pkg/config"
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

func TestStore_ReconcileFlagsOrphans(t *testing.T) {
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

// An index entry with an unset Kind (e.g. written by an older layout) must be
// read as a secret, never silently downgraded to a plaintext env var.
func TestLoadIndex_UnsetKindNormalizesToSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), IndexFileName)
	raw := "contexts:\n  default:\n    LEGACY:\n      name: LEGACY\n      context: default\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	idx, err := loadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := idx.get("default", "LEGACY")
	if !ok {
		t.Fatal("expected entry to load")
	}
	if !meta.Sensitive() {
		t.Fatalf("unset Kind must normalize to secret, got %q", meta.Kind)
	}
}

// A blank-kind entry carrying an inline value (e.g. hand-edited) must be
// normalized to a secret with the inline plaintext cleared, upholding the
// invariant that sensitive values never persist inline.
func TestLoadIndex_UnsetKindClearsInlineValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), IndexFileName)
	raw := "contexts:\n  default:\n    LEGACY:\n      name: LEGACY\n" +
		"      context: default\n      value: leaked\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	idx, err := loadIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	meta, _ := idx.get("default", "LEGACY")
	if !meta.Sensitive() {
		t.Fatalf("unset Kind must normalize to secret, got %q", meta.Kind)
	}
	if meta.Value != "" {
		t.Fatalf("inline value must be cleared on normalize, got %q", meta.Value)
	}
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
