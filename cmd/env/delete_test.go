package env

import (
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/require"
)

type deleteTestStore struct{ deleteErr error }

const (
	firstBinding = "FIRST"
	lastBinding  = "LAST"
)

func (s *deleteTestStore) Delete(string, string) error { return s.deleteErr }

func TestDeleteEnvironmentValueRestoresPersistedAttachmentAfterOrdinaryFailure(t *testing.T) {
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
	t.Setenv(config.EnvHome, t.TempDir())
	const name = "LOG_LEVEL"
	cfg := &config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{
			config.DefaultContext: {EnvVars: []string{firstBinding, name, lastBinding}},
		},
	}
	require.NoError(t, config.SaveConfig(cfg))
	deleteErr := errors.New("delete failed")
	err := deleteEnvironmentValue(deleteEnvRequest{
		config: cfg, store: &deleteTestStore{deleteErr: deleteErr}, context: config.DefaultContext,
		name: name, save: config.SaveConfig,
	})
	require.ErrorIs(t, err, deleteErr)
	reloaded, loadErr := config.LoadConfig("", "")
	require.NoError(t, loadErr)
	require.Equal(t, []string{firstBinding, name, lastBinding}, reloaded.Current().EnvVars)
}
