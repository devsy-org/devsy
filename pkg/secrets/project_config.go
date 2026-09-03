package secrets

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

const ProjectConfigPath = ".devsy/config.yaml"

// ProjectConfig is the repository-owned subset of Devsy configuration used by
// secret discovery. It is intentionally declarative and does not execute code.
type ProjectConfig struct {
	SecretSources []SourceConfig `json:"secretSources,omitempty" yaml:"secretSources,omitempty"`
	Secrets       []string       `json:"secrets,omitempty"       yaml:"secrets,omitempty"`
}

func ParseProjectConfig(data []byte) (*ProjectConfig, error) {
	cfg := &ProjectConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ProjectConfigPath, err)
	}
	if err := ValidateProjectConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
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
// returns a normalized repository-relative slash path. Repository config must
// never be able to read arbitrary host files.
func CleanProjectSourcePath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return "", fmt.Errorf("source path must not be empty")
	}
	if strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("source path %q must be relative to the repository root", value)
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("source path %q escapes the repository root", value)
	}
	return strings.TrimPrefix(clean, "./"), nil
}

// LoadProjectConfigFromRoot loads repository-owned config from a local checkout.
// A missing .devsy/config.yaml is not an error.
func LoadProjectConfigFromRoot(root string) (*ProjectConfig, bool, error) {
	configPath := filepath.Join(root, filepath.FromSlash(ProjectConfigPath))
	// #nosec G304 -- configPath is intentionally rooted under the selected repository.
	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read %s: %w", ProjectConfigPath, err)
	}
	cfg, err := ParseProjectConfig(data)
	if err != nil {
		return nil, false, err
	}
	return cfg, true, nil
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
