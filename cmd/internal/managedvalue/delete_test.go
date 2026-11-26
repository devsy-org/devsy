package managedvalue

import (
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/require"
)

type testStore struct {
	deleteErr   error
	deleteCalls int
	getCalls    int
}

const testManagedValueName = "VALUE"

func (*testStore) Set(_, _, _ string, _ secrets.Kind) error { return nil }
func (s *testStore) Get(string, string) (string, error) {
	s.getCalls++
	return "", nil
}

func (*testStore) Meta(string, string) (secrets.SecretMeta, error) {
	return secrets.SecretMeta{}, nil
}

func (*testStore) List(string) ([]secrets.SecretMeta, error) {
	return nil, nil
}

func (s *testStore) Delete(string, string) error {
	s.deleteCalls++
	return s.deleteErr
}

func TestDeleteRestoresAttachmentAfterOrdinaryStoreFailure(t *testing.T) {
	for _, binding := range []BindingKind{EnvBinding, SecretBinding} {
		t.Run(bindingName(binding), func(t *testing.T) {
			cfg := testConfig(binding, []string{"BEFORE", testManagedValueName, "AFTER"})
			store := &testStore{deleteErr: errors.New("delete failed")}
			saves := 0
			err := Delete(DeleteRequest{
				Config:  cfg,
				Store:   store,
				Context: config.DefaultContext,
				Name:    testManagedValueName,
				Binding: binding,
				Save:    func(*config.Config) error { saves++; return nil },
			})
			require.ErrorIs(t, err, store.deleteErr)
			require.Equal(
				t,
				[]string{"BEFORE", testManagedValueName, "AFTER"},
				bindingValues(cfg, binding),
			)
			require.Equal(t, 2, saves)
			require.Zero(t, store.getCalls)
		})
	}
}

func TestDeleteIndeterminateStoreFailureLeavesAttachmentDetached(t *testing.T) {
	store := &testStore{deleteErr: &secrets.MutationError{
		Operation: "delete", Context: config.DefaultContext, Name: testManagedValueName,
		Cause: errors.New("remove failed"), Rollback: errors.New("restore failed"),
	}}
	cfg := testConfig(SecretBinding, []string{testManagedValueName})
	err := Delete(DeleteRequest{
		Config:  cfg,
		Store:   store,
		Context: config.DefaultContext,
		Name:    testManagedValueName,
		Binding: SecretBinding,
		Save:    func(*config.Config) error { return nil },
	})
	var consistencyErr *ConsistencyError
	require.ErrorAs(t, err, &consistencyErr)
	require.Equal(t, "store-indeterminate", consistencyErr.Phase)
	require.Empty(t, cfg.Current().Secrets)
	require.Zero(t, store.getCalls)
	require.NotContains(t, err.Error(), "secret payload")
}

func TestDeleteRollbackSaveFailureReportsKnownSplitState(t *testing.T) {
	store := &testStore{deleteErr: errors.New("delete failed")}
	rollbackErr := errors.New("config write failed")
	cfg := testConfig(SecretBinding, []string{testManagedValueName})
	saves := 0
	err := Delete(DeleteRequest{
		Config:  cfg,
		Store:   store,
		Context: config.DefaultContext,
		Name:    testManagedValueName,
		Binding: SecretBinding,
		Save: func(*config.Config) error {
			saves++
			if saves == 2 {
				return rollbackErr
			}
			return nil
		},
	})
	var consistencyErr *ConsistencyError
	require.ErrorAs(t, err, &consistencyErr)
	require.ErrorIs(t, err, store.deleteErr)
	require.ErrorIs(t, err, rollbackErr)
	require.Equal(t, "config-rollback", consistencyErr.Phase)
	require.Equal(t, []string{testManagedValueName}, cfg.Current().Secrets)
	require.NotContains(t, err.Error(), "secret payload")
	require.Zero(t, store.getCalls)
}

func TestDeleteInitialSaveFailureRestoresMemoryAndSkipsStore(t *testing.T) {
	cfg := testConfig(EnvBinding, []string{testManagedValueName})
	store := &testStore{}
	saveErr := errors.New("config write failed")
	err := Delete(DeleteRequest{
		Config:  cfg,
		Store:   store,
		Context: config.DefaultContext,
		Name:    testManagedValueName,
		Binding: EnvBinding,
		Save:    func(*config.Config) error { return saveErr },
	})
	require.ErrorIs(t, err, saveErr)
	require.Equal(t, []string{testManagedValueName}, cfg.Current().EnvVars)
	require.Zero(t, store.deleteCalls)
}

func TestDeleteUnattachedDoesNotSaveConfig(t *testing.T) {
	cfg := testConfig(SecretBinding, nil)
	store := &testStore{}
	saves := 0
	require.NoError(t, Delete(DeleteRequest{
		Config:  cfg,
		Store:   store,
		Context: config.DefaultContext,
		Name:    testManagedValueName,
		Binding: SecretBinding,
		Save:    func(*config.Config) error { saves++; return nil },
	}))
	require.Zero(t, saves)
}

func testConfig(kind BindingKind, values []string) *config.Config {
	ctx := &config.ContextConfig{}
	if kind == SecretBinding {
		ctx.Secrets = values
	} else {
		ctx.EnvVars = values
	}
	return &config.Config{
		DefaultContext: config.DefaultContext,
		Contexts:       map[string]*config.ContextConfig{config.DefaultContext: ctx},
	}
}

func bindingValues(cfg *config.Config, kind BindingKind) []string {
	if kind == SecretBinding {
		return cfg.Current().Secrets
	}
	return cfg.Current().EnvVars
}

func bindingName(kind BindingKind) string {
	if kind == SecretBinding {
		return "secret"
	}
	return "env"
}
