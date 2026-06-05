package workspace

import (
	"errors"
	"fmt"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
)

// ListProviderVersions returns available versions for the named provider, newest first.
// Returns provider.ErrVersionListUnsupported when the source type can't be enumerated.
func ListProviderVersions(
	devsyConfig *config.Config,
	providerName string,
	opts provider.ListVersionsOptions,
) ([]provider.ProviderVersion, error) {
	source, err := ResolveProviderSource(devsyConfig, providerName)
	if err != nil {
		return nil, fmt.Errorf("resolve provider source: %w", err)
	}
	versions, err := provider.ListVersionsForSourceCached(providerName, source, opts)
	if err != nil {
		return nil, err
	}
	return provider.MarkCurrent(versions, source), nil
}

// SetProviderVersion switches the provider to the given tag.
func SetProviderVersion(devsyConfig *config.Config, providerName, tag string) error {
	source, err := ResolveProviderSource(devsyConfig, providerName)
	if err != nil {
		return fmt.Errorf("resolve provider source: %w", err)
	}
	rewritten, err := provider.RewriteSourceTag(source, tag)
	if err != nil {
		return err
	}
	_, err = UpdateProvider(devsyConfig, providerName, rewritten)
	return err
}

// CheckAllProviderVersions queries the latest version for every installed provider whose
// source supports version listing. Per-provider errors are recorded in the result map, not
// returned as a fatal error. Bypasses the cache so callers always see the freshest data.
func CheckAllProviderVersions(
	devsyConfig *config.Config,
) (map[string]provider.ProviderVersionCheckResult, error) {
	providers, err := LoadAllProviders(devsyConfig)
	if err != nil {
		return nil, err
	}
	out := map[string]provider.ProviderVersionCheckResult{}
	for name, p := range providers {
		out[name] = checkOneProviderVersion(devsyConfig, name, p)
	}
	return out, nil
}

func checkOneProviderVersion(
	devsyConfig *config.Config,
	name string,
	p *ProviderWithOptions,
) provider.ProviderVersionCheckResult {
	source, err := ResolveProviderSource(devsyConfig, name)
	if err != nil {
		return provider.ProviderVersionCheckResult{Error: err.Error()}
	}
	_, currentTag := provider.SplitSourceAndTag(source)
	if currentTag == "" && p != nil && p.Config != nil {
		currentTag = p.Config.Version
	}
	versions, err := ListProviderVersions(
		devsyConfig,
		name,
		provider.ListVersionsOptions{UseCache: false},
	)
	if errors.Is(err, provider.ErrVersionListUnsupported) {
		return provider.ProviderVersionCheckResult{Current: currentTag, Unsupported: true}
	}
	if err != nil {
		return provider.ProviderVersionCheckResult{Current: currentTag, Error: err.Error()}
	}
	return provider.BuildVersionCheckResult(currentTag, versions)
}
