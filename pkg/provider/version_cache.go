package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
)

// ErrVersionListUnsupported indicates the provider's source type does not expose a list of versions.
var ErrVersionListUnsupported = errors.New("provider source does not support version listing")

// ErrVersionListRateLimited indicates upstream rate-limiting hit the lister.
var ErrVersionListRateLimited = errors.New("provider version list rate-limited")

// ProviderVersion describes one upstream release.
type ProviderVersion struct {
	Tag         string    `json:"tag"`
	PublishedAt time.Time `json:"publishedAt"`
	Prerelease  bool      `json:"prerelease"`
	Current     bool      `json:"current"`
}

// ListVersionsOptions tunes the lister.
type ListVersionsOptions struct {
	UseCache          bool
	IncludePrerelease bool
}

// SplitSourceAndTag splits a canonical provider source into its base and optional @tag suffix.
func SplitSourceAndTag(canonical string) (base, tag string) {
	if before, after, ok := strings.Cut(canonical, "@"); ok {
		return before, after
	}
	return canonical, ""
}

const providerVersionCacheTTL = 6 * time.Hour

// ProviderVersionCacheEntry is one provider's cached version list.
type ProviderVersionCacheEntry struct {
	SourceHash string            `json:"sourceHash"`
	Versions   []ProviderVersion `json:"versions"`
	FetchedAt  time.Time         `json:"fetchedAt"`
}

// ProviderVersionCache maps provider names to their cached version entries.
type ProviderVersionCache map[string]ProviderVersionCacheEntry

func providerVersionCachePath() (string, error) {
	// Check for DEVSY_HOME override (primarily used in tests).
	if home := os.Getenv(config.EnvHome); home != "" {
		return filepath.Join(home, "cache", "provider-versions.json"), nil
	}
	dir, err := config.GetConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cache", "provider-versions.json"), nil
}

func LoadProviderVersionCache() (ProviderVersionCache, error) {
	path, err := providerVersionCachePath()
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- path is derived from providerVersionCachePath(), which controls the directory structure.
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ProviderVersionCache{}, nil
		}
		return nil, err
	}
	cache := ProviderVersionCache{}
	if err := json.Unmarshal(data, &cache); err != nil {
		// Corrupt cache → start fresh.
		return ProviderVersionCache{}, nil
	}
	return cache, nil
}

func SaveProviderVersionCache(c ProviderVersionCache) error {
	path, err := providerVersionCachePath()
	if err != nil {
		return err
	}
	// #nosec G301 -- cache directory should be world-readable for library use.
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// #nosec G306 -- cache file is read-only user data, safe at 0o600.
	return os.WriteFile(path, data, 0o600)
}

func (c ProviderVersionCache) Get(name, sourceHash string) (ProviderVersionCacheEntry, bool) {
	entry, ok := c[name]
	if !ok {
		return ProviderVersionCacheEntry{}, false
	}
	if entry.SourceHash != sourceHash {
		return entry, false
	}
	if time.Since(entry.FetchedAt) > providerVersionCacheTTL {
		return entry, false
	}
	return entry, true
}

func HashProviderSource(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}
