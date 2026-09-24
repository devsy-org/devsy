package secrets

import (
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	devsysecrets "github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/require"
)

type deleteTestStore struct {
	value     string
	deleteErr error
	getErr    error
}

const (
	deleteSecretName = "DELETE_TOKEN"
	deleteTestValue  = "delete-value"
)

func (s *deleteTestStore) Set(string, string, string, devsysecrets.Kind) error { return nil }
func (s *deleteTestStore) Get(string, string) (string, error) {
	if s.getErr != nil {
		return "", s.getErr
	}
	return s.value, nil
}

func (s *deleteTestStore) Meta(string, string) (devsysecrets.SecretMeta, error) {
	return devsysecrets.SecretMeta{}, nil
}

func (s *deleteTestStore) List(string) ([]devsysecrets.SecretMeta, error) { return nil, nil }
func (s *deleteTestStore) Delete(string, string) error                    { return s.deleteErr }

func TestDeleteSecretValueRestoresAttachedBindingWhenDeleteFails(t *testing.T) {
	cfg := deleteTestConfig([]string{"DELETE_FIRST", deleteSecretName, "DELETE_LAST"})
	deleteErr := errors.New("delete failed")
	store := &deleteTestStore{value: deleteTestValue, deleteErr: deleteErr}

	err := deleteSecretValue(deleteSecretRequest{
		config:  cfg,
		store:   store,
		context: config.DefaultContext,
		name:    deleteSecretName,
		save: func(*config.Config) error {
			return nil
		},
	})

	require.ErrorIs(t, err, deleteErr)
	require.Equal(
		t,
		[]string{"DELETE_FIRST", deleteSecretName, "DELETE_LAST"},
		cfg.Current().Secrets,
	)
}

func TestDeleteSecretValueLeavesBindingRemovedWhenValueUnavailable(t *testing.T) {
	cfg := deleteTestConfig([]string{deleteSecretName})
	deleteErr := errors.New("delete failed")
	store := &deleteTestStore{deleteErr: deleteErr, getErr: devsysecrets.ErrSecretNotFound}

	err := deleteSecretValue(deleteSecretRequest{
		config:  cfg,
		store:   store,
		context: config.DefaultContext,
		name:    deleteSecretName,
		save: func(*config.Config) error {
			return nil
		},
	})

	require.ErrorIs(t, err, deleteErr)
	require.Empty(t, cfg.Current().Secrets)
}

func TestDeleteSecretValueJoinsRollbackFailure(t *testing.T) {
	cfg := deleteTestConfig([]string{deleteSecretName})
	deleteErr := errors.New("delete failed")
	rollbackErr := errors.New("rollback failed")
	store := &deleteTestStore{value: deleteTestValue, deleteErr: deleteErr}
	saves := 0

	err := deleteSecretValue(deleteSecretRequest{
		config:  cfg,
		store:   store,
		context: config.DefaultContext,
		name:    deleteSecretName,
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
	require.Equal(t, []string{deleteSecretName}, cfg.Current().Secrets)
}

func TestDeleteSecretValueDoesNotSaveForUnattachedValue(t *testing.T) {
	cfg := deleteTestConfig(nil)
	saves := 0
	store := &deleteTestStore{value: deleteTestValue}

	require.NoError(t, deleteSecretValue(deleteSecretRequest{
		config:  cfg,
		store:   store,
		context: config.DefaultContext,
		name:    deleteSecretName,
		save: func(*config.Config) error {
			saves++
			return nil
		},
	}))
	require.Zero(t, saves)
}

func deleteTestConfig(secretsList []string) *config.Config {
	return &config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{
			config.DefaultContext: {Secrets: secretsList},
		},
	}
}
