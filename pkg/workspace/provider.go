package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
)

var ErrNoWorkspaceFound = errors.New("no workspace found")

type ProviderWithOptions struct {
	Config *provider.ProviderConfig `json:"config,omitempty"`
	State  *config.ProviderConfig   `json:"state,omitempty"`
}

type ProviderParams struct {
	DevsyConfig  *config.Config
	ProviderName string
	Raw          []byte
	Source       *provider.ProviderSource
}

// LoadProviders loads all known providers for the given context.
func LoadProviders(
	devsyConfig *config.Config,
) (*ProviderWithOptions, map[string]*ProviderWithOptions, error) {
	defaultContext := devsyConfig.Current()
	retProviders, err := LoadAllProviders(devsyConfig)
	if err != nil {
		return nil, nil, err
	}

	if defaultContext.DefaultProvider == "" {
		return nil, nil, fmt.Errorf("no default provider found")
	}
	if retProviders[defaultContext.DefaultProvider] == nil {
		return nil, nil, fmt.Errorf(
			"provider with name %s not found",
			defaultContext.DefaultProvider,
		)
	}

	return retProviders[defaultContext.DefaultProvider], retProviders, nil
}

func LoadAllProviders(
	devsyConfig *config.Config,
) (map[string]*ProviderWithOptions, error) {
	retProviders := map[string]*ProviderWithOptions{}

	loadConfiguredProviders(devsyConfig, retProviders)

	if err := loadUnconfiguredProviders(devsyConfig, retProviders); err != nil {
		return nil, err
	}

	return retProviders, nil
}

func FindProvider(
	devsyConfig *config.Config,
	name string,
) (*ProviderWithOptions, error) {
	retProviders, err := LoadAllProviders(devsyConfig)
	if err != nil {
		return nil, err
	}
	if retProviders[name] == nil {
		return nil, fmt.Errorf("provider with name %s not found", name)
	}

	return retProviders[name], nil
}

func ProviderFromHost(
	ctx context.Context,
	devsyConfig *config.Config,
	proHost string,
) (*provider.ProviderConfig, error) {
	proInstanceConfig, err := provider.LoadProInstanceConfig(devsyConfig.DefaultContext, proHost)
	if err != nil {
		return nil, fmt.Errorf("load pro instance %s: %w", proHost, err)
	}

	foundProvider, err := FindProvider(devsyConfig, proInstanceConfig.Provider)
	if err != nil {
		return nil, fmt.Errorf("find provider: %w", err)
	}
	if !foundProvider.Config.IsProxyProvider() && !foundProvider.Config.IsDaemonProvider() {
		return nil, fmt.Errorf("provider is not a pro provider")
	}

	return foundProvider.Config, nil
}

func AddProvider(
	devsyConfig *config.Config,
	providerName, providerSourceRaw string,
) (*provider.ProviderConfig, error) {
	providerRaw, providerSource, err := provider.ResolveProvider(providerSourceRaw)
	if err != nil {
		return nil, err
	}

	return AddProviderRaw(ProviderParams{
		DevsyConfig:  devsyConfig,
		ProviderName: providerName,
		Source:       providerSource,
		Raw:          providerRaw,
	})
}

func AddProviderRaw(p ProviderParams) (*provider.ProviderConfig, error) {
	providerConfig, err := installRawProvider(p)
	if err != nil {
		return nil, err
	}

	if p.DevsyConfig.Current().Providers == nil {
		p.DevsyConfig.Current().Providers = map[string]*config.ProviderConfig{}
	}
	if p.DevsyConfig.Current().Providers[providerConfig.Name] == nil {
		p.DevsyConfig.Current().Providers[providerConfig.Name] = &config.ProviderConfig{
			CreationTimestamp: types.Now(),
		}
	}

	if err := config.SaveConfig(p.DevsyConfig); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}

	return providerConfig, nil
}

func UpdateProvider(
	devsyConfig *config.Config,
	providerName, providerSourceRaw string,
) (*provider.ProviderConfig, error) {
	if devsyConfig.Current().Providers[providerName] == nil {
		return nil, fmt.Errorf("provider %s not found", providerName)
	}

	if providerSourceRaw == "" {
		s, err := ResolveProviderSource(devsyConfig, providerName)
		if err != nil {
			return nil, err
		}
		providerSourceRaw = s
	}

	providerRaw, providerSource, err := provider.ResolveProvider(providerSourceRaw)
	if err != nil {
		return nil, err
	}

	return updateProvider(ProviderParams{
		DevsyConfig:  devsyConfig,
		ProviderName: providerName,
		Raw:          providerRaw,
		Source:       providerSource,
	})
}

func CloneProvider(
	devsyConfig *config.Config,
	providerName, providerSourceRaw string,
) (*ProviderWithOptions, error) {
	sourceProvider, err := FindProvider(devsyConfig, providerSourceRaw)
	if err != nil {
		return nil, err
	}

	providerConfig, err := installProvider(
		ProviderParams{
			DevsyConfig:  devsyConfig,
			ProviderName: providerName,
			Source:       &sourceProvider.Config.Source,
		},
		sourceProvider.Config)
	if err != nil {
		return nil, err
	}
	sourceProvider.Config = providerConfig

	return sourceProvider, nil
}

func ResolveProviderSource(
	devsyConfig *config.Config,
	providerName string,
) (string, error) {
	providerConfig, err := FindProvider(devsyConfig, providerName)
	if err != nil {
		return "", fmt.Errorf("find provider: %w", err)
	}

	source := provider.GetProviderSource(providerConfig.Config.Source, providerConfig.Config.Name)
	if source == "" {
		return "", fmt.Errorf("provider %s source is missing", providerName)
	}

	return source, nil
}

func loadConfiguredProviders(
	devsyConfig *config.Config,
	retProviders map[string]*ProviderWithOptions,
) {
	defaultContext := devsyConfig.Current()
	for providerName, providerState := range defaultContext.Providers {
		if retProviders[providerName] != nil {
			retProviders[providerName].State = providerState
			continue
		}

		providerConfig, err := provider.LoadProviderConfig(
			devsyConfig.DefaultContext,
			providerName,
		)
		if err != nil {
			log.Warnf("error loading provider %s: %v", providerName, err)
			continue
		}

		retProviders[providerName] = &ProviderWithOptions{
			Config: providerConfig,
			State:  providerState,
		}
	}
}

func loadUnconfiguredProviders(
	devsyConfig *config.Config,
	retProviders map[string]*ProviderWithOptions,
) error {
	providerDir, err := provider.GetProvidersDir(devsyConfig.DefaultContext)
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(providerDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	for _, entry := range entries {
		if shouldSkipEntry(entry, retProviders) {
			continue
		}

		if err := loadProviderEntry(devsyConfig, entry, retProviders); err != nil {
			return err
		}
	}

	return nil
}

func shouldSkipEntry(entry os.DirEntry, retProviders map[string]*ProviderWithOptions) bool {
	return retProviders[entry.Name()] != nil || !entry.IsDir() ||
		strings.HasPrefix(entry.Name(), ".DS_Store")
}

func loadProviderEntry(
	devsyConfig *config.Config,
	entry os.DirEntry,
	retProviders map[string]*ProviderWithOptions,
) error {
	providerConfig, err := provider.LoadProviderConfig(devsyConfig.DefaultContext, entry.Name())
	if err != nil {
		return err
	}

	retProviders[providerConfig.Name] = &ProviderWithOptions{
		Config: providerConfig,
	}

	return nil
}

func installRawProvider(p ProviderParams) (*provider.ProviderConfig, error) {
	providerConfig, err := provider.ParseProvider(bytes.NewReader(p.Raw))
	if err != nil {
		return nil, err
	}
	return installProvider(ProviderParams{
		DevsyConfig:  p.DevsyConfig,
		ProviderName: p.ProviderName,
		Source:       p.Source,
	}, providerConfig)
}

func installProvider(
	p ProviderParams,
	providerConfig *provider.ProviderConfig,
) (*provider.ProviderConfig, error) {
	if p.Source == nil {
		return nil, fmt.Errorf("provider source is required")
	}

	providerConfig.Source = *p.Source
	if p.ProviderName != "" {
		providerConfig.Name = p.ProviderName
	}

	if err := checkProviderNotExists(p.DevsyConfig, providerConfig.Name); err != nil {
		return nil, err
	}

	if err := downloadAndSaveProvider(p, providerConfig); err != nil {
		return nil, err
	}

	return providerConfig, nil
}

func updateProvider(p ProviderParams) (*provider.ProviderConfig, error) {
	providerConfig, err := parseAndValidateProvider(p)
	if err != nil {
		return nil, err
	}

	cleanupOldOptions(p.DevsyConfig, providerConfig)

	if err := config.SaveConfig(p.DevsyConfig); err != nil {
		return nil, err
	}

	if err := downloadAndSaveProvider(p, providerConfig); err != nil {
		return nil, err
	}

	return providerConfig, nil
}

func parseAndValidateProvider(p ProviderParams) (*provider.ProviderConfig, error) {
	providerConfig, err := provider.ParseProvider(bytes.NewReader(p.Raw))
	if err != nil {
		return nil, err
	}
	if p.Source == nil {
		return nil, fmt.Errorf("provider source is required")
	}

	providerConfig.Source = *p.Source
	if p.ProviderName != "" {
		providerConfig.Name = p.ProviderName
	}
	if providerConfig.Options == nil {
		providerConfig.Options = map[string]*types.Option{}
	}

	return providerConfig, nil
}

func checkProviderNotExists(devsyConfig *config.Config, providerName string) error {
	if devsyConfig.Current().Providers[providerName] != nil {
		return fmt.Errorf("provider %s already exists", providerName)
	}

	providerDir, err := provider.GetProviderDir(devsyConfig.DefaultContext, providerName)
	if err != nil {
		return err
	}

	if _, err := os.Stat(providerDir); err == nil {
		return fmt.Errorf("provider %s already exists", providerName)
	}

	return nil
}

func downloadAndSaveProvider(p ProviderParams, providerConfig *provider.ProviderConfig) error {
	binariesDir, err := provider.GetProviderBinariesDir(
		p.DevsyConfig.DefaultContext,
		providerConfig.Name,
	)
	if err != nil {
		return fmt.Errorf("get binaries dir: %w", err)
	}

	providerDir, err := provider.GetProviderDir(p.DevsyConfig.DefaultContext, providerConfig.Name)
	if err != nil {
		return fmt.Errorf("get provider dir: %w", err)
	}

	if _, err := provider.DownloadBinaries(
		providerConfig.Binaries,
		binariesDir,
	); err != nil {
		_ = os.RemoveAll(providerDir)
		return fmt.Errorf("download binaries: %w", err)
	}

	return provider.SaveProviderConfig(p.DevsyConfig.DefaultContext, providerConfig)
}

func cleanupOldOptions(devsyConfig *config.Config, providerConfig *provider.ProviderConfig) {
	providerState := devsyConfig.Current().Providers[providerConfig.Name]
	if providerState == nil || providerState.Options == nil {
		return
	}

	for optionName := range providerState.Options {
		if _, ok := providerConfig.Options[optionName]; !ok {
			delete(providerState.Options, optionName)
		}
	}
}

// SwitchProvider updates the provider name for the given workspace with client locking.
// It persists the new provider name before resolving the client so that FindProvider
// can locate the already-renamed provider directory.
func SwitchProvider(
	ctx context.Context,
	devsyConfig *config.Config,
	workspace *provider.Workspace,
	newProviderName string,
) error {
	oldProviderName := workspace.Provider.Name
	workspace.Provider.Name = newProviderName

	err := provider.SaveWorkspaceConfig(workspace)
	if err != nil {
		workspace.Provider.Name = oldProviderName
		return fmt.Errorf("failed to save workspace config: %w", err)
	}

	revert := func() {
		workspace.Provider.Name = oldProviderName
		_ = provider.SaveWorkspaceConfig(workspace)
	}

	client, err := Get(ctx, GetOptions{
		DevsyConfig: devsyConfig,
		Args:        []string{workspace.ID},
		Owner:       platform.AllOwnerFilter,
	})
	if err != nil {
		revert()
		return fmt.Errorf("failed to get client for workspace %s: %w", workspace.ID, err)
	}

	err = client.Lock(ctx)
	if err != nil {
		revert()
		return fmt.Errorf("failed to lock workspace %s: %w", workspace.ID, err)
	}

	defer client.Unlock()
	status, err := client.Status(ctx, client2.StatusOptions{ContainerStatus: true})
	if err != nil {
		revert()
		return fmt.Errorf("failed to get status for workspace %s: %w", workspace.ID, err)
	}

	if status != client2.StatusStopped && status != client2.StatusNotFound {
		if err := client.Stop(ctx, client2.StopOptions{}); err != nil {
			revert()
			return fmt.Errorf("failed to stop workspace %s before switching: %w", workspace.ID, err)
		}
	}

	return nil
}
