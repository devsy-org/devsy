package env

import (
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/require"
)

type deleteTestStore struct {
	value     string
	deleteErr error
	getErr    error
}

const (
	deleteEnvName   = "FOO"
	deleteTestValue = "delete-value"
)

func (s *deleteTestStore) Set(string, string, string, secrets.Kind) error { return nil }
func (s *deleteTestStore) Get(string, string) (string, error) {
	if s.getErr != nil {
		return "", s.getErr
	}
	return s.value, nil
}

func (s *deleteTestStore) Meta(string, string) (secrets.SecretMeta, error) {
	return secrets.SecretMeta{}, nil
}

func (s *deleteTestStore) List(string) ([]secrets.SecretMeta, error) { return nil, nil }
func (s *deleteTestStore) Delete(string, string) error               { return s.deleteErr }

func TestDeleteEnvironmentValueRestoresAttachedBindingWhenDeleteFails(t *testing.T) {
	cfg := deleteTestConfig([]string{"DELETE_FIRST", deleteEnvName, "DELETE_LAST"})
	deleteErr := errors.New("delete failed")
	store := &deleteTestStore{value: deleteTestValue, deleteErr: deleteErr}

	err := deleteEnvironmentValue(deleteEnvRequest{
		config:  cfg,
		store:   store,
		context: config.DefaultContext,
		name:    deleteEnvName,
		save: func(*config.Config) error {
			return nil
		},
	})

	require.ErrorIs(t, err, deleteErr)
	require.Equal(t, []string{"DELETE_FIRST", deleteEnvName, "DELETE_LAST"}, cfg.Current().EnvVars)
}

func TestDeleteEnvironmentValueLeavesBindingRemovedWhenValueUnavailable(t *testing.T) {
	cfg := deleteTestConfig([]string{deleteEnvName})
	deleteErr := errors.New("delete failed")
	store := &deleteTestStore{deleteErr: deleteErr, getErr: secrets.ErrSecretNotFound}

	err := deleteEnvironmentValue(deleteEnvRequest{
		config:  cfg,
		store:   store,
		context: config.DefaultContext,
		name:    deleteEnvName,
		save: func(*config.Config) error {
			return nil
		},
	})

	require.ErrorIs(t, err, deleteErr)
	require.Empty(t, cfg.Current().EnvVars)
}

func TestDeleteEnvironmentValueJoinsRollbackFailure(t *testing.T) {
	cfg := deleteTestConfig([]string{deleteEnvName})
	deleteErr := errors.New("delete failed")
	rollbackErr := errors.New("rollback failed")
	store := &deleteTestStore{value: deleteTestValue, deleteErr: deleteErr}
	saves := 0

	err := deleteEnvironmentValue(deleteEnvRequest{
		config:  cfg,
		store:   store,
		context: config.DefaultContext,
		name:    deleteEnvName,
		save: func(*config.Config) error {
			saves++
			if saves == 2 {
				return rollbackErr
			}
			return nil
		},
	})

	require.ErrorIs(t, err, deleteErr)
	require.ErrorIs(t, err, rollbackErr)
	require.Equal(t, []string{deleteEnvName}, cfg.Current().EnvVars)
}

func TestDeleteEnvironmentValueDoesNotSaveForUnattachedValue(t *testing.T) {
	cfg := deleteTestConfig(nil)
	saves := 0
	store := &deleteTestStore{value: deleteTestValue}

	require.NoError(t, deleteEnvironmentValue(deleteEnvRequest{
		config:  cfg,
		store:   store,
		context: config.DefaultContext,
		name:    deleteEnvName,
		save: func(*config.Config) error {
			saves++
			return nil
		},
	}))
	require.Zero(t, saves)
}

func deleteTestConfig(envVars []string) *config.Config {
	return &config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{
			config.DefaultContext: {EnvVars: envVars},
		},
	}
}
