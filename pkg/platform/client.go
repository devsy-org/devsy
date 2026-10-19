package platform

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/provider"
)

func InitClientFromHost(
	ctx context.Context,
	devsyConfig *config.Config,
	devsyProHost string,
) (client.Client, error) {
	provider, err := ProviderFromHost(ctx, devsyConfig, devsyProHost)
	if err != nil {
		return nil, fmt.Errorf("provider from platform instance: %w", err)
	}

	return InitClientFromProvider(ctx, devsyConfig, provider)
}

func InitClientFromProvider(
	ctx context.Context,
	devsyConfig *config.Config,
	providerName string,
) (client.Client, error) {
	configPath, err := DevsyConfigPath(devsyConfig.DefaultContext, providerName)
	if err != nil {
		return nil, fmt.Errorf("devsy config path: %w", err)
	}

	return client.InitClientFromPath(ctx, configPath)
}

func ProviderFromHost(
	ctx context.Context,
	devsyConfig *config.Config,
	devsyProHost string,
) (string, error) {
	platformInstanceConfig, err := provider.LoadPlatformInstanceConfig(
		devsyConfig.DefaultContext,
		devsyProHost,
	)
	if err != nil {
		return "", fmt.Errorf("load platform instance %s: %w", devsyProHost, err)
	}

	return platformInstanceConfig.Provider, nil
}

func DevsyConfigPath(context string, providerName string) (string, error) {
	providerDir, err := provider.GetProviderDir(context, providerName)
	if err != nil {
		return "", err
	}

	configPath := filepath.Join(providerDir, "devsy-config.json")

	return configPath, nil
}
