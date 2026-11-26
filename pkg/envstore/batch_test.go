package envstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBatchDeleteFailurePreservesAllOriginalValues(t *testing.T) {
	store := NewStore(t.TempDir()).(*localStore)
	require.NoError(t, store.Set("staging", "A", "first"))
	require.NoError(t, store.Set("staging", "B", "second"))
	before, err := store.List("staging")
	require.NoError(t, err)
	store.write = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	require.ErrorContains(t, store.DeleteValues("staging", []string{"A", "B"}), "disk full")
	after, err := store.List("staging")
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestBatchRestorePreservesTimestampsAndOtherContexts(t *testing.T) {
	store := NewStore(t.TempDir()).(*localStore)
	created := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	lastUsed := created.Add(time.Hour)
	values := []EnvValue{
		{Name: "A", Value: "first", Created: created, LastUsed: lastUsed},
		{Name: "B", Value: "second", Created: created},
	}
	require.NoError(t, store.RestoreValues("staging", values))
	require.NoError(t, store.Set("other", "A", "other-context"))
	before, err := store.List("staging")
	require.NoError(t, err)
	require.NoError(t, store.DeleteValues("staging", []string{"A", "B"}))
	remaining, err := store.List("staging")
	require.NoError(t, err)
	require.Empty(t, remaining)
	require.NoError(t, store.RestoreValues("staging", before))
	after, err := store.List("staging")
	require.NoError(t, err)
	require.Equal(t, before, after)
	other, err := store.Meta("other", "A")
	require.NoError(t, err)
	require.Equal(t, "other-context", other.Value)
	info, err := os.Stat(filepath.Join(store.dir, FileName))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
