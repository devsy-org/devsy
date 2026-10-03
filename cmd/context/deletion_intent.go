package context

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/devsy-org/devsy/cmd/internal/secretstore"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envstore"
	"github.com/devsy-org/devsy/pkg/secrets"
)

type contextDeletionPersistence struct {
	begin func(contextSnapshot) error
	clear func() error
}

func newContextDeletionPersistence(cfg *config.Config, target string) contextDeletionPersistence {
	return contextDeletionPersistence{
		begin: func(snapshot contextSnapshot) error {
			intent, err := buildContextDeletionIntent(cfg, target, snapshot)
			if err != nil {
				return err
			}
			return config.WriteContextDeletionIntent(intent)
		},
		clear: config.ClearContextDeletionIntent,
	}
}

func buildContextDeletionIntent(
	cfg *config.Config,
	target string,
	snapshot contextSnapshot,
) (*config.ContextDeletionIntent, error) {
	fingerprint, err := config.ContextDeletionFingerprint(cfg.Contexts[target])
	if err != nil {
		return nil, config.ErrContextDeletionIntentInvalid
	}
	path, err := config.GetConfigPath()
	if err != nil {
		return nil, config.ErrContextDeletionIntentInvalid
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, config.ErrContextDeletionIntentInvalid
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	directory, err := config.ContextDeletionStoreDirectory()
	if err != nil {
		return nil, config.ErrContextDeletionIntentInvalid
	}
	dataDirectory, err := config.ContextDeletionDataDirectory()
	if err != nil {
		return nil, config.ErrContextDeletionIntentInvalid
	}
	intent := &config.ContextDeletionIntent{
		SchemaVersion:  1,
		ConfigFile:     filepath.Clean(path),
		StoreDirectory: directory,
		DataDirectory:  dataDirectory,
		Context:        target,
		Fingerprint:    fingerprint,
		EnvNames:       envSnapshotNames(snapshot.envs),
	}
	for _, secret := range snapshot.secrets {
		intent.Secrets = append(
			intent.Secrets,
			config.ContextDeletionSecret{
				Name:    secret.meta.Name,
				Backend: string(secret.meta.Backend),
			},
		)
	}
	return intent, nil
}

func checkpointContextDeletion(request contextDeleteRequest, phase string) {
	if request.checkpoint != nil {
		request.checkpoint(phase)
	}
}

func beginContextDeletion(request contextDeleteRequest, snapshot contextSnapshot) error {
	if request.intent == nil {
		return nil
	}
	return request.intent.begin(snapshot)
}

func clearContextDeletion(request contextDeleteRequest) error {
	if request.intent == nil {
		return nil
	}
	return request.intent.clear()
}

func finishContextDeletion(request contextDeleteRequest) error {
	if request.intent == nil {
		return nil
	}
	if err := removeContextDir(request.context); err != nil {
		return deletionRecoveryError(request.context, err)
	}
	checkpointContextDeletion(request, "directory")
	if err := clearContextDeletion(request); err != nil {
		return deletionRecoveryError(request.context, err)
	}
	checkpointContextDeletion(request, "cleared")
	return nil
}

type contextDeletionRecoveryError struct {
	target string
	cause  error
}

func (e *contextDeletionRecoveryError) Error() string {
	return fmt.Sprintf(
		"context %q deletion is incomplete; retry devsy context delete %q with access to its original secret backends",
		e.target,
		e.target,
	)
}

func (e *contextDeletionRecoveryError) Unwrap() []error {
	return []error{config.ErrContextDeletionPending, e.cause}
}

func deletionRecoveryError(target string, cause error) error {
	return &contextDeletionRecoveryError{target: target, cause: cause}
}

// resumeContextDeletion completes only names recorded in the durable intent.
// It never snapshots deleted values or invents replacements for missing values.
func resumeContextDeletion(cfg *config.Config, intent *config.ContextDeletionIntent) error {
	if err := validateDeletionContextFingerprint(cfg, intent); err != nil {
		return err
	}
	request, err := newContextRecoveryRequest(cfg, intent)
	if err != nil {
		return deletionRecoveryError(intent.Context, err)
	}
	for _, secret := range intent.Secrets {
		if err := deleteRecordedContextSecret(request, secret); err != nil {
			return deletionRecoveryError(intent.Context, err)
		}
	}
	if err := request.envs.DeleteValues(intent.Context, intent.EnvNames); err != nil {
		return deletionRecoveryError(intent.Context, err)
	}
	if cfg.Contexts[intent.Context] != nil {
		candidate := config.CloneConfig(cfg)
		delete(candidate.Contexts, intent.Context)
		resetContextReferences(candidate, intent.Context)
		if err := config.SaveConfig(candidate); err != nil {
			return deletionRecoveryError(intent.Context, err)
		}
	}
	return finishContextDeletion(request)
}

func validateDeletionContextFingerprint(
	cfg *config.Config,
	intent *config.ContextDeletionIntent,
) error {
	if cfg.Contexts[intent.Context] == nil {
		return nil
	}
	fingerprint, err := config.ContextDeletionFingerprint(cfg.Contexts[intent.Context])
	if err != nil || fingerprint != intent.Fingerprint {
		return fmt.Errorf(
			"%w: registered context changed; preserve the intent and repair the context before retrying",
			config.ErrContextDeletionPending,
		)
	}
	return nil
}

func newContextRecoveryRequest(
	cfg *config.Config,
	intent *config.ContextDeletionIntent,
) (contextDeleteRequest, error) {
	request := contextDeleteRequest{config: cfg, context: intent.Context}
	persistence := contextDeletionPersistence{clear: config.ClearContextDeletionIntent}
	request.intent = &persistence
	envs, err := envstore.NewStoreForConfig(cfg)
	if err != nil {
		return request, err
	}
	request.envs = envs.(envstore.BatchStore)
	if len(intent.Secrets) == 0 {
		return request, nil
	}
	store, err := secrets.NewSecretStoreForConfig(cfg, secretstore.Options())
	if err != nil {
		return request, err
	}
	request.secrets = store.(contextSecretStore)
	return request, nil
}

func deleteRecordedContextSecret(
	request contextDeleteRequest,
	record config.ContextDeletionSecret,
) error {
	meta, err := request.secrets.Meta(request.context, record.Name)
	if errors.Is(err, secrets.ErrSecretNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if string(meta.Backend) != record.Backend {
		return errors.New("pending context deletion has conflicting secret ownership")
	}
	return request.secrets.Delete(request.context, record.Name)
}
