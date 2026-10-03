package envstore

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

const legacy = `keySource: passphrase
contexts:
  default:
    LOG_LEVEL:
      kind: env
      value: debug
      created: 2026-10-02T00:00:00Z
    TOKEN:
      kind: secret
      backend: file
      created: 2026-10-02T00:00:00Z
  other:
    EMPTY:
      kind: env
      value: ""
      created: 2026-10-02T00:00:00Z
`

func TestMigrationPreservesValuesAndSecretMetadata(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secrets.yaml"), []byte(legacy), 0o600))
	store := NewStore(dir)
	values, err := store.List("default")
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.Equal(t, "LOG_LEVEL", values[0].Name)
	require.Equal(t, "debug", values[0].Value)
	require.Equal(t, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), values[0].Created)
	empty, err := store.Get("other", "EMPTY")
	require.NoError(t, err)
	require.Empty(t, empty)
	// #nosec G304 -- path is a test fixture in t.TempDir.
	raw, err := os.ReadFile(filepath.Join(dir, "secrets.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "kind: env")
	require.Contains(t, string(raw), "keySource: passphrase")
	require.Contains(t, string(raw), "backend: file")
	require.Contains(t, string(raw), "schemaVersion: 2")
	before := string(raw)
	require.NoError(t, store.Set("default", "LOG_LEVEL", "trace"))
	// #nosec G304 -- path is a test fixture in t.TempDir.
	raw, err = os.ReadFile(filepath.Join(dir, "secrets.yaml"))
	require.NoError(t, err)
	require.Equal(t, before, string(raw))
}

func TestMigrationCrashAfterEnvCommitIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secrets.yaml"), []byte(legacy), 0o600))
	store := NewStore(dir).(*localStore)
	store.write = func(path string, raw []byte, mode os.FileMode) error {
		if filepath.Base(path) == "secrets.yaml" {
			return errors.New("simulated interruption")
		}
		return atomicWriteFile(path, raw, mode)
	}
	_, err := store.List("default")
	require.ErrorContains(t, err, "simulated interruption")
	// #nosec G304 -- path is a test fixture in t.TempDir.
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	require.NoError(t, err)
	var d data
	require.NoError(t, yaml.Unmarshal(raw, &d))
	entry := d.Contexts["default"]["LOG_LEVEL"]
	entry.Value = "newer"
	d.Contexts["default"]["LOG_LEVEL"] = entry
	raw, err = yaml.Marshal(d)
	require.NoError(t, err)
	require.NoError(t, atomicWriteFile(filepath.Join(dir, FileName), raw, 0o600))
	store.write = atomicWriteFile
	value, err := store.Get("default", "LOG_LEVEL")
	require.NoError(t, err)
	require.Equal(t, "newer", value)
	_, err = store.List("default")
	require.NoError(t, err)
	// #nosec G304 -- path is a test fixture in t.TempDir.
	raw, err = os.ReadFile(filepath.Join(dir, "secrets.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "kind: env")
}

func TestMigrationDoesNotRemoveLegacyValuesIfEnvWriteFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.yaml")
	require.NoError(t, os.WriteFile(path, []byte(legacy), 0o600))
	store := NewStore(dir).(*localStore)
	store.write = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	_, err := store.List("default")
	require.ErrorContains(t, err, "disk full")
	// #nosec G304 -- path is a test fixture in t.TempDir.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, legacy, string(raw))
}

func TestEnvironmentCRUDIgnoresSecretProtectionAndCorruption(t *testing.T) {
	for _, catalog := range []string{legacy, "contexts: [broken", `contexts:
  default:
    TOKEN:
      backend: unknown
      kind: secret
`} {
		t.Run(catalog[:8], func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("DEVSY_SECRETS_PASSPHRASE", "wrong")
			t.Setenv("DEVSY_SECRETS_PASSPHRASE_FILE", "/nonexistent")
			t.Setenv("DEVSY_SECRETS_BACKEND", "keyring")
			require.NoError(
				t,
				os.WriteFile(filepath.Join(dir, "secrets.yaml"), []byte(catalog), 0o600),
			)
			require.NoError(
				t,
				os.WriteFile(
					filepath.Join(dir, "secrets.enc"),
					[]byte("corrupt ciphertext"),
					0o600,
				),
			)
			store := NewStore(dir)
			require.NoError(t, store.Set("default", "PLAIN", "value"))
			value, err := store.Get("default", "PLAIN")
			require.NoError(t, err)
			require.Equal(t, "value", value)
			_, err = store.List("default")
			require.NoError(t, err)
			require.NoError(t, store.Delete("default", "PLAIN"))
			_, err = store.Get("default", "PLAIN")
			require.ErrorIs(t, err, ErrNotFound)
			// #nosec G304 -- path is a test fixture in t.TempDir.
			raw, err := os.ReadFile(filepath.Join(dir, "secrets.enc"))
			require.NoError(t, err)
			require.Equal(t, "corrupt ciphertext", string(raw))
		})
	}
}

func TestConcurrentWritesPreserveEveryValue(t *testing.T) {
	store := NewStore(t.TempDir())
	var wg sync.WaitGroup
	for _, name := range []string{"A", "B", "C", "D"} {
		wg.Add(1)
		go func(name string) { defer wg.Done(); require.NoError(t, store.Set("default", name, name)) }(
			name,
		)
	}
	wg.Wait()
	values, err := store.List("default")
	require.NoError(t, err)
	require.Len(t, values, 4)
}

func TestInvalidEnvFileFailsWithoutOverwriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	require.NoError(t, os.WriteFile(path, []byte("schemaVersion: 999"), 0o600))
	require.ErrorContains(t, NewStore(dir).Set("default", "A", "B"), "unsupported")
	// #nosec G304 -- path is a test fixture in t.TempDir.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "schemaVersion: 999", string(raw))
}

func TestUnknownSecretCatalogSchemaRemainsUntouched(t *testing.T) {
	for _, version := range []struct{ name, value string }{
		{"future", "3"},
		{"malformed string", "two"},
		{"fractional", "1.5"},
		{"negative", "-1"},
		{"null", "null"},
	} {
		t.Run(version.name, func(t *testing.T) {
			dir := t.TempDir()
			catalog := "schemaVersion: " + version.value + `
contexts:
  default:
    FUTURE_ENTRY:
      kind: env
      value: future-semantics
`
			path := filepath.Join(dir, "secrets.yaml")
			require.NoError(t, os.WriteFile(path, []byte(catalog), 0o600))
			store := NewStore(dir)
			require.NoError(t, store.Set("default", "NEW_ENV", "independent"))
			value, err := store.Get("default", "NEW_ENV")
			require.NoError(t, err)
			require.Equal(t, "independent", value)
			values, err := store.List("default")
			require.NoError(t, err)
			require.Len(t, values, 1)
			require.Equal(t, "NEW_ENV", values[0].Name)
			// #nosec G304 -- path is a test fixture in t.TempDir.
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, catalog, string(raw))
		})
	}
}
