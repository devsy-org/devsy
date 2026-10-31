package kubernetes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

// ImageDetails reads the image config from the file set in the IMAGE_CONFIG_FILE
// option. It returns nil when the option is empty, so callers fall back to
// reading the config from the registry.
func (k *KubernetesDriver) ImageDetails(imageName string) (*config.ImageDetails, error) {
	if k.options.ImageConfigFile == "" {
		return nil, nil
	}

	log.Debugf("Reading image config for %q from %q", imageName, k.options.ImageConfigFile)
	return loadImageDetails(k.options.ImageConfigFile, imageName)
}

// loadImageDetails parses an image config file. It accepts the JSON of
// config.ImageDetails, an OCI image config (e.g. from `crane config`) or a
// single `docker image inspect` object, since field names match
// case-insensitively and unknown fields are ignored.
func loadImageDetails(path, imageName string) (*config.ImageDetails, error) {
	// #nosec G304 -- path comes from the operator-controlled IMAGE_CONFIG_FILE provider option
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read image config file %q: %w", path, err)
	}

	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return nil, fmt.Errorf("image config file %q must contain a JSON object", path)
	}

	details := &config.ImageDetails{}
	if err := json.Unmarshal(data, details); err != nil {
		return nil, fmt.Errorf("parse image config file %q: %w", path, err)
	}

	// An image may legitimately have an empty config, so check that the
	// config object is present rather than that any of its fields are set.
	var probe struct {
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("parse image config file %q: %w", path, err)
	}
	if len(probe.Config) == 0 || probe.Config[0] != '{' {
		return nil, fmt.Errorf("image config file %q has no config object", path)
	}

	if details.ID == "" {
		details.ID = imageName
	}

	return details, nil
}
