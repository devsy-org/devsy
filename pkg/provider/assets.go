package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/devsy-org/devsy/pkg/log"
)

// githubRawBaseURL serves repository content at a tag; tests point it at a local server.
var githubRawBaseURL = "https://raw.githubusercontent.com"

const (
	iconSchemeHTTP  = "http"
	iconSchemeHTTPS = "https"
)

// ErrIconSourceUnresolved reports that a relative icon cannot be tied to the
// installed provider version, so no repository-relative fetch is attempted.
var ErrIconSourceUnresolved = errors.New("provider source version is unresolved")

// IconLocation is where an icon reference points. Exactly one field is set.
type IconLocation struct {
	URL  string
	File string
}

// ResolveProviderIconReference resolves an author-supplied icon reference
// against the installed provider source. It performs no I/O except resolving
// symlinks for local manifests, so it is safe to call from list paths.
func ResolveProviderIconReference(source ProviderSource, reference string) (IconLocation, error) {
	parsed, err := parseIconReference(reference)
	if err != nil {
		return IconLocation{}, err
	}
	if parsed.Scheme != "" {
		return resolveAbsoluteIconURL(parsed)
	}
	relative, err := cleanRelativeIconPath(parsed)
	if err != nil {
		return IconLocation{}, err
	}
	return resolveRelativeIconReference(source, parsed, relative)
}

func parseIconReference(reference string) (*url.URL, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return nil, errors.New("icon reference is empty")
	}
	if strings.ContainsRune(reference, 0) {
		return nil, errors.New("icon reference contains a NUL byte")
	}

	parsed, err := url.Parse(reference)
	if err != nil {
		return nil, errors.New("icon reference is not a valid URL or path")
	}
	return parsed, nil
}

func resolveRelativeIconReference(
	source ProviderSource,
	parsed *url.URL,
	relative string,
) (IconLocation, error) {
	switch {
	case source.Internal:
		return IconLocation{}, errors.New("built-in providers have no source-relative icons")
	case source.Github != "":
		return resolveGithubIconReference(source, parsed, relative)
	case source.File != "":
		return resolveLocalIconReference(source.File, parsed, relative)
	default:
		return resolveURLRelativeIcon(source, parsed)
	}
}

func resolveGithubIconReference(
	source ProviderSource,
	parsed *url.URL,
	relative string,
) (IconLocation, error) {
	location, err := resolveGithubIcon(source, relative)
	if err != nil {
		return IconLocation{}, err
	}
	target, err := url.Parse(location.URL)
	if err != nil {
		return IconLocation{}, err
	}
	target.RawQuery, target.ForceQuery = parsed.RawQuery, parsed.ForceQuery
	target.Fragment, target.RawFragment = parsed.Fragment, parsed.RawFragment
	return IconLocation{URL: target.String()}, nil
}

func resolveLocalIconReference(
	manifestFile string,
	parsed *url.URL,
	relative string,
) (IconLocation, error) {
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return IconLocation{}, errors.New(
			"local icon path must not contain a query or fragment",
		)
	}
	return resolveLocalIcon(manifestFile, relative)
}

func resolveAbsoluteIconURL(parsed *url.URL) (IconLocation, error) {
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != iconSchemeHTTP && scheme != iconSchemeHTTPS {
		return IconLocation{}, fmt.Errorf("icon scheme %q is not supported", parsed.Scheme)
	}
	if parsed.Host == "" {
		return IconLocation{}, errors.New("icon URL has no host")
	}

	return IconLocation{URL: parsed.String()}, nil
}

// cleanRelativeIconPath validates a scheme-less reference and returns its
// decoded, cleaned, slash-separated path rooted at the logical manifest root.
func cleanRelativeIconPath(parsed *url.URL) (string, error) {
	if parsed.Host != "" || parsed.User != nil {
		return "", errors.New("icon reference must not contain a URL authority")
	}

	if err := validateRelativeIconPath(parsed.Path); err != nil {
		return "", err
	}
	cleaned := path.Clean(parsed.Path)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("icon reference escapes the provider manifest directory")
	}
	return cleaned, nil
}

func validateRelativeIconPath(decoded string) error {
	switch {
	case decoded == "":
		return errors.New("icon reference has no path")
	case strings.ContainsRune(decoded, 0):
		return errors.New("icon reference contains a NUL byte")
	case strings.ContainsRune(decoded, '\\'):
		return errors.New("icon reference must not contain backslashes")
	case strings.HasPrefix(decoded, "/"):
		return errors.New("icon reference must be relative to the provider manifest")
	}

	return nil
}

// localIconRelativePath reuses the source-reference path checks at the rooted
// file-read boundary, without replacing the logical root with a resolved path.
func localIconRelativePath(reference string) (string, error) {
	parsed, err := parseIconReference(reference)
	if err != nil {
		return "", err
	}
	return cleanRelativeIconPath(parsed)
}

func resolveGithubIcon(source ProviderSource, relative string) (IconLocation, error) {
	if source.ResolvedRef == "" {
		return IconLocation{}, ErrIconSourceUnresolved
	}

	repository := strings.TrimPrefix(source.Github, "github.com/")
	owner, repo, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return IconLocation{}, fmt.Errorf("invalid github source %q", source.Github)
	}

	target, err := url.Parse(githubRawBaseURL)
	if err != nil {
		return IconLocation{}, err
	}
	target.Path = path.Join("/", owner, repo, "refs/tags", source.ResolvedRef, relative)

	return IconLocation{URL: target.String()}, nil
}

func resolveURLRelativeIcon(source ProviderSource, relative *url.URL) (IconLocation, error) {
	base := source.ResolvedManifestURL
	if base == "" {
		base = logicalManifestURL(source.URL)
	}
	if base == "" {
		return IconLocation{}, ErrIconSourceUnresolved
	}

	target, err := url.Parse(base)
	if err != nil || (target.Scheme != iconSchemeHTTP && target.Scheme != iconSchemeHTTPS) ||
		target.Host == "" {
		return IconLocation{}, errors.New("provider manifest URL is not a valid http(s) URL")
	}

	target.User = nil
	target.RawQuery = ""
	target.ForceQuery = false
	target.Fragment = ""
	target.RawFragment = ""

	return IconLocation{URL: target.ResolveReference(relative).String()}, nil
}

// resolveLocalIcon keeps the icon inside the manifest directory even through
// symlinks; the returned path is fully resolved.
func resolveLocalIcon(manifestFile, relative string) (IconLocation, error) {
	if strings.ContainsRune(relative, ':') {
		return IconLocation{}, errors.New("local icon path must not contain ':'")
	}

	root, err := filepath.EvalSymlinks(filepath.Dir(manifestFile))
	if err != nil {
		return IconLocation{}, fmt.Errorf("resolve manifest directory: %w", err)
	}

	relativeTarget, err := resolveLocalIconTarget(root, relative)
	if err != nil {
		return IconLocation{}, err
	}
	return IconLocation{File: filepath.Join(root, relativeTarget)}, nil
}

// resolveLocalIconTarget keeps symlink targets within the canonical source
// directory. Callers opening the file must still use an os.Root boundary.
func resolveLocalIconTarget(root, relative string) (string, error) {
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return "", fmt.Errorf("resolve local icon: %w", err)
	}
	relativeTarget := mustRel(root, resolved)
	if !filepath.IsLocal(relativeTarget) {
		return "", errors.New("local icon resolves outside the provider manifest directory")
	}
	return relativeTarget, nil
}

// mustRel returns a path that is never local when target is not under root.
func mustRel(root, target string) string {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return ".." + string(os.PathSeparator)
	}
	return rel
}

// PrepareProviderIcons downloads, validates and caches the provider's light
// and dark icons, then selects the resulting immutable revision in
// cfg.IconRevision. Asset failures are recorded as missing variants and never
// returned; only cache I/O failures or cancellation are errors, in which case
// cfg.IconRevision stays empty. cfg.Source must already be authoritative and
// the call must precede saving the installed manifest.
func PrepareProviderIcons(
	ctx context.Context,
	devsyContext string,
	cfg *ProviderConfig,
	manifest []byte,
) error {
	if cfg == nil {
		return errors.New("provider config is nil")
	}
	cfg.IconRevision = ""
	if cfg.Icon == "" && cfg.IconDark == "" {
		return nil
	}

	cacheDir, err := GetProviderAssetsDir(devsyContext, cfg.Name)
	if err != nil {
		return err
	}

	fetcher := newIconFetcher()
	defer fetcher.close()

	light, dark := fetchIconVariants(ctx, fetcher, cfg)
	if err := ctx.Err(); err != nil {
		return err
	}

	revision, created, err := storeIconRevision(cacheDir, iconInputs{
		manifest: manifest,
		source:   cfg.Source,
		icon:     cfg.Icon,
		iconDark: cfg.IconDark,
	}, light, dark)
	if err != nil {
		return err
	}

	if created {
		logIconDiagnostic(cfg.Name, light, dark)
	}
	cfg.IconRevision = revision

	return nil
}

func fetchIconVariants(
	ctx context.Context,
	fetcher *iconFetcher,
	cfg *ProviderConfig,
) (light, dark iconVariant) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		light = fetcher.fetchVariant(ctx, cfg.Source, cfg.Icon)
	}()
	go func() {
		defer wg.Done()
		dark = fetcher.fetchVariant(ctx, cfg.Source, cfg.IconDark)
	}()
	wg.Wait()

	return light, dark
}

// logIconDiagnostic emits one concise line for a newly recorded revision with
// failed variants. It never fails the provider lifecycle.
func logIconDiagnostic(providerName string, light, dark iconVariant) {
	var failures []string
	if light.failure != "" {
		failures = append(failures, "light: "+light.failure)
	}
	if dark.failure != "" {
		failures = append(failures, "dark: "+dark.failure)
	}
	if len(failures) == 0 {
		return
	}

	log.Warnf(
		"provider %q icon unavailable, using fallback (%s)",
		providerName,
		strings.Join(failures, "; "),
	)
}
