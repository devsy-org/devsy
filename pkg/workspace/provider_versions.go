package workspace

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
)

// ErrVersionListUnsupported indicates the provider's source type does not expose a list of versions.
var ErrVersionListUnsupported = errors.New("provider source does not support version listing")

// ErrVersionListRateLimited indicates upstream rate-limiting hit the lister.
var ErrVersionListRateLimited = errors.New("provider version list rate-limited")

// ErrInvalidProviderSourceForVersionSwap indicates a source string cannot be safely rewritten with a version tag.
var ErrInvalidProviderSourceForVersionSwap = errors.New(
	"provider source cannot be safely rewritten with a version tag",
)

// githubAPIBaseURL is the base URL for GitHub API calls; overridden in tests.
var githubAPIBaseURL = "https://api.github.com"

// ProviderVersion describes one upstream release.
type ProviderVersion struct {
	Tag         string    `json:"tag"`
	PublishedAt time.Time `json:"publishedAt"`
	Prerelease  bool      `json:"prerelease"`
	Current     bool      `json:"current"`
}

// ListProviderVersions returns available versions for the named provider, newest first.
// Returns ErrVersionListUnsupported when the source type can't be enumerated.
func ListProviderVersions(
	devsyConfig *config.Config,
	providerName string,
	opts ListVersionsOptions,
) ([]ProviderVersion, error) {
	source, err := ResolveProviderSource(devsyConfig, providerName)
	if err != nil {
		return nil, fmt.Errorf("resolve provider source: %w", err)
	}
	versions, err := listVersionsForSourceCached(providerName, source, opts)
	if err != nil {
		return nil, err
	}
	return markCurrent(versions, source), nil
}

// listVersionsForSourceCached wraps listVersionsForSource with cache read/write.
// providerName is used as the cache key. When UseCache is true the cache is consulted;
// successful fetches always update the cache regardless.
func listVersionsForSourceCached(
	providerName, source string,
	opts ListVersionsOptions,
) ([]ProviderVersion, error) {
	hash := hashProviderSource(source)

	if opts.UseCache {
		if cache, err := LoadProviderVersionCache(); err == nil {
			if entry, fresh := cache.Get(providerName, hash); fresh {
				return append([]ProviderVersion(nil), entry.Versions...), nil
			}
		}
	}

	versions, err := listVersionsForSource(source, opts)
	if err != nil {
		return nil, err
	}
	storeVersionCacheEntry(providerName, hash, versions)
	return versions, nil
}

// storeVersionCacheEntry persists the given versions under the provider name.
// Errors are intentionally swallowed — cache write failure should not block the lister.
func storeVersionCacheEntry(name, sourceHash string, versions []ProviderVersion) {
	cache, err := LoadProviderVersionCache()
	if err != nil || cache == nil {
		cache = providerVersionCache{}
	}
	cache[name] = providerVersionCacheEntry{
		SourceHash: sourceHash,
		Versions:   versions,
		FetchedAt:  time.Now(),
	}
	_ = SaveProviderVersionCache(cache)
}

// listVersionsForSource dispatches to the appropriate lister based on source shape.
// Separated from ListProviderVersions so it can be tested without a real Config.
func listVersionsForSource(source string, opts ListVersionsOptions) ([]ProviderVersion, error) {
	switch classifyVersionSource(source) {
	case sourceGitHub:
		org, repo, ok := parseGitHubSourcePath(source)
		if !ok {
			return nil, fmt.Errorf("invalid github source: %s", source)
		}
		return listGitHubReleases(githubAPIBaseURL, org, repo, opts.IncludePrerelease)
	case sourceManifestURL:
		return listManifestVersions(source, opts.IncludePrerelease)
	case sourceLocal, sourceUnknown:
		return nil, ErrVersionListUnsupported
	}
	return nil, ErrVersionListUnsupported
}

// markCurrent flags the version whose tag matches the source's pinned tag.
func markCurrent(versions []ProviderVersion, canonicalSource string) []ProviderVersion {
	_, currentTag := splitSourceAndTag(canonicalSource)
	if currentTag == "" {
		return versions
	}
	for i := range versions {
		if versions[i].Tag == currentTag {
			versions[i].Current = true
		}
	}
	return versions
}

// ListVersionsOptions tunes the lister.
type ListVersionsOptions struct {
	UseCache          bool
	IncludePrerelease bool
}

// rewriteSourceTag returns the source with its version tag replaced by the given one.
// Errors if the tag is empty or the source contains multiple @ signs in non-version positions.
func rewriteSourceTag(source, tag string) (string, error) {
	if tag == "" {
		return "", fmt.Errorf("version tag must not be empty")
	}
	base, _ := splitSourceAndTag(source)
	if strings.Contains(base, "@") {
		return "", ErrInvalidProviderSourceForVersionSwap
	}
	return base + "@" + tag, nil
}

// SetProviderVersion switches the provider to the given tag.
func SetProviderVersion(devsyConfig *config.Config, providerName, tag string) error {
	source, err := ResolveProviderSource(devsyConfig, providerName)
	if err != nil {
		return fmt.Errorf("resolve provider source: %w", err)
	}
	rewritten, err := rewriteSourceTag(source, tag)
	if err != nil {
		return err
	}
	_, err = UpdateProvider(devsyConfig, providerName, rewritten)
	return err
}

type sourceKind int

const (
	sourceUnknown sourceKind = iota
	sourceGitHub
	sourceManifestURL
	sourceLocal
)

func classifyVersionSource(canonical string) sourceKind {
	// Strip @version suffix if present; only the leftmost @ counts as a tag separator.
	bare := canonical
	if before, _, ok := strings.Cut(canonical, "@"); ok {
		bare = before
	}
	switch {
	case strings.HasPrefix(bare, "github.com/"):
		return sourceGitHub
	case strings.HasPrefix(bare, "https://"), strings.HasPrefix(bare, "http://"):
		return sourceManifestURL
	case strings.HasPrefix(bare, "/"),
		strings.HasPrefix(bare, "./"),
		strings.HasPrefix(bare, "../"):
		return sourceLocal
	default:
		return sourceUnknown
	}
}

func splitSourceAndTag(canonical string) (base, tag string) {
	if before, after, ok := strings.Cut(canonical, "@"); ok {
		return before, after
	}
	return canonical, ""
}

// ProviderVersionCheckResult is the per-provider result from CheckAllProviderVersions.
type ProviderVersionCheckResult struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Unsupported     bool   `json:"unsupported"`
	Error           string `json:"error,omitempty"`
}

// CheckAllProviderVersions queries the latest version for every installed provider whose
// source supports version listing. Per-provider errors are recorded in the result map, not
// returned as a fatal error. Bypasses the cache so callers always see the freshest data.
func CheckAllProviderVersions(
	devsyConfig *config.Config,
) (map[string]ProviderVersionCheckResult, error) {
	providers, err := LoadAllProviders(devsyConfig)
	if err != nil {
		return nil, err
	}
	out := map[string]ProviderVersionCheckResult{}
	for name, p := range providers {
		out[name] = checkOneProviderVersion(devsyConfig, name, p)
	}
	return out, nil
}

func checkOneProviderVersion(
	devsyConfig *config.Config,
	name string,
	p *ProviderWithOptions,
) ProviderVersionCheckResult {
	source, err := ResolveProviderSource(devsyConfig, name)
	if err != nil {
		return ProviderVersionCheckResult{Error: err.Error()}
	}
	_, currentTag := splitSourceAndTag(source)
	if currentTag == "" && p != nil && p.Config != nil {
		currentTag = p.Config.Version
	}
	versions, err := ListProviderVersions(devsyConfig, name, ListVersionsOptions{UseCache: false})
	if errors.Is(err, ErrVersionListUnsupported) {
		return ProviderVersionCheckResult{Current: currentTag, Unsupported: true}
	}
	if err != nil {
		return ProviderVersionCheckResult{Current: currentTag, Error: err.Error()}
	}
	return buildVersionCheckResult(currentTag, versions)
}

func buildVersionCheckResult(
	currentTag string,
	versions []ProviderVersion,
) ProviderVersionCheckResult {
	latest := ""
	if len(versions) > 0 {
		latest = versions[0].Tag
	}
	return ProviderVersionCheckResult{
		Current:         currentTag,
		Latest:          latest,
		UpdateAvailable: latest != "" && latest != currentTag,
	}
}
