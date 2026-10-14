package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var snapshotLocalEnv = regexp.MustCompile(`\$\{localEnv:([^}:]+)\}`)

// SnapshotBuildContext resolves persisted metadata only for subtractive artifact exclusions.
func SnapshotBuildContext(result *Result) (string, error) {
	metadata, err := snapshotConfigMetadata(result)
	if err != nil {
		return "", err
	}
	origin, err := snapshotConfigOrigin(metadata, result.SubstitutionContext)
	if err != nil {
		return "", err
	}
	if err := validateSnapshotContextEnv(
		metadata.Config,
		result.SubstitutionContext.Env,
	); err != nil {
		return "", err
	}
	var parsed DevContainerConfig
	ctx := *result.SubstitutionContext
	if err := Substitute(&ctx, metadata.Config, &parsed); err != nil {
		return "", fmt.Errorf("resolve snapshot build context: %w", err)
	}
	// Substitute uses JSON conversion, which omits Origin.
	parsed.Origin = origin
	buildContext := GetContextPath(&parsed)
	if strings.Contains(buildContext, "${") {
		return "", fmt.Errorf("workspace result has unresolved build context; run `devsy up` first")
	}
	return buildContext, nil
}

func snapshotConfigOrigin(
	metadata *DevContainerConfigWithPath,
	ctx *SubstitutionContext,
) (string, error) {
	if filepath.IsAbs(metadata.Config.Origin) {
		return metadata.Config.Origin, nil
	}
	if !filepath.IsAbs(ctx.LocalWorkspaceFolder) || !filepath.IsLocal(metadata.Path) {
		return "", fmt.Errorf(
			"workspace result has invalid local build context metadata; run `devsy up` first",
		)
	}
	// Persisted Path is relative to the content root before any Git subpath.
	return filepath.Join(ctx.LocalWorkspaceFolder, filepath.FromSlash(metadata.Path)), nil
}

func validateSnapshotContextEnv(config *DevContainerConfig, env map[string]string) error {
	for _, field := range []string{config.GetContext(), config.GetDockerfile()} {
		for _, match := range snapshotLocalEnv.FindAllStringSubmatch(field, -1) {
			if !snapshotEnvPresent(env, match[1]) {
				return fmt.Errorf(
					"workspace result has unresolved build context; run `devsy up` first",
				)
			}
		}
	}
	return nil
}

func snapshotEnvPresent(env map[string]string, name string) bool {
	if _, present := env[name]; present {
		return true
	}
	if filepath.Separator == '\\' {
		for key := range env {
			if strings.EqualFold(key, name) {
				return true
			}
		}
	}
	return false
}

func snapshotConfigMetadata(result *Result) (*DevContainerConfigWithPath, error) {
	if result == nil || result.DevContainerConfigWithPath == nil ||
		result.DevContainerConfigWithPath.Config == nil || result.SubstitutionContext == nil {
		return nil, fmt.Errorf(
			"workspace result is missing build context metadata; run `devsy up` first",
		)
	}
	return result.DevContainerConfigWithPath, nil
}
