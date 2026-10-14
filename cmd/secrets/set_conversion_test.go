package secrets

import (
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envstore"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/require"
)

const (
	conversionContext   = "default"
	conversionName      = "TOKEN"
	conversionPlaintext = "original plaintext"
	conversionOldValue  = "previous protected value"
	conversionNewValue  = "replacement protected value"
)

type conversionSecretStore struct {
	secrets.SecretStore
	value       string
	exists      bool
	readError   error
	restoreFail error
	writes      int
}

func (s *conversionSecretStore) Meta(contextName, name string) (secrets.SecretMeta, error) {
	if !s.exists {
		return secrets.SecretMeta{}, secrets.ErrSecretNotFound
	}
	return secrets.SecretMeta{Context: contextName, Name: name, Kind: secrets.KindSecret}, nil
}

func (s *conversionSecretStore) Get(_, _ string) (string, error) {
	return s.value, s.readError
}

func (s *conversionSecretStore) Set(_, _, value string) error {
	s.writes++
	if s.writes > 1 && s.restoreFail != nil {
		return s.restoreFail
	}
	s.value, s.exists = value, true
	return nil
}

func (s *conversionSecretStore) Delete(_, _ string) error {
	if s.restoreFail != nil {
		return s.restoreFail
	}
	s.value, s.exists = "", false
	return nil
}

type failedConversionEnvironment struct {
	envstore.EnvStore
	deleteError error
}

func (s failedConversionEnvironment) Delete(_, _ string) error {
	return s.deleteError
}

func conversionEnvironment(t *testing.T) envstore.EnvStore {
	t.Helper()
	envs := envstore.NewStore(t.TempDir())
	require.NoError(t, envs.Set(conversionContext, conversionName, conversionPlaintext))
	return envs
}

func TestConversionCleanupFailureRestoresSecretState(t *testing.T) {
	cleanupError := errors.New("injected environment replacement failure")
	for _, existed := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "new secret", true: "existing secret"}[existed],
			func(t *testing.T) {
				envs := conversionEnvironment(t)
				store := &conversionSecretStore{exists: existed}
				if existed {
					store.value = conversionOldValue
				}
				err := setSecretValue(store, failedConversionEnvironment{
					EnvStore: envs, deleteError: cleanupError,
				}, secretSetTarget{context: conversionContext, name: conversionName, value: conversionNewValue})
				require.ErrorIs(t, err, cleanupError)
				require.NotErrorIs(t, err, secrets.ErrStateIndeterminate)
				require.Equal(t, existed, store.exists)
				if existed {
					require.Equal(t, conversionOldValue, store.value)
				} else {
					require.Empty(t, store.value)
				}
				plaintext, err := envs.Get(conversionContext, conversionName)
				require.NoError(t, err)
				require.Equal(t, conversionPlaintext, plaintext)
			},
		)
	}
}

func TestConversionRollbackFailureIsActionableAndRedacted(t *testing.T) {
	store := &conversionSecretStore{restoreFail: errors.New(conversionNewValue)}
	err := setSecretValue(store, failedConversionEnvironment{
		EnvStore: conversionEnvironment(t), deleteError: errors.New("cleanup unavailable"),
	}, secretSetTarget{context: conversionContext, name: conversionName, value: conversionNewValue})
	require.ErrorIs(t, err, secrets.ErrStateIndeterminate)
	require.ErrorContains(t, err, "inspect both stores")
	require.ErrorContains(t, err, "env delete")
	require.NotContains(t, err.Error(), conversionNewValue)
	require.True(t, store.exists)
}

func TestConversionDoesNotOverwriteAnUnreadableExistingSecret(t *testing.T) {
	store := &conversionSecretStore{
		exists: true, value: conversionOldValue, readError: secrets.ErrUnlockRequired,
	}
	err := setSecretValue(
		store,
		conversionEnvironment(t),
		secretSetTarget{
			context: conversionContext,
			name:    conversionName,
			value:   conversionNewValue,
		},
	)
	require.ErrorIs(t, err, secrets.ErrUnlockRequired)
	require.Equal(t, conversionOldValue, store.value)
	require.Zero(t, store.writes)
}

func TestSecretSetWithoutEnvironmentValueDoesNotAttemptEnvironmentWrite(t *testing.T) {
	store := &conversionSecretStore{}
	envs := failedConversionEnvironment{
		EnvStore: envstore.NewStore(t.TempDir()), deleteError: errors.New("must not delete"),
	}
	require.NoError(
		t,
		setSecretValue(
			store,
			envs,
			secretSetTarget{
				context: conversionContext,
				name:    conversionName,
				value:   conversionNewValue,
			},
		),
	)
	require.Equal(t, conversionNewValue, store.value)
}

func TestConversionCleanupFailureRestoresEncryptedFileValue(t *testing.T) {
	for _, existed := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existed], func(t *testing.T) {
			config.ResetPathManager()
			t.Cleanup(config.ResetPathManager)
			t.Setenv(config.EnvHome, t.TempDir())
			t.Setenv(secrets.EnvBackend, string(secrets.BackendFile))
			store, err := secrets.NewSecretStoreForConfig(nil, secrets.StoreOptions{
				UnlockResolver: secrets.DefaultUnlockResolver{
					ExplicitPassphrase: "conversion rollback fixture passphrase",
				},
			})
			require.NoError(t, err)
			if existed {
				require.NoError(t, store.Set(conversionContext, conversionName, conversionOldValue))
			}
			envs, err := envstore.NewStoreForConfig(nil)
			require.NoError(t, err)
			require.NoError(t, envs.Set(conversionContext, conversionName, conversionPlaintext))
			err = setSecretValue(store, failedConversionEnvironment{
				EnvStore: envs, deleteError: errors.New("environment replacement failed"),
			}, secretSetTarget{context: conversionContext, name: conversionName, value: conversionNewValue})
			require.Error(t, err)
			value, err := store.Get(conversionContext, conversionName)
			if existed {
				require.NoError(t, err)
				require.Equal(t, conversionOldValue, value)
			} else {
				require.ErrorIs(t, err, secrets.ErrSecretNotFound)
			}
			plaintext, err := envs.Get(conversionContext, conversionName)
			require.NoError(t, err)
			require.Equal(t, conversionPlaintext, plaintext)
		})
	}
}
