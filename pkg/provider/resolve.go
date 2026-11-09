package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/download"
	"github.com/devsy-org/devsy/pkg/gitcredentials"
	devsyhttp "github.com/devsy-org/devsy/pkg/http"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/providers"
)

// DeprecatedMicrosandboxSource explicitly selects the legacy built-in provider.
const DeprecatedMicrosandboxSource = "builtin:microsandbox"

// ResolveProvider resolves a provider source string into raw provider YAML
// bytes and a ProviderSource describing the origin (internal, URL, file, or
// GitHub release).
func ResolveProvider(
	ctx context.Context,
	providerSource string,
) ([]byte, *ProviderSource, error) {
	if providerSource == DeprecatedMicrosandboxSource {
		// Keep stored legacy records compatible with embedded exec refresh and reload.
		providerSource = MicrosandboxDriver
	}
	retSource := &ProviderSource{Raw: strings.TrimSpace(providerSource)}

	if out, ok := resolveInternalProvider(providerSource, retSource); ok {
		return out, retSource, nil
	}

	if out, err := tryResolveURLProvider(
		ctx,
		providerSource,
		retSource,
	); hasOutputOrError(
		out,
		err,
	) {
		return out, retSource, err
	}

	if out, err := tryResolveFileProvider(providerSource, retSource); hasOutputOrError(out, err) {
		return out, retSource, err
	}

	out, source, err := downloadProviderGithub(ctx, providerSource)
	if len(out) > 0 || err != nil {
		return out, source, err
	}

	return nil, nil, fmt.Errorf(
		"provider type not recognized: specify a local file, url, or github repository",
	)
}

func hasOutputOrError(out []byte, err error) bool {
	return out != nil || err != nil
}

func tryResolveURLProvider(
	ctx context.Context,
	providerSource string,
	retSource *ProviderSource,
) ([]byte, error) {
	out, ok, err := resolveURLProvider(ctx, providerSource, retSource)
	if !ok {
		return nil, nil
	}
	return out, err
}

func tryResolveFileProvider(
	providerSource string,
	retSource *ProviderSource,
) ([]byte, error) {
	out, ok, err := resolveFileProvider(providerSource, retSource)
	if !ok {
		return nil, nil
	}
	return out, err
}

func downloadProviderGithub(
	ctx context.Context,
	originalPath string,
) ([]byte, *ProviderSource, error) {
	path := strings.TrimPrefix(originalPath, "github.com/")

	release := ""
	index := strings.LastIndex(path, "@")
	if index != -1 {
		release = path[index+1:]
		path = path[:index]
	}

	splitted := strings.Split(strings.TrimSuffix(path, "/"), "/")
	if len(splitted) == 1 {
		path = config.RepoOwner + "/" + config.ProviderPrefix + path
	} else if len(splitted) != 2 {
		return nil, nil, fmt.Errorf(
			"invalid github path format: expected 'owner/repo' or 'provider-name', got %q",
			originalPath,
		)
	}

	resolvedRef := release
	if resolvedRef == "" {
		resolvedRef = resolveLatestGithubRelease(ctx, path)
	}
	requestURL := buildGithubURL(path, resolvedRef)

	body, err := download.File(
		ctx,
		requestURL,
		download.WithCredentialResolver(gitcredentials.Resolver{}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("download: %w", err)
	}
	defer func() { _ = body.Close() }()

	out, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, err
	}

	source := &ProviderSource{
		Raw:    originalPath,
		Github: path,
	}
	if resolvedRef != "" {
		source.ResolvedRef = resolvedRef
		source.ResolvedManifestURL = requestURL
	}

	return out, source, nil
}

// resolveLatestGithubRelease pins an unversioned source to the release GitHub
// currently designates as latest so manifest and icons come from one tag. A
// metadata failure returns "" and leaves provenance unresolved: the manifest
// still installs through the latest-download redirect.
func resolveLatestGithubRelease(ctx context.Context, path string) string {
	tag, err := FetchLatestGitHubReleaseTag(ctx, path, gitcredentials.Resolver{})
	if err != nil {
		log.Debugf("resolve latest release of %s: %v", path, err)
		return ""
	}
	return tag
}

func resolveInternalProvider(
	providerSource string,
	retSource *ProviderSource,
) ([]byte, bool) {
	internalProviders := providers.GetBuiltInProviders()
	if internalProviders[providerSource] != "" {
		retSource.Internal = true
		return []byte(internalProviders[providerSource]), true
	}
	return nil, false
}

func resolveURLProvider(
	ctx context.Context,
	providerSource string,
	retSource *ProviderSource,
) ([]byte, bool, error) {
	if !strings.HasPrefix(providerSource, "http://") &&
		!strings.HasPrefix(providerSource, "https://") {
		return nil, false, nil
	}

	log.Debugf("downloading provider from %s", providerSource)
	out, err := downloadProvider(ctx, providerSource)
	if err != nil {
		return nil, true, fmt.Errorf("download provider: %w", err)
	}
	retSource.URL = providerSource
	retSource.ResolvedManifestURL = logicalManifestURL(providerSource)
	return out, true, nil
}

func resolveFileProvider(
	providerSource string,
	retSource *ProviderSource,
) ([]byte, bool, error) {
	explicit := isExplicitManifestPath(providerSource)
	if skipManifestSource(providerSource, explicit) {
		return nil, false, nil
	}
	filePath, err := expandManifestPath(providerSource)
	if err != nil {
		return nil, true, err
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return resolveFileStatError(filePath, explicit, err)
	}
	if !info.Mode().IsRegular() {
		if explicit {
			return nil, true, fmt.Errorf("provider manifest is not a regular file: %s", filePath)
		}
		return nil, false, nil
	}

	// #nosec G304 - providerSource is user-provided path for loading provider config
	out, err := os.ReadFile(filePath)
	if err != nil {
		return nil, true, fmt.Errorf("read provider file %q: %w", providerSource, err)
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, true, fmt.Errorf("resolve absolute path for %q: %w", providerSource, err)
	}
	retSource.File = absPath
	return out, true, nil
}

func skipManifestSource(source string, explicit bool) bool {
	return !explicit && !hasYAMLSuffix(source)
}

func isExplicitManifestPath(source string) bool {
	return filepath.IsAbs(source) || strings.HasPrefix(source, "/") ||
		strings.HasPrefix(source, "./") ||
		strings.HasPrefix(source, "../") ||
		strings.HasPrefix(source, "~") ||
		strings.Contains(source, `\`) ||
		isWindowsDrivePath(source)
}

func isWindowsDrivePath(source string) bool {
	if len(source) < 2 || source[1] != ':' {
		return false
	}
	letter := source[0] | 0x20
	return letter >= 'a' && letter <= 'z' &&
		(runtime.GOOS == windowsOS || (len(source) > 2 && source[2] == '/'))
}

func expandManifestPath(source string) (string, error) {
	if source != "~" && !strings.HasPrefix(source, "~/") && !strings.HasPrefix(source, `~\`) {
		return source, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, strings.TrimLeft(source[1:], `/\`)), nil
}

func hasYAMLSuffix(source string) bool {
	return strings.HasSuffix(source, ".yaml") || strings.HasSuffix(source, ".yml")
}

func resolveFileStatError(source string, manifestPath bool, err error) ([]byte, bool, error) {
	if !os.IsNotExist(err) {
		return nil, true, fmt.Errorf("stat provider file %q: %w", source, err)
	}
	if !manifestPath {
		return nil, false, nil
	}
	absPath, absErr := filepath.Abs(source)
	if absErr != nil {
		return nil, true, fmt.Errorf("resolve absolute path for %q: %w", source, absErr)
	}
	return nil, true, fmt.Errorf("provider manifest file not found: %s", absPath)
}

func downloadProvider(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := devsyhttp.GetHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("download binary: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// githubWebBaseURL hosts release downloads; tests point it at a local server.
var githubWebBaseURL = "https://github.com"

func buildGithubURL(path, release string) string {
	if release == "" {
		return fmt.Sprintf("%s/%s/releases/latest/download/provider.yaml", githubWebBaseURL, path)
	}
	return fmt.Sprintf(
		"%s/%s/releases/download/%s/provider.yaml",
		githubWebBaseURL,
		path,
		url.PathEscape(release),
	)
}

// logicalManifestURL drops credentials, query and fragment so the stored base
// for sibling assets never carries secrets from the manifest request.
func logicalManifestURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}

// GetProviderSource returns a canonical source string for the provider, used
// for version checks and updates.
func GetProviderSource(src ProviderSource, configName string) string {
	switch {
	case src.Internal:
		if src.Raw == "" {
			return configName
		}
		return src.Raw
	case src.URL != "":
		return src.URL
	case src.File != "":
		return src.File
	case src.Github != "":
		return githubSourceString(src)
	default:
		return ""
	}
}

// githubSourceString canonicalizes a GitHub source to github.com/<org>/<repo>
// so version helpers (classifyVersionSource, parseGitHubSourcePath) recognize
// it, and applies the tag from Raw when there is one.
func githubSourceString(src ProviderSource) string {
	githubSource, githubTag := SplitSourceAndTag(src.Github)
	if !strings.HasPrefix(githubSource, "github.com/") {
		githubSource = "github.com/" + githubSource
	}
	_, rawTag := SplitSourceAndTag(src.Raw)
	if rawTag != "" {
		githubTag = rawTag
	}
	if githubTag != "" {
		return githubSource + "@" + githubTag
	}
	return githubSource
}
