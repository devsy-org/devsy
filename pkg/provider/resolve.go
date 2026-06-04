package provider

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/download"
	devsyhttp "github.com/devsy-org/devsy/pkg/http"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/providers"
)

// ResolveProvider resolves a provider source string into raw provider YAML
// bytes and a ProviderSource describing the origin (internal, URL, file, or
// GitHub release).
func ResolveProvider(
	providerSource string,
) ([]byte, *ProviderSource, error) {
	retSource := &ProviderSource{Raw: strings.TrimSpace(providerSource)}

	if out, ok := resolveInternalProvider(providerSource, retSource); ok {
		return out, retSource, nil
	}

	if out, err := tryResolveURLProvider(
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

	out, source, err := downloadProviderGithub(providerSource)
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
	providerSource string,
	retSource *ProviderSource,
) ([]byte, error) {
	out, ok, err := resolveURLProvider(providerSource, retSource)
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

	requestURL := buildGithubURL(path, release)

	body, err := download.File(requestURL)
	if err != nil {
		return nil, nil, fmt.Errorf("download: %w", err)
	}
	defer func() { _ = body.Close() }()

	out, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, err
	}

	return out, &ProviderSource{
		Raw:    originalPath,
		Github: path,
	}, nil
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
	providerSource string,
	retSource *ProviderSource,
) ([]byte, bool, error) {
	if !strings.HasPrefix(providerSource, "http://") &&
		!strings.HasPrefix(providerSource, "https://") {
		return nil, false, nil
	}

	log.Infof("downloading provider from %s", providerSource)
	out, err := downloadProvider(providerSource)
	if err != nil {
		return nil, true, fmt.Errorf("download provider: %w", err)
	}
	retSource.URL = providerSource
	return out, true, nil
}

func resolveFileProvider(
	providerSource string,
	retSource *ProviderSource,
) ([]byte, bool, error) {
	if !strings.HasSuffix(providerSource, ".yaml") && !strings.HasSuffix(providerSource, ".yml") {
		return nil, false, nil
	}

	if _, err := os.Stat(providerSource); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, true, fmt.Errorf("stat provider file %q: %w", providerSource, err)
	}

	// #nosec G304 - providerSource is user-provided path for loading provider config
	out, err := os.ReadFile(providerSource)
	if err != nil {
		return nil, true, fmt.Errorf("read provider file %q: %w", providerSource, err)
	}

	absPath, err := filepath.Abs(providerSource)
	if err != nil {
		return nil, true, fmt.Errorf("resolve absolute path for %q: %w", providerSource, err)
	}
	retSource.File = absPath
	return out, true, nil
}

func downloadProvider(url string) ([]byte, error) {
	resp, err := devsyhttp.GetHTTPClient().Get(url)
	if err != nil {
		return nil, fmt.Errorf("download binary: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func buildGithubURL(path, release string) string {
	if release == "" {
		return fmt.Sprintf("https://github.com/%s/releases/latest/download/provider.yaml", path)
	}
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/provider.yaml", path, release)
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
		// Canonicalize to github.com/<org>/<repo> so version helpers
		// (classifyVersionSource, parseGitHubSourcePath) recognize it.
		if strings.HasPrefix(src.Github, "github.com/") {
			return src.Github
		}
		return "github.com/" + src.Github
	default:
		return ""
	}
}
