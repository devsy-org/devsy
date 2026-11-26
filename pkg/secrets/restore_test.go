package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

const testRestoredValue = "restore-private-value"

type restoreRecordingRegistry struct {
	mapBackendRegistry
	intents []BackendOpenIntent
}

func (r *restoreRecordingRegistry) Open(
	kind Backend,
	idx *index,
	intent BackendOpenIntent,
) (backend, error) {
	r.intents = append(r.intents, intent)
	return r.mapBackendRegistry.Open(kind, idx, intent)
}

func newRestoreTestStore(t *testing.T) (*secretOnlyStore, *restoreRecordingRegistry) {
	t.Helper()
	registry := &restoreRecordingRegistry{
		mapBackendRegistry: mapBackendRegistry{backends: map[Backend]*mapBackend{
			BackendKeyring: newMapBackend(), BackendFile: newMapBackend(),
		}},
	}
	store := newLocalStoreWithRegistry(
		BackendFile,
		filepath.Join(t.TempDir(), IndexFileName),
		registry,
	)
	return &secretOnlyStore{store}, registry
}

func capturedRestoreMeta(kind Backend) SecretMeta {
	return SecretMeta{
		Name:    "RESTORED_TOKEN",
		Context: testContext,
		Kind:    KindSecret,
		Backend: kind,
		Created: time.Date(
			2025,
			time.January,
			2,
			3,
			4,
			5,
			0,
			time.UTC,
		),
		LastUsed: time.Date(2025, time.February, 2, 3, 4, 5, 0, time.UTC),
	}
}

func TestSecretRestorePreservesOriginalBackendAndMetadata(t *testing.T) {
	for _, kind := range []Backend{BackendKeyring, BackendFile} {
		t.Run(string(kind), func(t *testing.T) {
			s, registry := newRestoreTestStore(t)
			meta := capturedRestoreMeta(kind)
			if kind == BackendFile {
				idx, err := loadIndex(s.indexPath)
				require.NoError(t, err)
				idx.data.KeySource = string(keySourcePassphrase)
				require.NoError(t, idx.save())
			}
			require.NoError(t, s.Restore(meta, testRestoredValue))
			require.NoError(t, s.Delete(meta.Context, meta.Name))
			registry.intents = nil
			if kind == BackendFile {
				s.preference = BackendKeyring
			}
			require.NoError(t, s.Restore(meta, testRestoredValue))
			restored, err := s.Meta(meta.Context, meta.Name)
			require.NoError(t, err)
			require.Equal(t, meta, restored)
			if kind == BackendFile {
				idx, err := loadIndex(s.indexPath)
				require.NoError(t, err)
				require.Equal(t, string(keySourcePassphrase), idx.data.KeySource)
			}
			require.Equal(t, []BackendOpenIntent{BackendOpenExisting}, registry.intents)
			require.Equal(
				t,
				testRestoredValue,
				registry.backends[kind].values[backendKey(meta.Context, meta.Name)],
			)
			other := BackendFile
			if kind == BackendFile {
				other = BackendKeyring
			}
			require.Empty(t, registry.backends[other].values)
			raw, err := os.ReadFile(
				s.indexPath,
			) // #nosec G304 -- temporary test catalog in t.TempDir.
			require.NoError(t, err)
			require.NotContains(t, string(raw), testRestoredValue)
		})
	}
}

func TestSecretRestoreRejectsConflictingOwnership(t *testing.T) {
	s, registry := newRestoreTestStore(t)
	current := capturedRestoreMeta(BackendFile)
	require.NoError(t, s.Restore(current, testRestoredValue))
	registry.intents = nil
	require.Error(t, s.Restore(capturedRestoreMeta(BackendKeyring), "replacement"))
	require.Empty(t, registry.intents)
	require.Equal(
		t,
		testRestoredValue,
		registry.backends[BackendFile].values[backendKey(current.Context, current.Name)],
	)
}

func TestSecretRestoreRejectsInvalidCapturedMetadata(t *testing.T) {
	valid := capturedRestoreMeta(BackendKeyring)
	for _, mutate := range []func(*SecretMeta){
		func(m *SecretMeta) { m.Name = "invalid/name" }, func(m *SecretMeta) { m.Context = "" },
		func(m *SecretMeta) { m.Context = "unsafe\ncontext" }, func(m *SecretMeta) { m.Kind = KindEnv },
		func(m *SecretMeta) { m.Backend = "" }, func(m *SecretMeta) { m.Backend = BackendAuto },
		func(m *SecretMeta) { m.Value = testRestoredValue },
	} {
		s, registry := newRestoreTestStore(t)
		meta := valid
		mutate(&meta)
		err := s.Restore(meta, testRestoredValue)
		require.Error(t, err)
		require.NotContains(t, err.Error(), testRestoredValue)
		require.Empty(t, registry.intents)
	}
}

func TestSecretRestorePropagatesSafeCompensationFailures(t *testing.T) {
	for _, phase := range []string{"backend", "catalog"} {
		t.Run(phase, func(t *testing.T) {
			s, registry := newRestoreTestStore(t)
			cause := errors.New("failure containing " + testRestoredValue)
			if phase == "backend" {
				registry.backends[BackendKeyring].setError = cause
			} else {
				s.saveIndex = func(*index) error { return cause }
			}
			meta := capturedRestoreMeta(BackendKeyring)
			err := s.Restore(meta, testRestoredValue)
			require.ErrorIs(t, err, cause)
			require.NotContains(t, err.Error(), testRestoredValue)
			_, err = s.Meta(meta.Context, meta.Name)
			require.ErrorIs(t, err, ErrSecretNotFound)
		})
	}
}

func TestSecretRestorePreservesLegacyEmptyKind(t *testing.T) {
	s, _ := newRestoreTestStore(t)
	captured := capturedRestoreMeta(BackendKeyring)
	captured.Kind = ""
	require.NoError(t, s.Restore(captured, testRestoredValue))
	raw, err := os.ReadFile(s.indexPath) // #nosec G304 -- temporary test catalog in t.TempDir.
	require.NoError(t, err)
	var data indexData
	require.NoError(t, yaml.Unmarshal(raw, &data))
	require.Equal(t, captured, data.Contexts[captured.Context][captured.Name])
}
