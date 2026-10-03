package provider

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/devsy-org/devsy/pkg/hash"
)

// ValidateExternalDriverConfig checks declarations without accessing the filesystem.
// Template-selected drivers must be validated again after option resolution.
func ValidateExternalDriverConfig(agent ProviderAgentConfig) error {
	key := agent.External.Binary
	if key == "" || optionNameRegEx.MatchString(key) {
		return errors.New(
			"agent.external.binary must be an agent.binaries key containing only uppercase letters, numbers or underscores",
		)
	}
	if agent.External.ImageBackend != "" && agent.External.ImageBackend != DockerDriver &&
		agent.External.ImageBackend != "none" {
		return errors.New("agent.external.imageBackend must be docker or none")
	}
	if err := validateExternalArgs(agent.External.Args); err != nil {
		return err
	}
	locations := agent.Binaries[key]
	if len(locations) == 0 {
		return fmt.Errorf(
			"agent.external.binary %q must reference a nonempty agent.binaries declaration",
			key,
		)
	}
	return validateExternalLocations(key, locations)
}

func validateExternalArgs(args []string) error {
	if len(args) > 0 && args[0] == "" {
		return errors.New("agent.external.args must not start with an empty argument")
	}
	for index, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("agent.external.args[%d] contains a NUL byte", index)
		}
	}
	return nil
}

func validateExternalLocations(key string, locations []*ProviderBinary) error {
	platforms := map[string]bool{}
	for _, binary := range locations {
		if binary == nil {
			return fmt.Errorf("agent.binaries.%s contains a null binary declaration", key)
		}
		if err := validateBinary("agent.binaries", key, binary); err != nil {
			return err
		}
		decoded, err := hex.DecodeString(binary.Checksum)
		if err != nil || len(decoded) != 32 {
			return fmt.Errorf(
				"agent.binaries.%s requires a SHA-256 checksum for %s/%s",
				key,
				binary.OS,
				binary.Arch,
			)
		}
		platform := binary.OS + "/" + binary.Arch
		if platforms[platform] {
			return fmt.Errorf("agent.binaries.%s has duplicate declarations for %s", key, platform)
		}
		platforms[platform] = true
	}
	return nil
}

// ResolveExternalRuntimeBinary verifies an already prepared agent-side executable.
// It never downloads, executes, or removes a file, including on checksum failure.
func ResolveExternalRuntimeBinary(agent ProviderAgentConfig, binariesDir string) (string, error) {
	if agent.Driver != ExternalDriver {
		return "", errors.New("agent.driver must be external")
	}
	if err := ValidateExternalDriverConfig(agent); err != nil {
		return "", err
	}
	if !filepath.IsAbs(binariesDir) {
		return "", errors.New("agent binaries directory must be an absolute path")
	}
	key := agent.External.Binary
	for _, binary := range agent.Binaries[key] {
		if binary.OS == runtime.GOOS && binary.Arch == runtime.GOARCH {
			resolved := getBinaryPath(binary, filepath.Join(binariesDir, strings.ToLower(key)))
			return verifyExternalExecutable(resolved, binary.Checksum)
		}
	}
	return "", fmt.Errorf(
		"external runtime binary %q has no declaration for agent platform %s/%s",
		key,
		runtime.GOOS,
		runtime.GOARCH,
	)
}

func verifyExternalExecutable(path, expected string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("external runtime executable must be an absolute path")
	}
	metadata, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("external runtime executable is not prepared: %w", err)
	}
	if !metadata.Mode().IsRegular() {
		return "", errors.New("external runtime executable must be a regular file")
	}
	if runtime.GOOS != "windows" && metadata.Mode().Perm()&0o111 == 0 {
		return "", errors.New("external runtime executable must have executable permissions")
	}
	actual, err := hash.File(path)
	if err != nil {
		return "", fmt.Errorf("verify external runtime executable: %w", err)
	}
	if !strings.EqualFold(actual, expected) {
		return "", errors.New("external runtime executable checksum verification failed")
	}
	return filepath.Clean(path), nil
}
