package provider

import (
	"fmt"
	"strings"
)

// GithubAPIBaseURL is the base URL for GitHub API calls; overridden in tests.
var GithubAPIBaseURL = "https://api.github.com"

// ProviderVersionCheckResult is the per-provider result from CheckAllProviderVersions.
type ProviderVersionCheckResult struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Unsupported     bool   `json:"unsupported"`
	Error           string `json:"error,omitempty"`
}

type sourceKind int

const (
	sourceUnknown sourceKind = iota
	sourceGitHub
	sourceManifestURL
	sourceLocal
)

// ClassifyVersionSource categorises a canonical source string into its source kind.
func ClassifyVersionSource(canonical string) sourceKind {
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

// ListVersionsForSource dispatches to the appropriate lister based on source shape.
func ListVersionsForSource(source string, opts ListVersionsOptions) ([]ProviderVersion, error) {
	switch ClassifyVersionSource(source) {
	case sourceGitHub:
		org, repo, ok := parseGitHubSourcePath(source)
		if !ok {
			return nil, fmt.Errorf("invalid github source: %s", source)
		}
		return ListGitHubReleases(GithubAPIBaseURL, org, repo, opts.IncludePrerelease)
	case sourceManifestURL:
		return ListManifestVersions(source, opts.IncludePrerelease)
	case sourceLocal, sourceUnknown:
		return nil, ErrVersionListUnsupported
	}
	return nil, ErrVersionListUnsupported
}

// MarkCurrent flags the version whose tag matches the source's pinned tag.
func MarkCurrent(versions []ProviderVersion, canonicalSource string) []ProviderVersion {
	_, currentTag := SplitSourceAndTag(canonicalSource)
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

// RewriteSourceTag returns the source with its version tag replaced by the given one.
func RewriteSourceTag(source, tag string) (string, error) {
	if tag == "" {
		return "", fmt.Errorf("version tag must not be empty")
	}
	base, _ := SplitSourceAndTag(source)
	return base + "@" + tag, nil
}

// BuildVersionCheckResult constructs a ProviderVersionCheckResult from a current tag and list of versions.
func BuildVersionCheckResult(
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
