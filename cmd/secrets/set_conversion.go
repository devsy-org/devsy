package secrets

import (
	"errors"
	"fmt"

	"github.com/devsy-org/devsy/pkg/envstore"
	"github.com/devsy-org/devsy/pkg/secrets"
)

type secretSetTarget struct {
	context, name, value string
}

// The caller holds the config lock throughout conversion and compensation.
func setSecretValue(
	store secrets.SecretStore,
	envs envstore.EnvStore,
	target secretSetTarget,
) error {
	contextName, name, value := target.context, target.name, target.value
	_, err := envs.Meta(contextName, name)
	if errors.Is(err, envstore.ErrNotFound) {
		return store.Set(contextName, name, value)
	}
	if err != nil {
		return err
	}
	restore, err := secretRestoration(store, contextName, name)
	if err != nil {
		return err
	}
	if err := store.Set(contextName, name, value); err != nil {
		return err
	}
	if err := envs.Delete(contextName, name); err != nil {
		return &conversionError{
			context: contextName, name: name, cause: err, rollback: restore(),
		}
	}
	return nil
}

func secretRestoration(
	store secrets.SecretStore,
	contextName, name string,
) (func() error, error) {
	_, err := store.Meta(contextName, name)
	if errors.Is(err, secrets.ErrSecretNotFound) {
		return func() error { return store.Delete(contextName, name) }, nil
	}
	if err != nil {
		return nil, err
	}
	previous, err := store.Get(contextName, name)
	if err != nil {
		return nil, err
	}
	return func() error { return store.Set(contextName, name, previous) }, nil
}

type conversionError struct {
	context, name string
	cause         error
	rollback      error
}

func (e *conversionError) Error() string {
	if e.rollback != nil {
		return fmt.Sprintf(
			"secret conversion for %q in context %q is incomplete: plaintext cleanup and "+
				"secret restoration failed; restore backend access, inspect both stores, then "+
				"retry secret set or use env delete to finish plaintext cleanup",
			e.name, e.context,
		)
	}
	return fmt.Sprintf(
		"secret conversion for %q in context %q failed: plaintext cleanup failed and the "+
			"original secret state was restored; repair env.yaml and retry secret set",
		e.name, e.context,
	)
}

func (e *conversionError) Unwrap() []error {
	if e.rollback != nil {
		return []error{e.cause, e.rollback, secrets.ErrStateIndeterminate}
	}
	return []error{e.cause}
}
