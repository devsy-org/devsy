package secrets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// legacyIndexData models the catalog schema used by older installed CLIs.
type legacyIndexData struct {
	SchemaVersion int                              `json:"schemaVersion,omitempty"`
	Contexts      map[string]map[string]SecretMeta `json:"contexts"`
	KeySource     string                           `json:"keySource,omitempty"`
}

func TestIndexSaveKeepsLegacyKeySourceReadable(t *testing.T) {
	for _, source := range []string{
		string(keySourceAutoFile),
		string(keySourcePassphrase),
	} {
		t.Run(source, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), IndexFileName)
			idx, err := loadIndex(path)
			require.NoError(t, err)
			idx.data.KeySource = source
			require.NoError(t, idx.save())

			raw, err := os.ReadFile(path) // #nosec G304 -- test fixture in a temp directory.
			require.NoError(t, err)
			var legacy legacyIndexData
			require.NoError(t, yaml.Unmarshal(raw, &legacy))
			require.Equal(t, source, legacy.KeySource)
		})
	}
}

func TestIndexReadNormalizesCatalogResavedByLegacyWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), IndexFileName)
	idx, err := loadIndex(path)
	require.NoError(t, err)
	idx.data.KeySource = string(keySourcePassphrase)
	idx.put(SecretMeta{
		Context: testContext,
		Name:    "TOKEN",
		Kind:    KindSecret,
		Backend: BackendFile,
	})
	require.NoError(t, idx.save())

	raw, err := os.ReadFile(path) // #nosec G304 -- test fixture in a temp directory.
	require.NoError(t, err)
	var legacy legacyIndexData
	require.NoError(t, yaml.Unmarshal(raw, &legacy))
	// An old writer ignores the nested fileStore field, then serializes its
	// known schema and the retained top-level keySource.
	legacy.SchemaVersion = 0
	raw, err = yaml.Marshal(legacy)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600)) // #nosec G304 -- temp test path.

	loaded, err := loadIndex(path)
	require.NoError(t, err)
	require.Equal(t, string(keySourcePassphrase), loaded.data.KeySource)
	require.Empty(t, loaded.data.FileStore.KeySource)
	require.NoError(t, loaded.save())
	loaded, err = loadIndex(path)
	require.NoError(t, err)
	require.Equal(t, string(keySourcePassphrase), loaded.data.KeySource)
	require.Equal(t, string(keySourcePassphrase), loaded.data.FileStore.KeySource)
	meta, exists := loaded.get(testContext, "TOKEN")
	require.True(t, exists)
	require.Equal(t, BackendFile, meta.Backend)
}

func TestIndexConflictingLegacyAndNestedKeySourcesAreCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), IndexFileName)
	raw := []byte(
		"schemaVersion: 2\nkeySource: file\nfileStore:\n  keySource: passphrase\ncontexts: {}\n",
	)
	require.NoError(t, os.WriteFile(path, raw, 0o600)) // #nosec G304 -- temp test path.

	_, err := loadIndex(path)
	require.ErrorIs(t, err, ErrStoreCorrupt)
}

func TestProtectionResetClearsBothKeySourceFields(t *testing.T) {
	p, _ := newLegacyResetFixture(t, KindSecret, false)
	p.readRemembered = func() (string, error) { return "", nil }
	_, err := p.ResetFileStore()
	require.NoError(t, err)

	raw, err := os.ReadFile(filepath.Join(p.dir, IndexFileName)) // #nosec G304 -- temp test path.
	require.NoError(t, err)
	var catalog indexData

	require.NoError(t, yaml.Unmarshal(raw, &catalog))
	require.Empty(t, catalog.KeySource)
	require.Empty(t, catalog.FileStore.KeySource)
}
