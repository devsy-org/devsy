package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
)

const providerVersionCacheTTL = 6 * time.Hour

type providerVersionCacheEntry struct {
	SourceHash string            `json:"sourceHash"`
	Versions   []ProviderVersion `json:"versions"`
	FetchedAt  time.Time         `json:"fetchedAt"`
}

type providerVersionCache map[string]providerVersionCacheEntry

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

func LoadProviderVersionCache() (providerVersionCache, error) {
	path, err := providerVersionCachePath()
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- path is derived from providerVersionCachePath(), which controls the directory structure.
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return providerVersionCache{}, nil
		}
		return nil, err
	}
	cache := providerVersionCache{}
	if err := json.Unmarshal(data, &cache); err != nil {
		// Corrupt cache → start fresh.
		return providerVersionCache{}, nil
	}
	return cache, nil
}

func SaveProviderVersionCache(c providerVersionCache) error {
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

func (c providerVersionCache) Get(name, sourceHash string) (providerVersionCacheEntry, bool) {
	entry, ok := c[name]
	if !ok {
		return providerVersionCacheEntry{}, false
	}
	if entry.SourceHash != sourceHash {
		return entry, false
	}
	if time.Since(entry.FetchedAt) > providerVersionCacheTTL {
		return entry, false
	}
	return entry, true
}

func hashProviderSource(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}
