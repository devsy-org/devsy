package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/tailscale/hujson"
	"sigs.k8s.io/yaml"
)

// ProjectConfig is the repository-owned subset of Devsy configuration used by
// secret discovery.
type ProjectConfig struct {
	SecretSources []SourceConfig `json:"secretSources,omitempty" yaml:"secretSources,omitempty"`
	Secrets       []string       `json:"secrets,omitempty"       yaml:"secrets,omitempty"`
}

type devContainerCustomizationsWrapper struct {
	Customizations map[string]json.RawMessage `json:"customizations"`
}

func ParseProjectConfig(data []byte) (*ProjectConfig, error) {
	if cfg, handled, err := parseDevContainerCustomizations(data); handled {
		return cfg, err
	}
	cfg := &ProjectConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse project secret configuration: %w", err)
	}
	if err := ValidateProjectConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func parseDevContainerCustomizations(data []byte) (*ProjectConfig, bool, error) {
	normalized, err := hujson.Standardize(data)
	if err != nil {
		return nil, false, nil
	}
	var wrapper devContainerCustomizationsWrapper
	if err := json.Unmarshal(normalized, &wrapper); err != nil || wrapper.Customizations == nil {
		return nil, false, nil
	}
	rawDevsy := wrapper.Customizations["devsy"]
	if len(rawDevsy) == 0 {
		rawDevsy = wrapper.Customizations[config.BinaryName]
	}
	if len(rawDevsy) == 0 {
		return nil, true, nil
	}
	cfg := &ProjectConfig{}
	if err := json.Unmarshal(rawDevsy, cfg); err != nil {
		return nil, true, fmt.Errorf("parse customizations.devsy: %w", err)
	}
	if err := ValidateProjectConfig(cfg); err != nil {
		return nil, true, err
	}
	return cfg, true, nil
}

func ValidateProjectConfig(cfg *ProjectConfig) error {
	if cfg == nil {
		return nil
	}
	seen, err := validateProjectSources(cfg.SecretSources)
	if err != nil {
		return err
	}
	return validateProjectSecrets(cfg.Secrets, seen)
}

func validateProjectSources(sources []SourceConfig) (map[string]struct{}, error) {
	seen := make(map[string]struct{}, len(sources))
	for i, source := range sources {
		if err := validateProjectSource(i, source, seen); err != nil {
			return nil, err
		}
		seen[source.Name] = struct{}{}
	}
	return seen, nil
}

func validateProjectSource(index int, source SourceConfig, seen map[string]struct{}) error {
	if err := ValidateSourceName(source.Name); err != nil {
		return fmt.Errorf("secretSources[%d]: %w", index, err)
	}
	if source.Name == LocalSourceName {
		return fmt.Errorf("secretSources[%d]: source name %q is reserved", index, LocalSourceName)
	}
	if source.Type != SOPSFormatter {
		return fmt.Errorf("secretSources[%d]: unsupported source type %q", index, source.Type)
	}
	if _, exists := seen[source.Name]; exists {
		return fmt.Errorf("duplicate project secret source %q", source.Name)
	}
	if _, err := CleanProjectSourcePath(source.Path); err != nil {
		return fmt.Errorf("secret source %q: %w", source.Name, err)
	}
	return nil
}

func validateProjectSecrets(values []string, sources map[string]struct{}) error {
	for _, value := range values {
		if err := validateProjectSecret(value, sources); err != nil {
			return err
		}
	}
	return nil
}

func validateProjectSecret(value string, sources map[string]struct{}) error {
	ref, err := ParseRef(value)
	if err != nil {
		return fmt.Errorf("project secret %q: %w", value, err)
	}
	if ref.Source == LocalSourceName {
		return fmt.Errorf("project configuration may not attach local secret %q", ref.Name)
	}
	if _, ok := sources[ref.Source]; !ok {
		return fmt.Errorf("project secret %q references undefined source %q", value, ref.Source)
	}
	return nil
}

// CleanProjectSourcePath validates a repository-controlled source path and
// returns a normalized repository-relative slash path.
func CleanProjectSourcePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("source path must not be empty")
	}
	if err := validateRelativePath(value); err != nil {
		return "", err
	}
	normalized := strings.ReplaceAll(value, `\`, "/")
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("source path %q escapes the repository root", value)
	}
	return strings.TrimPrefix(clean, "./"), nil
}

func validateRelativePath(value string) error {
	if filepath.VolumeName(value) != "" || isWindowsAbs(value) {
		return fmt.Errorf("source path %q must be relative to the repository root", value)
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\`) {
		return fmt.Errorf("source path %q must be relative to the repository root", value)
	}
	return nil
}

func isWindowsAbs(value string) bool {
	if len(value) >= 2 && value[1] == ':' {
		c := value[0]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, `//`)
}

// LoadProjectConfigFromRoot loads repository-owned config from a local checkout.
// A missing configuration is not an error.
func LoadProjectConfigFromRoot(root string) (*ProjectConfig, bool, error) {
	return LoadProjectConfigFromRootWithOptions(root, "", "")
}

// LoadProjectConfigFromRootWithOptions loads repository-owned config from a local checkout,
// checking the specified devcontainer path, conventional root devcontainer locations,
// and profile-specific devcontainer directories for customizations.devsy.
func LoadProjectConfigFromRootWithOptions(
	root, devContainerPath, devContainerID string,
) (*ProjectConfig, bool, error) {
	candidates, err := devContainerCandidates(devContainerPath, devContainerID, root)
	if err != nil {
		return nil, false, err
	}
	for _, relPath := range candidates {
		if cfg, found, err := loadCandidateConfig(root, relPath); found || err != nil {
			return cfg, found, err
		}
	}
	return nil, false, nil
}

func devContainerCandidates(devContainerPath, devContainerID, root string) ([]string, error) {
	if devContainerPath != "" {
		clean, err := CleanProjectSourcePath(devContainerPath)
		if err != nil {
			return nil, err
		}
		return []string{clean}, nil
	}
	candidates := []string{
		path.Join(".devcontainer", "devcontainer.json"),
		".devcontainer.json",
	}
	if devContainerID != "" {
		cleanID, err := CleanProjectSourcePath(devContainerID)
		if err != nil {
			return nil, fmt.Errorf("invalid devcontainer id %q: %w", devContainerID, err)
		}
		return []string{path.Join(".devcontainer", cleanID, "devcontainer.json")}, nil
	}
	if nested := findNestedDevContainer(root); nested != "" {
		candidates = append(candidates, nested)
	}
	return candidates, nil
}

func findNestedDevContainer(root string) string {
	devcontainerDir := filepath.Join(root, ".devcontainer")
	entries, err := os.ReadDir(devcontainerDir)
	if err != nil {
		return ""
	}
	var nested []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cand := filepath.Join(devcontainerDir, entry.Name(), "devcontainer.json")
		if _, err := os.Stat(cand); err == nil {
			nested = append(nested, path.Join(".devcontainer", entry.Name(), "devcontainer.json"))
		}
	}
	if len(nested) == 1 {
		return nested[0]
	}
	return ""
}

func loadCandidateConfig(root, relPath string) (*ProjectConfig, bool, error) {
	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	// #nosec G304 -- fullPath is rooted under the selected repository.
	data, err := os.ReadFile(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read %s: %w", relPath, err)
	}
	cfg, err := ParseProjectConfig(data)
	if err != nil {
		return nil, false, fmt.Errorf("parse %s: %w", relPath, err)
	}
	if cfg != nil && (len(cfg.SecretSources) > 0 || len(cfg.Secrets) > 0) {
		return cfg, true, nil
	}
	return nil, false, nil
}

// ResolveProjectSourcePath converts a repository-controlled relative path to a
// local path while enforcing containment, including after symlink resolution.
func ResolveProjectSourcePath(root, value string) (string, error) {
	candidate, resolvedRoot, err := projectSourceCandidate(root, value)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve secret source path %q: %w", value, err)
	}
	if err := ensurePathContained(resolvedRoot, resolved, value); err != nil {
		return "", err
	}
	if err := ensureRegularFile(resolved, value); err != nil {
		return "", err
	}
	return resolved, nil
}

func projectSourceCandidate(root, value string) (string, string, error) {
	clean, err := CleanProjectSourcePath(value)
	if err != nil {
		return "", "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", "", fmt.Errorf("resolve repository root: %w", err)
	}
	return filepath.Join(rootAbs, filepath.FromSlash(clean)), resolvedRoot, nil
}

func ensurePathContained(root, candidate, original string) error {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("source path %q escapes the repository root", original)
	}
	return nil
}

func ensureRegularFile(filePath, original string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("stat secret source path %q: %w", original, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("secret source path %q is not a regular file", original)
	}
	return nil
}
