package context

import (
	"errors"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envstore"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/require"
)

const (
	cleanupFirstSecret    = "FIRST"
	cleanupSecondSecret   = "SECOND"
	cleanupOtherContext   = "other"
	cleanupKeepName       = "KEEP"
	cleanupKeepValue      = "keep"
	cleanupOtherKey       = "other/KEEP"
	cleanupFirstKey       = "staging/FIRST"
	cleanupEncryptedName  = "TOKEN"
	cleanupDetachedSecret = "DETACHED_SECRET"
	cleanupSecret         = "private-secret"
	cleanupEnv            = "private-env"
)

const (
	cleanupContext  = "staging"
	cleanupAttached = "ATTACHED"
)

type cleanupSecretValue struct {
	meta  secrets.SecretMeta
	value string
}

type cleanupSecretStore struct {
	values      map[string]cleanupSecretValue
	deleted     []string
	restored    []string
	failDelete  string
	failRestore string
	repairOnGet bool
}

func (s *cleanupSecretStore) Meta(contextName, name string) (secrets.SecretMeta, error) {
	entry, ok := s.values[contextName+"/"+name]
	if !ok {
		return secrets.SecretMeta{}, secrets.ErrSecretNotFound
	}
	return entry.meta, nil
}

func (s *cleanupSecretStore) Get(contextName, name string) (string, error) {
	entry, ok := s.values[contextName+"/"+name]
	if !ok {
		return "", secrets.ErrSecretNotFound
	}
	if s.repairOnGet && entry.meta.Backend == "" {
		entry.meta.Backend = secrets.BackendFile
		entry.meta.LastUsed = entry.meta.Created.Add(2 * time.Hour)
		s.values[contextName+"/"+name] = entry
	}
	return entry.value, nil
}

func (s *cleanupSecretStore) Delete(contextName, name string) error {
	s.deleted = append(s.deleted, name)
	delete(s.values, contextName+"/"+name)
	if name == s.failDelete {
		return errors.New("secret backend error contains private-secret")
	}
	return nil
}

func (s *cleanupSecretStore) Restore(meta secrets.SecretMeta, value string) error {
	s.restored = append(s.restored, meta.Name)
	if meta.Name == s.failRestore {
		return errors.New("restore error contains private-secret")
	}
	s.values[meta.Context+"/"+meta.Name] = cleanupSecretValue{meta: meta, value: value}
	return nil
}

type cleanupEnvStore struct {
	envstore.BatchStore
	deleteErr    error
	restoreErr   error
	deleteCalls  int
	restoreCalls int
	beforeDelete func()
}

func (s *cleanupEnvStore) DeleteValues(contextName string, names []string) error {
	s.deleteCalls++
	if s.beforeDelete != nil {
		s.beforeDelete()
	}
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.BatchStore.DeleteValues(contextName, names)
}

func (s *cleanupEnvStore) RestoreValues(contextName string, values []envstore.EnvValue) error {
	s.restoreCalls++
	if s.restoreErr != nil {
		return s.restoreErr
	}
	return s.BatchStore.RestoreValues(contextName, values)
}

func cleanupFixture(t *testing.T) (contextDeleteRequest, *cleanupEnvStore, *cleanupSecretStore) {
	t.Helper()
	cfg := &config.Config{
		DefaultContext:  cleanupContext,
		OriginalContext: cleanupContext,
		Contexts: map[string]*config.ContextConfig{
			"default": {},
			cleanupContext: {
				Secrets: []string{cleanupFirstSecret, cleanupSecondSecret},
				EnvVars: []string{cleanupAttached},
			},
			cleanupOtherContext: {},
		},
	}
	envs := &cleanupEnvStore{BatchStore: envstore.NewStore(t.TempDir()).(envstore.BatchStore)}
	stamp := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	require.NoError(t, envs.RestoreValues(cleanupContext, []envstore.EnvValue{
		{Name: cleanupAttached, Value: cleanupEnv, Created: stamp, LastUsed: stamp.Add(time.Hour)},
		{Name: "DETACHED", Value: "other-env", Created: stamp},
	}))
	require.NoError(t, envs.Set(cleanupOtherContext, cleanupKeepName, cleanupKeepValue))
	secretStore := &cleanupSecretStore{values: map[string]cleanupSecretValue{}}
	for _, name := range []string{cleanupFirstSecret, cleanupSecondSecret, cleanupDetachedSecret} {
		meta := secrets.SecretMeta{
			Name:    name,
			Context: cleanupContext,
			Kind:    secrets.KindSecret,
			Backend: secrets.BackendFile,
			Created: stamp,
		}
		secretStore.values["staging/"+name] = cleanupSecretValue{meta: meta, value: cleanupSecret}
	}
	envs.restoreCalls = 0
	secretStore.values[cleanupOtherKey] = cleanupSecretValue{
		meta:  secrets.SecretMeta{Name: cleanupKeepName, Context: cleanupOtherContext},
		value: cleanupKeepValue,
	}
	return contextDeleteRequest{
		config:  cfg,
		context: cleanupContext,
		envs:    envs,
		secrets: secretStore,
		save:    func(*config.Config) error { return nil },
	}, envs, secretStore
}

func assertCleanupValuesPreserved(
	t *testing.T,
	request contextDeleteRequest,
	original []envstore.EnvValue,
) {
	t.Helper()
	envs := request.envs.(*cleanupEnvStore)
	secretStore := request.secrets.(*cleanupSecretStore)
	after, err := envs.List(cleanupContext)
	require.NoError(t, err)
	require.Equal(t, original, after)
	for _, name := range []string{cleanupFirstSecret, cleanupSecondSecret, cleanupDetachedSecret} {
		require.Equal(t, cleanupSecret, secretStore.values["staging/"+name].value)
	}
	require.Equal(t, cleanupKeepValue, secretStore.values[cleanupOtherKey].value)
	other, err := envs.Meta(cleanupOtherContext, cleanupKeepName)
	require.NoError(t, err)
	require.Equal(t, cleanupKeepValue, other.Value)
	require.Contains(t, request.config.Contexts, cleanupContext)
	require.Equal(t, cleanupContext, request.config.DefaultContext)
}

func TestContextCleanupRestoresSecretsAfterEnvironmentDeleteFailure(t *testing.T) {
	request, envs, secretStore := cleanupFixture(t)
	original, err := envs.List(cleanupContext)
	require.NoError(t, err)
	envs.deleteErr = errors.New("write error contains private-env")
	envs.restoreErr = errors.New("environment write still blocked")
	err = deleteContextValues(request)
	require.Error(t, err)
	require.NotContains(t, err.Error(), cleanupEnv)
	require.Equal(t, []string{cleanupFirstSecret, cleanupSecondSecret}, secretStore.deleted)
	require.Equal(t, 1, envs.deleteCalls, "failure must occur after actual secret deletion")
	require.Equal(t, []string{cleanupSecondSecret, cleanupFirstSecret}, secretStore.restored)
	require.Zero(
		t,
		envs.restoreCalls,
		"atomic env deletion failure preserves originals without another write",
	)
	require.NotErrorIs(t, err, ErrContextCleanupIndeterminate)
	assertCleanupValuesPreserved(t, request, original)
}

func TestContextCleanupRestoresPartialSecretDeletion(t *testing.T) {
	request, envs, secretStore := cleanupFixture(t)
	original, err := envs.List(cleanupContext)
	require.NoError(t, err)
	secretStore.failDelete = cleanupSecondSecret
	err = deleteContextValues(request)
	require.Error(t, err)
	require.NotContains(t, err.Error(), cleanupSecret)
	require.Zero(t, envs.deleteCalls)
	require.Equal(t, []string{cleanupSecondSecret, cleanupFirstSecret}, secretStore.restored)
	assertCleanupValuesPreserved(t, request, original)
}

func TestContextCleanupRestoresBothDomainsAfterConfigSaveFailure(t *testing.T) {
	request, envs, _ := cleanupFixture(t)
	original, err := envs.List(cleanupContext)
	require.NoError(t, err)
	request.save = func(candidate *config.Config) error {
		require.NotContains(t, candidate.Contexts, cleanupContext)
		require.Equal(t, config.DefaultContext, candidate.DefaultContext)
		values, err := envs.List(cleanupContext)
		require.NoError(t, err)
		require.Empty(t, values)
		return errors.New("config write failed")
	}
	err = deleteContextValues(request)
	require.ErrorContains(t, err, "config persistence")
	assertCleanupValuesPreserved(t, request, original)
}

func TestContextCleanupReportsRedactedIndeterminateRollback(t *testing.T) {
	request, envs, secretStore := cleanupFixture(t)
	secretStore.failDelete = cleanupSecondSecret
	secretStore.failRestore = cleanupSecondSecret
	err := deleteContextValues(request)
	require.ErrorIs(t, err, ErrContextCleanupIndeterminate)
	require.NotErrorIs(t, err, config.ErrContextDeletionPending)
	require.ErrorContains(t, err, "rollback is incomplete")
	require.NotContains(t, err.Error(), cleanupSecret)
	require.Equal(
		t,
		cleanupSecret,
		secretStore.values[cleanupFirstKey].value,
		"rollback continues after a failure",
	)
	require.Contains(t, request.config.Contexts, cleanupContext)
	value, envErr := envs.Meta(cleanupContext, cleanupAttached)
	require.NoError(t, envErr)
	require.Equal(t, cleanupEnv, value.Value)
}

func TestContextCleanupReportsEnvironmentRollbackFailureWithoutValues(t *testing.T) {
	request, envs, secretStore := cleanupFixture(t)
	request.save = func(*config.Config) error { return errors.New("config write failed") }
	envs.restoreErr = errors.New("environment restore failed: private-env")
	err := deleteContextValues(request)
	require.ErrorIs(t, err, ErrContextCleanupIndeterminate)
	require.NotContains(t, err.Error(), cleanupEnv)
	require.Equal(t, []string{cleanupSecondSecret, cleanupFirstSecret}, secretStore.restored)
	require.Contains(t, request.config.Contexts, cleanupContext)
}

func TestContextCleanupSuccessLeavesOtherContextsAndDetachedSecrets(t *testing.T) {
	request, envs, secretStore := cleanupFixture(t)
	saved := false
	request.save = func(candidate *config.Config) error {
		saved = true
		require.NotContains(t, candidate.Contexts, cleanupContext)
		return nil
	}
	require.NoError(t, deleteContextValues(request))
	require.True(t, saved)
	values, err := envs.List(cleanupContext)
	require.NoError(t, err)
	require.Empty(t, values)
	require.NotContains(t, secretStore.values, cleanupFirstKey)
	require.NotContains(t, secretStore.values, "staging/SECOND")
	require.Contains(t, secretStore.values, "staging/DETACHED_SECRET")
	require.Equal(t, cleanupKeepValue, secretStore.values[cleanupOtherKey].value)
}

func TestContextCleanupConfigSaveFailureKeepsPersistedRegistration(t *testing.T) {
	setupDeleteContextTest(t)
	request, envs, secretStore := cleanupFixture(t)
	require.NoError(t, config.SaveConfig(request.config))
	original, err := envs.List(cleanupContext)
	require.NoError(t, err)
	request.save = func(*config.Config) error { return errors.New("injected config save failure") }
	err = deleteContextValues(request)
	require.ErrorContains(t, err, "config persistence")
	assertCleanupValuesPreserved(t, request, original)
	persisted, err := config.LoadConfig("", "")
	require.NoError(t, err)
	require.Contains(t, persisted.Contexts, cleanupContext)
	require.Equal(
		t,
		[]string{cleanupFirstSecret, cleanupSecondSecret},
		persisted.Contexts[cleanupContext].Secrets,
	)
	require.Equal(t, cleanupSecret, secretStore.values[cleanupFirstKey].value)
}

func TestContextCleanupRestoresRealEncryptedSecretAfterEnvironmentFailure(t *testing.T) {
	cfg, _ := setupDeleteContextTest(t)
	cfg.Contexts[cleanupContext].Secrets = []string{cleanupEncryptedName}
	require.NoError(t, config.SaveConfig(cfg))
	t.Setenv(secrets.EnvBackend, "file")
	t.Setenv(secrets.EnvPassphrase, "context-cleanup-long-test-passphrase")
	t.Setenv(secrets.EnvPassphraseFile, "")
	store, err := secrets.NewSecretStoreForConfig(cfg)
	require.NoError(t, err)
	require.NoError(t, store.Set(cleanupContext, cleanupEncryptedName, cleanupSecret))
	original, err := store.Meta(cleanupContext, cleanupEncryptedName)
	require.NoError(t, err)
	request, err := newContextDeleteRequest(cfg, cleanupContext)
	require.NoError(t, err)
	envs := &cleanupEnvStore{
		BatchStore: request.envs,
		deleteErr:  errors.New("injected environment write failure"),
	}
	require.NoError(t, envs.Set(cleanupContext, cleanupAttached, cleanupEnv))
	envs.beforeDelete = func() {
		_, err := store.Meta(cleanupContext, cleanupEncryptedName)
		require.ErrorIs(
			t,
			err,
			secrets.ErrSecretNotFound,
			"the failure must occur after real secret deletion",
		)
	}
	request.envs = envs
	err = deleteContextValues(request)
	require.ErrorContains(t, err, "environment deletion")
	require.NotErrorIs(t, err, ErrContextCleanupIndeterminate)
	restored, err := store.Meta(cleanupContext, cleanupEncryptedName)
	require.NoError(t, err)
	require.Equal(t, original, restored, "restore original recorded backend and timestamps")
	value, err := store.Get(cleanupContext, cleanupEncryptedName)
	require.NoError(t, err)
	require.Equal(t, cleanupSecret, value)
	persisted, err := config.LoadConfig("", "")
	require.NoError(t, err)
	require.Contains(t, persisted.Contexts, cleanupContext)
	require.Equal(t, []string{cleanupEncryptedName}, persisted.Contexts[cleanupContext].Secrets)
}

func TestContextCleanupCapturesRepairedOwnershipWithoutChangingOriginalTimestamps(t *testing.T) {
	request, envs, store := cleanupFixture(t)
	entry := store.values[cleanupFirstKey]
	entry.meta.Backend = ""
	store.values[cleanupFirstKey] = entry
	store.repairOnGet = true
	envs.deleteErr = errors.New("injected environment cleanup failure")
	err := deleteContextValues(request)
	require.ErrorContains(t, err, "environment deletion")
	restored := store.values[cleanupFirstKey]
	entry.meta.Backend = secrets.BackendFile
	require.Equal(t, entry, restored)
}
