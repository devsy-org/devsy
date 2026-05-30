package workspace

import (
	"errors"
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
