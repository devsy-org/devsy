package workspace

import (
	"errors"
	"fmt"
	"time"

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
	versions, err := listVersionsForSourceCached(providerName, source, opts)
	if err != nil {
		return nil, err
	}
	return provider.MarkCurrent(versions, source), nil
}

// listVersionsForSourceCached wraps provider.ListVersionsForSource with cache read/write.
// providerName is used as the cache key. When UseCache is true the cache is consulted;
// successful fetches always update the cache regardless.
func listVersionsForSourceCached(
	providerName, source string,
	opts provider.ListVersionsOptions,
) ([]provider.ProviderVersion, error) {
	hash := provider.HashProviderSource(source)

	if opts.UseCache {
		if cache, err := provider.LoadProviderVersionCache(); err == nil {
			if entry, fresh := cache.Get(providerName, hash); fresh {
				return append([]provider.ProviderVersion(nil), entry.Versions...), nil
			}
		}
	}

	versions, err := provider.ListVersionsForSource(source, opts)
	if err != nil {
		return nil, err
	}
	storeVersionCacheEntry(providerName, hash, versions)
	return versions, nil
}

// storeVersionCacheEntry persists the given versions under the provider name.
// Errors are intentionally swallowed — cache write failure should not block the lister.
func storeVersionCacheEntry(name, sourceHash string, versions []provider.ProviderVersion) {
	cache, err := provider.LoadProviderVersionCache()
	if err != nil || cache == nil {
		cache = provider.ProviderVersionCache{}
	}
	cache[name] = provider.ProviderVersionCacheEntry{
		SourceHash: sourceHash,
		Versions:   versions,
		FetchedAt:  time.Now(),
	}
	_ = provider.SaveProviderVersionCache(cache)
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
