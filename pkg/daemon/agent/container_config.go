package agent

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
)

const ContainerDaemonConfigPath = "/var/run/secrets/" + config.BinaryName + "/daemon_config"

func LoadContainerDaemonConfig() (*DaemonConfig, error) {
	encoded, err := readContainerDaemonConfig(ContainerDaemonConfigPath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(encoded) == "" {
		return nil, nil
	}
	return DecodeDaemonConfig(encoded)
}

func readContainerDaemonConfig(path string) (string, error) {
	// Projected Kubernetes secrets use symlinks and take precedence over the
	// process environment, matching the daemon's existing config source.
	raw, err := os.ReadFile(path) // #nosec G304 -- fixed secret path or isolated test fixture
	if errors.Is(err, os.ErrNotExist) {
		return os.Getenv(config.EnvWorkspaceDaemonConfig), nil
	}
	if err != nil {
		return "", fmt.Errorf("read container daemon config: %w", err)
	}
	encoded := string(raw)
	if strings.TrimSpace(encoded) == "" {
		return "", errors.New("mounted container daemon config is empty")
	}
	return encoded, nil
}
