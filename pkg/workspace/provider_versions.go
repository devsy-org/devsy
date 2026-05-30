package workspace

import (
	"errors"
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

// ListProviderVersions returns available versions for the named provider, newest first.
// Returns ErrVersionListUnsupported when the source type can't be enumerated.
func ListProviderVersions(
	devsyConfig *config.Config,
	providerName string,
	opts ListVersionsOptions,
) ([]ProviderVersion, error) {
	return nil, ErrVersionListUnsupported
}

// ListVersionsOptions tunes the lister.
type ListVersionsOptions struct {
	UseCache          bool
	IncludePrerelease bool
}

// SetProviderVersion switches the provider to the given tag.
func SetProviderVersion(devsyConfig *config.Config, providerName, tag string) error {
	return errors.New("not implemented")
}
