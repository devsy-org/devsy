package context

import (
	"errors"
	"fmt"
	"slices"

	"github.com/devsy-org/devsy/cmd/internal/secretstore"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envstore"
	"github.com/devsy-org/devsy/pkg/secrets"
)

var ErrContextCleanupIndeterminate = errors.New("context cleanup rollback incomplete")

type contextSecretStore interface {
	Meta(string, string) (secrets.SecretMeta, error)
	Get(string, string) (string, error)
	Delete(string, string) error
	Restore(secrets.SecretMeta, string) error
}

type contextDeleteRequest struct {
	config     *config.Config
	context    string
	envs       envstore.BatchStore
	secrets    contextSecretStore
	save       func(*config.Config) error
	intent     *contextDeletionPersistence
	checkpoint func(string)
}

type contextRollback struct {
	attempted  int
	restoreEnv bool
	phase      string
	cause      error
}

type contextSnapshot struct {
	envs    []envstore.EnvValue
	secrets []contextSecretSnapshot
}

type contextSecretSnapshot struct {
	meta  secrets.SecretMeta
	value string
}

// ContextCleanupError reports safe action metadata without backend/parser text,
// which may include a value. Causes remain available for errors.Is/errors.As.
type ContextCleanupError struct {
	Context         string
	Phase           string
	Cause           error
	Rollback        error
	RecoveryPending bool
}

func (e *ContextCleanupError) Error() string {
	if e.RecoveryPending {
		return fmt.Sprintf(
			"context %q cleanup rollback is incomplete; commands are blocked until recovery; "+
				"restore store access and retry devsy context delete %q to complete deletion",
			e.Context,
			e.Context,
		)
	}
	if errors.Is(e.Cause, config.ErrContextDeletionPending) {
		return fmt.Sprintf(
			"context %q cleanup could not be durably canceled; a deletion intent may remain; "+
				"retry devsy context delete %q to complete deletion",
			e.Context,
			e.Context,
		)
	}
	if e.Rollback != nil {
		return fmt.Sprintf("context %q cleanup failed during %s and rollback is incomplete; "+
			"keep the context registered, restore store access, verify its managed values, "+
			"and retry deletion", e.Context, e.Phase)
	}
	return fmt.Sprintf(
		"context %q cleanup failed during %s; existing managed values were preserved",
		e.Context,
		e.Phase,
	)
}

func (e *ContextCleanupError) Unwrap() []error {
	if e.Rollback != nil {
		causes := []error{ErrContextCleanupIndeterminate, e.Cause, e.Rollback}
		if e.RecoveryPending {
			causes = append(causes, config.ErrContextDeletionPending)
		}
		return causes
	}
	return []error{e.Cause}
}

func newContextDeleteRequest(cfg *config.Config, contextName string) (contextDeleteRequest, error) {
	request := contextDeleteRequest{config: cfg, context: contextName, save: config.SaveConfig}
	persistence := newContextDeletionPersistence(cfg, contextName)
	request.intent = &persistence
	envs, err := envstore.NewStoreForConfig(cfg)
	if err != nil {
		return request, cleanupError(contextName, "environment store is unavailable", err)
	}
	request.envs = envs.(envstore.BatchStore)
	if len(cfg.Contexts[contextName].Secrets) == 0 {
		return request, nil
	}
	store, err := secrets.NewSecretStoreForConfig(cfg, secretstore.Options())
	if err != nil {
		return request, cleanupError(contextName, "secret store is unavailable", err)
	}
	request.secrets = store.(contextSecretStore)
	return request, nil
}

func cleanupError(contextName, phase string, cause error) *ContextCleanupError {
	return &ContextCleanupError{Context: contextName, Phase: phase, Cause: cause}
}

// The caller holds the config lock. Value snapshots are taken before any
// deletion and kept only in memory; this compensates synchronous failures, not
// crashes across separate files/backends.
func deleteContextValues(request contextDeleteRequest) error {
	snapshot, err := snapshotContextValues(request)
	if err != nil {
		return err
	}
	if err := beginContextDeletion(request, snapshot); err != nil {
		return cleanupError(request.context, "deletion intent persistence", err)
	}
	checkpointContextDeletion(request, "intent")
	attempted := 0
	for _, secret := range snapshot.secrets {
		attempted++
		if err := request.secrets.Delete(request.context, secret.meta.Name); err != nil {
			return rollbackContextValues(
				request,
				snapshot,
				contextRollback{attempted: attempted, phase: "secret deletion", cause: err},
			)
		}
		checkpointContextDeletion(request, "secret")
	}
	if err := request.envs.DeleteValues(
		request.context,
		envSnapshotNames(snapshot.envs),
	); err != nil {
		return rollbackContextValues(
			request,
			snapshot,
			contextRollback{attempted: attempted, phase: "environment deletion", cause: err},
		)
	}
	checkpointContextDeletion(request, "environment")
	candidate := config.CloneConfig(request.config)
	delete(candidate.Contexts, request.context)
	resetContextReferences(candidate, request.context)
	if err := request.save(candidate); err != nil {
		// SaveConfig atomically replaces config.yaml and returns failures before
		// replacement, so the registered context remains in the original file.
		return rollbackContextValues(
			request,
			snapshot,
			contextRollback{
				attempted:  attempted,
				restoreEnv: true,
				phase:      "config persistence",
				cause:      err,
			},
		)
	}
	checkpointContextDeletion(request, "config")
	return finishContextDeletion(request)
}

func snapshotContextValues(request contextDeleteRequest) (contextSnapshot, error) {
	snapshot := contextSnapshot{}
	values, err := request.envs.List(request.context)
	if err != nil {
		return snapshot, cleanupError(request.context, "environment store is unavailable", err)
	}
	snapshot.envs = values
	seen := map[string]bool{}
	for _, binding := range request.config.Contexts[request.context].Secrets {
		secret, exists, err := snapshotContextSecret(request, binding)
		if err != nil {
			return snapshot, cleanupError(request.context, "secret snapshot", err)
		}
		if exists && !seen[secret.meta.Name] {
			snapshot.secrets = append(snapshot.secrets, secret)
			seen[secret.meta.Name] = true
		}
	}
	return snapshot, nil
}

func snapshotContextSecret(
	request contextDeleteRequest,
	binding string,
) (contextSecretSnapshot, bool, error) {
	ref, err := secrets.ParseRef(binding)
	if err != nil {
		return contextSecretSnapshot{}, false, err
	}
	// External sources are references, never owned by local context cleanup.
	if ref.Source != secrets.LocalSourceName {
		return contextSecretSnapshot{}, false, nil
	}
	meta, err := request.secrets.Meta(request.context, ref.Name)
	if errors.Is(err, secrets.ErrSecretNotFound) {
		return contextSecretSnapshot{}, false, nil
	}
	if err != nil {
		return contextSecretSnapshot{}, false, err
	}
	value, err := request.secrets.Get(request.context, ref.Name)
	if err != nil {
		return contextSecretSnapshot{}, false, err
	}
	if meta.Backend == "" {
		repaired, err := request.secrets.Meta(request.context, ref.Name)
		if err != nil {
			return contextSecretSnapshot{}, false, err
		}
		meta.Backend = repaired.Backend
	}
	return contextSecretSnapshot{meta: meta, value: value}, true, nil
}

func envSnapshotNames(values []envstore.EnvValue) []string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.Name)
	}
	return names
}

func rollbackContextValues(
	request contextDeleteRequest,
	snapshot contextSnapshot,
	failure contextRollback,
) error {
	var failures []error
	if failure.restoreEnv {
		if err := request.envs.RestoreValues(request.context, snapshot.envs); err != nil {
			failures = append(failures, err)
		}
	}
	for _, secret := range slices.Backward(snapshot.secrets[:failure.attempted]) {
		if err := request.secrets.Restore(secret.meta, secret.value); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) == 0 {
		if err := clearContextDeletion(request); err != nil {
			return deletionRecoveryError(request.context, err)
		}
	}
	result := cleanupError(request.context, failure.phase, failure.cause)
	result.Rollback = errors.Join(failures...)
	result.RecoveryPending = result.Rollback != nil && request.intent != nil
	return result
}
