package secrets

import (
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	devsysecrets "github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/require"
)

type listTestStore struct {
	metas []devsysecrets.SecretMeta
}

func (s *listTestStore) Set(string, string, string, devsysecrets.Kind) error { return nil }
func (s *listTestStore) Get(string, string) (string, error)                  { return "", nil }
func (s *listTestStore) Meta(string, string) (devsysecrets.SecretMeta, error) {
	return devsysecrets.SecretMeta{}, nil
}
func (s *listTestStore) List(string) ([]devsysecrets.SecretMeta, error) { return s.metas, nil }
func (s *listTestStore) Delete(string, string) error                    { return nil }

func TestListEntriesMarksAttachedSecrets(t *testing.T) {
	created := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	store := &listTestStore{metas: []devsysecrets.SecretMeta{
		{
			Name:    "ATTACHED",
			Context: config.DefaultContext,
			Kind:    devsysecrets.KindSecret,
			Created: created,
		},
		{Name: "DETACHED", Context: config.DefaultContext, Kind: devsysecrets.KindSecret},
		{Name: "ENV_VAR", Context: config.DefaultContext, Kind: devsysecrets.KindEnv},
	}}
	cfg := deleteTestConfig([]string{"ATTACHED", "sops:project/API_TOKEN"})

	entries, err := listEntries(cfg, store, config.DefaultContext)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.Equal(t, "ATTACHED", entries[0].Name)
	require.True(t, entries[0].Attached)
	require.Equal(t, created.Format(time.RFC3339), entries[0].Created)
	require.Equal(t, "DETACHED", entries[1].Name)
	require.False(t, entries[1].Attached)
}

func TestListEntriesDetachedWhenContextHasNoBindings(t *testing.T) {
	store := &listTestStore{metas: []devsysecrets.SecretMeta{
		{Name: "TOKEN", Context: config.DefaultContext, Kind: devsysecrets.KindSecret},
	}}
	cfg := deleteTestConfig(nil)

	entries, err := listEntries(cfg, store, config.DefaultContext)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.False(t, entries[0].Attached)
}
