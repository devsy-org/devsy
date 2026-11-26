package secrets

import (
	"fmt"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envstore"
)

// NewResolverForConfig constructs the local Devsy source plus all external
// sources registered in the active local context.
func NewResolverForConfig(devsyConfig *config.Config, options ...StoreOptions) (*Resolver, error) {
	if devsyConfig == nil {
		return nil, fmt.Errorf("devsy config is nil")
	}
	store, err := NewSecretStoreForConfig(devsyConfig, options...)
	if err != nil {
		return nil, err
	}
	envs, err := envstore.NewStoreForConfig(devsyConfig)
	if err != nil {
		return nil, err
	}
	resolver := NewResolver()
	resolver.envSource = &localEnvironmentSource{store: envs, context: devsyConfig.DefaultContext}
	if err := resolver.Register(
		LocalSourceName,
		LocalSourceName,
		NewLocalSource(store, devsyConfig.DefaultContext),
	); err != nil {
		return nil, err
	}
	configs, err := LoadSourceConfigs(devsyConfig)
	if err != nil {
		return nil, err
	}
	for _, sourceConfig := range configs {
		if err := RegisterConfiguredSource(resolver, sourceConfig); err != nil {
			return nil, err
		}
	}
	return resolver, nil
}

func RegisterConfiguredSource(resolver *Resolver, sourceConfig SourceConfig) error {
	if resolver == nil {
		return fmt.Errorf("secret resolver is nil")
	}
	switch sourceConfig.Type {
	case SOPSFormatter:
		return resolver.Register(
			sourceConfig.Name,
			sourceConfig.Type,
			NewSOPSSource(sourceConfig.Name, sourceConfig.Path, sourceConfig.Format),
		)
	default:
		return fmt.Errorf(
			"secret source %q has unsupported type %q",
			sourceConfig.Name,
			sourceConfig.Type,
		)
	}
}

// NewEnvironmentResolverForConfig avoids external secret-source configuration
// and secret catalog reads for environment-only workspace requests.
func NewEnvironmentResolverForConfig(cfg *config.Config) (*Resolver, error) {
	if cfg == nil {
		return nil, fmt.Errorf("devsy config is nil")
	}
	envs, err := envstore.NewStoreForConfig(cfg)
	if err != nil {
		return nil, err
	}
	resolver := NewResolver()
	resolver.envSource = &localEnvironmentSource{store: envs, context: cfg.DefaultContext}
	return resolver, nil
}
